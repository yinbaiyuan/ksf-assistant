import Darwin
import KSFAssistantCore
import Foundation

enum CoreServiceError: Error, LocalizedError {
    case executableMissing
    case processStopped
    case pipeReadFailed(String)
    case invalidResponse
    case remote(String)

    var errorDescription: String? {
        switch self {
        case .executableMissing:
            return "未找到 KSFAssistant 核心服务。"
        case .processStopped:
            return "KSFAssistant 核心服务已停止。"
        case let .pipeReadFailed(message):
            return "读取 KSFAssistant 核心服务失败：\(message)"
        case .invalidResponse:
            return "核心服务返回了无法识别的数据。"
        case let .remote(message):
            return message
        }
    }
}

struct CoreServiceCreatedTask: Decodable {
    let threadId: String
    let name: String
    let prompt: String
    let submission: String
}


struct CoreServiceDashboard {
    let coreVersion: String
    let usage: UsageSnapshot
    let usageStatus: String
    let rateError: String?
    let tokenError: String?
    let activity: TaskActivitySnapshot
    let projects: ProjectDashboardSnapshot
    let feishu: FeishuServiceSnapshot
    let feishuLinks: [FeishuTaskLinkSnapshot]
}

enum CoreServicePipeIO {
    static func readChunk(from handle: FileHandle) throws -> Data {
        var bytes = [UInt8](repeating: 0, count: 64 * 1_024)
        while true {
            let count = bytes.withUnsafeMutableBytes { buffer -> Int in
                guard let baseAddress = buffer.baseAddress else { return 0 }
                return Darwin.read(handle.fileDescriptor, baseAddress, buffer.count)
            }
            if count > 0 {
                return Data(bytes.prefix(count))
            }
            if count == 0 {
                return Data()
            }
            if errno == EINTR {
                continue
            }
            let message = String(cString: strerror(errno))
            throw CoreServiceError.pipeReadFailed(message)
        }
    }
}

actor CoreServiceProcessClient {
    private var process: Process?
    private var connection: JSONRPCPipeConnection?
    private var initialization: Task<Void, Error>?
    private var stopping = false

    func start(ksfRoot: String) async throws {
        if let initialization { return try await initialization.value }
        guard !stopping else { throw CoreServiceError.processStopped }
        guard process == nil else { return }
        guard let executable = Self.locateExecutable() else {
            throw CoreServiceError.executableMissing
        }
        let process = Process()
        let inputPipe = Pipe()
        let outputPipe = Pipe()
        let errorPipe = Pipe()
        process.executableURL = executable
        process.currentDirectoryURL = executable.deletingLastPathComponent()
        var environment = ProcessInfo.processInfo.environment
        if let runtime = Self.locateFeishuRuntime() {
            environment["KSF_ASSISTANT_MANAGED"] = "1"
            environment["KSF_ASSISTANT_FEISHU_BRIDGE"] = runtime.bridge.path
            environment["KSF_ASSISTANT_LARK_CLI"] = runtime.larkCLI.path
            environment["FEISHU_BRIDGE_DATA_DIR"] = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent(".config/feishu-bridge", isDirectory: true).path
        }
        process.environment = environment
        process.standardInput = inputPipe
        process.standardOutput = outputPipe
        process.standardError = errorPipe
        errorPipe.fileHandleForReading.readabilityHandler = { handle in
            _ = handle.availableData
        }
        try process.run()
        self.process = process
        let connection = JSONRPCPipeConnection(input: inputPipe.fileHandleForWriting, output: outputPipe.fileHandleForReading)
        self.connection = connection
        process.terminationHandler = { _ in connection.close() }
        let initialization = Task<Void, Error> {
            _ = try await requestData(method: "initialize", params: [
                "clientInfo": [
                    "name": "ksf_assistant_macos",
                    "title": "KSFAssistant for macOS",
                    "version": "0.11.0-preview.1",
                ],
                "integrations": ["ksfRoot": ksfRoot],
            ])
        }
        self.initialization = initialization
        defer { self.initialization = nil }
        do {
            try await initialization.value
        } catch {
            stopProcess()
            throw error
        }
    }

    func updateIntegrationContext(ksfRoot: String) async throws {
        _ = try await requestData(method: "integration/context/update", params: [
            "ksfRoot": ksfRoot,
        ])
    }

    func dashboard(
        ksfRoot: String,
        pinnedProjectIDs: Set<String>,
        pricingSelection: PricingSelection
    ) async throws -> CoreServiceDashboard {
        let data = try await requestData(method: "dashboard/read", params: [
            "ksfRoot": ksfRoot,
            "pinnedProjectIds": Array(pinnedProjectIDs).sorted(),
            "pricingSelection": Self.pricingSelectionObject(pricingSelection),
        ])
        let dto = try Self.decoder().decode(DashboardDTO.self, from: data)
        return dto.value
    }

    func tokenHistory(dayCount: Int = 30) async throws -> [DailyUsageBucket] {
        try await decode(method: "token/history/read", params: ["dayCount": dayCount])
    }

    func tokenHistoryComparison(
        dayCount: Int = 30,
        pricingSelection: PricingSelection,
        repriceOnly: Bool = false
    ) async throws -> TokenHistoryComparison {
        try await decode(method: "token/history/compare", params: [
            "dayCount": dayCount,
            "pricingSelection": Self.pricingSelectionObject(pricingSelection),
            "repriceOnly": repriceOnly,
        ])
    }

    func pricingCatalog(customPlans: [PricingPlan]) async throws -> PricingCatalog {
        try await decode(method: "pricing/catalog/read", params: [
            "customPlans": customPlans.map(Self.pricingPlanObject),
        ])
    }

    func createTask(projectID: String, ksfRoot: String, purpose: String) async throws -> CoreServiceCreatedTask {
        try await decode(method: "task/create", params: [
            "projectId": projectID,
            "ksfRoot": ksfRoot,
            "purpose": purpose,
        ])
    }

    func submitTask(threadID: String, cwd: String, prompt: String) async throws {
        _ = try await requestData(method: "task/submit", params: [
            "threadId": threadID,
            "hostId": "local",
            "cwd": cwd,
            "prompt": prompt,
        ])
    }

    func createTaskLink(
        threadID: String,
        title: String,
        projectName: String,
        targetAlias: String
    ) async throws -> FeishuTaskLinkSnapshot {
        try await decode(method: "feishu/taskLink/create", params: [
            "threadId": threadID,
            "title": title,
            "projectName": projectName,
            "targetAlias": targetAlias,
        ])
    }

    func confirmTaskCard(_ authorization: FeishuTaskCardAuthorization) async throws {
        _ = try await requestData(method: "feishu/operation/confirm", params: ["operationId": authorization.operation.id, "challenge": authorization.challenge])
    }

    func cancelTaskCard(_ authorization: FeishuTaskCardAuthorization, threadID: String) async throws {
        _ = try await requestData(method: "feishu/operation/cancel", params: ["operationId": authorization.operation.id])
        _ = try await releaseTaskLink(threadID: threadID)
    }

    func releaseTaskLink(threadID: String) async throws -> FeishuTaskLinkSnapshot {
        try await decode(method: "feishu/taskLink/release", params: [
            "threadId": threadID,
        ])
    }

    func interruptTaskLink(threadID: String) async throws -> FeishuTaskLinkSnapshot {
        try await decode(method: "feishu/taskLink/interrupt", params: [
            "threadId": threadID,
        ])
    }



    func stop() async {
        guard !stopping else { return }
        stopping = true
        initialization?.cancel()
        if let connection {
            _ = try? await connection.request(method: "shutdown", params: [:], timeout: 8)
        }
        stopProcess()
    }

    func pollUserApproval(interactive: Bool) async throws -> UserApprovalPoll {
        let data = try await requestData(method: "userApproval/poll", params: ["interactive": interactive], timeout: 2)
        return try UserApprovalPoll.decode(data)
    }

    func decideUserApproval(id: String, approve: Bool) async throws -> Bool {
        let data = try await requestData(method: "userApproval/decide", params: ["id": id, "approve": approve], timeout: 2)
        return try UserApprovalDecision.decode(data).accepted
    }

    private func decode<T: Decodable>(method: String, params: [String: Any]) async throws -> T {
        let data = try await requestData(method: method, params: params)
        return try Self.decoder().decode(T.self, from: data)
    }

    private func requestData(method: String, params: [String: Any], timeout: TimeInterval? = nil) async throws -> Data {
        guard let process, process.isRunning, let connection, !stopping else {
            throw CoreServiceError.processStopped
        }
        let duration = timeout ?? 45
        return try await connection.request(method: method, params: params, timeout: duration)
    }

    private func stopProcess() {
        connection?.close()
        connection = nil
        if let process, process.isRunning {
            process.terminate()
        }
        process = nil
    }

    private static func locateExecutable() -> URL? {
        let fileManager = FileManager.default
        if let explicit = ProcessInfo.processInfo.environment["KSF_ASSISTANT_CORE_PATH"],
           fileManager.isExecutableFile(atPath: explicit) {
            return URL(fileURLWithPath: explicit)
        }
#if arch(arm64)
        let platformDirectory = "darwin-arm64"
#else
        let platformDirectory = "darwin-x64"
#endif
        var candidates: [URL] = []
        if let resources = Bundle.main.resourceURL {
            candidates.append(resources.appendingPathComponent("core/\(platformDirectory)/ksf-assistant-core"))
        }
        let executable = URL(fileURLWithPath: CommandLine.arguments[0]).standardizedFileURL
        candidates.append(
            executable.deletingLastPathComponent().deletingLastPathComponent()
                .appendingPathComponent("dist/core/\(platformDirectory)/ksf-assistant-core")
        )
        candidates.append(
            URL(fileURLWithPath: FileManager.default.currentDirectoryPath, isDirectory: true)
                .appendingPathComponent("dist/core/\(platformDirectory)/ksf-assistant-core")
        )
        return candidates.first { fileManager.isExecutableFile(atPath: $0.path) }
    }

    func feishuConfigurationRequest(method: String, payload: Data) async throws -> Data {
        guard ["feishu/configuration/read", "feishu/configuration/action", "feishu/configuration/result"].contains(method),
              let params = try JSONSerialization.jsonObject(with: payload) as? [String: Any]
        else { throw CoreServiceError.invalidResponse }
        return try await requestData(method: method, params: params, timeout: 125)
    }

    func toolchainStatus() async throws -> ToolchainStatus {
        try await decode(method: "toolchain/status", params: [:])
    }

    func installToolchain() async throws -> ToolchainStatus {
        try await decode(method: "toolchain/install", params: ["confirm": true])
    }



    private static func locateFeishuRuntime() -> (bridge: URL, larkCLI: URL)? {
        let fileManager = FileManager.default
#if arch(arm64)
        let platformDirectory = "darwin-arm64"
#else
        let platformDirectory = "darwin-x64"
#endif
        var roots: [(URL, URL)] = []
        if let resources = Bundle.main.resourceURL {
            roots.append((
                resources.appendingPathComponent("runtime/feishu-bridge/\(platformDirectory)/ksf-assistant-feishu-bridge"),
                resources.appendingPathComponent("runtime/lark-cli/\(platformDirectory)/lark-cli")
            ))
        }
        let current = URL(fileURLWithPath: fileManager.currentDirectoryPath, isDirectory: true)
        roots.append((
            current.appendingPathComponent("dist/runtime/feishu-bridge/\(platformDirectory)/ksf-assistant-feishu-bridge"),
            current.appendingPathComponent("dist/runtime/lark-cli/\(platformDirectory)/lark-cli")
        ))
        return roots.compactMap { bridge, larkCLI -> (URL, URL)? in
            guard fileManager.isExecutableFile(atPath: bridge.path),
                  fileManager.isExecutableFile(atPath: larkCLI.path) else { return nil }
            return (bridge, larkCLI)
        }.first
    }

    private static func decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let value = try decoder.singleValueContainer().decode(String.self)
            let fractional = ISO8601DateFormatter()
            fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = fractional.date(from: value) { return date }
            if let date = ISO8601DateFormatter().date(from: value) { return date }
            throw DecodingError.dataCorruptedError(
                in: try decoder.singleValueContainer(),
                debugDescription: "Invalid ISO-8601 date: \(value)"
            )
        }
        return decoder
    }

    private static func pricingSelectionObject(_ selection: PricingSelection) -> [String: Any] {
        [
            "planId": selection.planId,
            "customPlans": selection.customPlans.map(pricingPlanObject),
        ]
    }

    private static func pricingPlanObject(_ plan: PricingPlan) -> [String: Any] {
        var value: [String: Any] = [
            "id": plan.id,
            "provider": plan.provider,
            "model": plan.model,
            "displayName": plan.displayName,
            "regularInputMicroUsdPerMillion": plan.regularInputMicroUsdPerMillion,
            "cachedInputMicroUsdPerMillion": plan.cachedInputMicroUsdPerMillion,
            "outputMicroUsdPerMillion": plan.outputMicroUsdPerMillion,
            "builtIn": plan.builtIn,
        ]
        if let variant = plan.variant { value["variant"] = variant }
        return value
    }
}

private struct DashboardDTO: Decodable {
    let coreVersion: String
    let usage: UsageDTO
    let activity: ActivityDTO
    let projects: ProjectsDTO
    let feishu: FeishuDTO

    var value: CoreServiceDashboard {
        CoreServiceDashboard(
            coreVersion: coreVersion,
            usage: usage.snapshot,
            usageStatus: usage.status,
            rateError: usage.rateError,
            tokenError: usage.tokenError,
            activity: activity.snapshot,
            projects: projects.snapshot,
            feishu: feishu.snapshot,
            feishuLinks: feishu.links ?? []
        )
    }
}

private struct UsageDTO: Decodable {
    let buckets: [RateLimitBucket]
    let tokenSummary: TokenUsageSummary?
    let dailyUsageBuckets: [DailyUsageBucket]
    let localDailyUsage: DailyUsageBucket?
    let localPreviousDailyUsage: DailyUsageBucket?
    let localDailyCost: TokenCostEstimate?
    let rateUpdatedAt: Date?
    let tokenUpdatedAt: Date?
    let localTokenUpdatedAt: Date?
    let status: String
    let rateError: String?
    let tokenError: String?

    var snapshot: UsageSnapshot {
        UsageSnapshot(
            buckets: buckets,
            tokenSummary: tokenSummary,
            dailyUsageBuckets: dailyUsageBuckets,
            localDailyUsage: localDailyUsage,
            localPreviousDailyUsage: localPreviousDailyUsage,
            localDailyCost: localDailyCost,
            rateUpdatedAt: rateUpdatedAt,
            tokenUpdatedAt: tokenUpdatedAt,
            localTokenUpdatedAt: localTokenUpdatedAt
        )
    }
}

private struct ActivityDTO: Decodable {
    let runningCount: Int
    let waitingCount: Int
    let observedAt: Date
    let availability: String
    let observations: [ObservationDTO]

    var snapshot: TaskActivitySnapshot {
        TaskActivitySnapshot(
            runningCount: runningCount,
            waitingCount: waitingCount,
            observedAt: observedAt,
            availability: TaskActivityAvailability(rawValue: availability) ?? .offline,
            observations: observations.map(\.value)
        )
    }
}

private struct ObservationDTO: Decodable {
    let id: String
    let hostId: String
    let agentNickname: String?
    let sourceKind: String?
    let runtimeStatus: String
    let activeFlags: [String]
    let pendingRequestMethods: [String]
    let hasPendingPlanImplementation: Bool

    var value: CodexTaskObservation {
        CodexTaskObservation(
            id: id,
            hostID: hostId,
            agentNickname: agentNickname,
            sourceKind: sourceKind,
            runtimeStatus: CodexTaskRuntimeStatus(rawValue: runtimeStatus) ?? .systemError,
            activeFlags: Set(activeFlags.compactMap(CodexTaskActiveFlag.init(rawValue:))),
            pendingRequestMethods: Set(pendingRequestMethods),
            hasPendingPlanImplementation: hasPendingPlanImplementation
        )
    }
}

private struct ProjectsDTO: Decodable {
    let availability: String
    let projects: [ProjectItemDTO]
    let catalog: [KSFProject]
    let observedAt: Date
    let message: String?

    var snapshot: ProjectDashboardSnapshot {
        ProjectDashboardSnapshot(
            availability: KSFProjectAvailability(rawValue: availability) ?? .unavailable,
            projects: projects.map(\.value),
            catalog: catalog,
            observedAt: observedAt,
            message: message
        )
    }
}

private struct ProjectItemDTO: Decodable {
    let id: String
    let kind: String
    let project: KSFProject?
    let isPinned: Bool
    let tasks: [ProjectTaskDTO]
    let latestActivity: Date?
    let usage: ProjectUsageSummary?
    let launchAction: LaunchActionDTO?
    let preferredEngineeringId: String?

    var value: ProjectDashboardItem {
        ProjectDashboardItem(
            id: id,
            project: project,
            kind: kind == "unassigned" ? .unassigned : (project == nil ? .unavailablePinned : .project),
            isPinned: isPinned,
            tasks: tasks.map(\.value),
            latestActivity: latestActivity,
            usage: usage,
            launchAction: launchAction?.value(projectID: id),
            preferredEngineeringID: preferredEngineeringId
        )
    }
}

private struct ProjectTaskDTO: Decodable {
    let threadId: String
    let hostId: String
    let name: String?
    let classification: String
    let waitingReason: String?
    let route: KSFRouteSummary?
    let taskRuntime: ProjectTaskRuntime?
    let createdAt: Date
    let projectId: String

    var value: ProjectTaskItem {
        ProjectTaskItem(
            threadID: threadId,
            hostID: hostId,
            name: name,
            classification: classificationValue,
            waitingReason: waitingReasonValue,
            route: route,
            taskRuntime: taskRuntime,
            createdAt: createdAt,
            projectID: projectId
        )
    }

    private var classificationValue: TaskActivityClassifier.Classification {
        switch classification {
        case "running": return .running
        case "waiting": return .waiting
        case "completed": return .completed
        default: return .ignored
        }
    }

    private var waitingReasonValue: ProjectTaskWaitingReason? {
        switch waitingReason {
        case "approval": return .approval
        case "planConfirmation": return .planConfirmation
        case "userInput": return .userInput
        case "actionRequired": return .actionRequired
        default: return nil
        }
    }
}

private struct LaunchActionDTO: Decodable {
    let title: String
    let scriptPath: String
    let workingDirectory: String

    func value(projectID: String) -> ProjectLaunchAction {
        ProjectLaunchAction(
            projectID: projectID,
            title: title,
            scriptPath: scriptPath,
            workingDirectory: workingDirectory
        )
    }
}

private struct FeishuDTO: Decodable {
    let availability: String
    let message: String?
    let profile: String?
    let profileValid: Bool?
    let inboundConnection: Bool?
    let processRunning: Bool?
    let processState: String?
    let configured: Bool?
    let processPid: Int?
    let restartCount: Int?
    let lastError: String?
    let targetAliases: [String]?
    let taskLinkProtocolVersion: Int
    let taskLinkReady: Bool
    let readinessBlockers: [String]?
    let links: [FeishuTaskLinkSnapshot]?

    var snapshot: FeishuServiceSnapshot {
        FeishuServiceSnapshot(
            availability: availabilityValue,
            targetAliases: targetAliases ?? [],
            taskLinkProtocolVersion: taskLinkProtocolVersion,
            taskLinkReady: taskLinkReady,
            readinessBlockers: readinessBlockers ?? [],
            profile: profile ?? "",
            profileValid: profileValid ?? false,
            inboundConnection: inboundConnection ?? false,
            processRunning: processRunning ?? false,
            processState: processState ?? "stopped",
            configured: configured ?? false,
            processPID: processPid,
            restartCount: restartCount ?? 0,
            lastError: lastError
        )
    }

    private var availabilityValue: FeishuServiceAvailability {
        switch availability {
        case "ready": return .ready
        case "stopped": return .stopped
        case "dryRun": return .dryRun
        case "notConfigured": return .notConfigured
        default: return .unavailable(message ?? "飞书服务暂不可用。")
        }
    }
}
