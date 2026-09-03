import Darwin
import CodexUsageCore
import Foundation

enum SharedCoreError: Error, LocalizedError {
    case executableMissing
    case processStopped
    case pipeReadFailed(String)
    case invalidResponse
    case remote(String)

    var errorDescription: String? {
        switch self {
        case .executableMissing:
            return "未找到 Codex Usage Bar 共享核心。"
        case .processStopped:
            return "Codex Usage Bar 共享核心已停止。"
        case let .pipeReadFailed(message):
            return "读取 Codex Usage Bar 共享核心失败：\(message)"
        case .invalidResponse:
            return "共享核心返回了无法识别的数据。"
        case let .remote(message):
            return message
        }
    }
}

struct SharedCoreCreatedTask: Decodable {
    let threadId: String
    let name: String
    let prompt: String
    let submission: String
}

struct SharedCoreFeishuAuth: Decodable {
    let status: String
    let flow: String
    let userCode: String?
    let qrDataURL: String
}

struct SharedCoreFeishuPermissions: Decodable {
    struct PermissionSet: Decodable {
        let missing: [String]
    }
    struct UserIdentity: Decodable {
        let ready: Bool
        let application: PermissionSet?
        let oauth: PermissionSet
    }
    struct Identities: Decodable {
        let user: UserIdentity
    }
    struct Permissions: Decodable {
        let verified: Bool
        let identities: Identities
    }
    let permissions: Permissions
}

struct SharedCoreDashboard {
    let coreVersion: String
    let usage: UsageSnapshot
    let usageStatus: String
    let rateError: String?
    let tokenError: String?
    let activity: TaskActivitySnapshot
    let projects: ProjectDashboardSnapshot
    let feishu: FeishuBridgeSnapshot
    let feishuLinks: [FeishuTaskLinkSnapshot]
}

enum SharedCorePipeIO {
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
            throw SharedCoreError.pipeReadFailed(message)
        }
    }
}

actor SharedCoreProcessClient {
    private var process: Process?
    private var input: FileHandle?
    private var output: FileHandle?
    private var readBuffer = Data()
    private var sequence = 0

    func start() throws {
        guard process == nil else { return }
        guard let executable = Self.locateExecutable() else {
            throw SharedCoreError.executableMissing
        }
        let process = Process()
        let inputPipe = Pipe()
        let outputPipe = Pipe()
        let errorPipe = Pipe()
        process.executableURL = executable
        process.currentDirectoryURL = executable.deletingLastPathComponent()
        var environment = ProcessInfo.processInfo.environment
        if let runtime = Self.locateFeishuRuntime() {
            environment["CODEX_USAGE_BAR_MANAGED"] = "1"
            environment["CODEX_USAGE_BAR_FEISHU_SERVICE_ROOT"] = runtime.service.path
            environment["FEISHU_BRIDGE_DATA_DIR"] = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent(".config/feishu-bridge", isDirectory: true).path
            if let node = runtime.node {
                environment["CODEX_USAGE_BAR_NODE"] = node.path
            }
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
        input = inputPipe.fileHandleForWriting
        output = outputPipe.fileHandleForReading
        do {
            _ = try requestData(method: "initialize", params: [
                "clientInfo": [
                    "name": "codex_usage_bar_macos",
                    "title": "Codex Usage Bar for macOS",
                    "version": "0.9.0",
                ],
            ])
        } catch {
            stopProcess()
            throw error
        }
    }

    func dashboard(
        ksfRoot: String,
        pinnedProjectIDs: Set<String>,
        pricingSelection: PricingSelection
    ) throws -> SharedCoreDashboard {
        let data = try requestData(method: "dashboard/read", params: [
            "ksfRoot": ksfRoot,
            "pinnedProjectIds": Array(pinnedProjectIDs).sorted(),
            "pricingSelection": Self.pricingSelectionObject(pricingSelection),
        ])
        let dto = try Self.decoder().decode(DashboardDTO.self, from: data)
        return dto.value
    }

    func tokenHistory(dayCount: Int = 30) throws -> [DailyUsageBucket] {
        try decode(method: "token/history/read", params: ["dayCount": dayCount])
    }

    func tokenHistoryComparison(
        dayCount: Int = 30,
        pricingSelection: PricingSelection,
        repriceOnly: Bool = false
    ) throws -> TokenHistoryComparison {
        try decode(method: "token/history/compare", params: [
            "dayCount": dayCount,
            "pricingSelection": Self.pricingSelectionObject(pricingSelection),
            "repriceOnly": repriceOnly,
        ])
    }

    func pricingCatalog(customPlans: [PricingPlan]) throws -> PricingCatalog {
        try decode(method: "pricing/catalog/read", params: [
            "customPlans": customPlans.map(Self.pricingPlanObject),
        ])
    }

    func createTask(projectID: String, ksfRoot: String, purpose: String) throws -> SharedCoreCreatedTask {
        try decode(method: "task/create", params: [
            "projectId": projectID,
            "ksfRoot": ksfRoot,
            "purpose": purpose,
        ])
    }

    func submitTask(threadID: String, cwd: String, prompt: String) throws {
        _ = try requestData(method: "task/submit", params: [
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
    ) throws -> FeishuTaskLinkSnapshot {
        try decode(method: "feishu/taskLink/create", params: [
            "threadId": threadID,
            "title": title,
            "projectName": projectName,
            "targetAlias": targetAlias,
        ])
    }

    func releaseTaskLink(threadID: String) throws -> FeishuTaskLinkSnapshot {
        try decode(method: "feishu/taskLink/release", params: [
            "threadId": threadID,
        ])
    }

    func interruptTaskLink(threadID: String) throws -> FeishuTaskLinkSnapshot {
        try decode(method: "feishu/taskLink/interrupt", params: [
            "threadId": threadID,
        ])
    }

    func sendFeishuTest(targetAlias: String) throws {
        _ = try requestData(method: "feishu/test", params: [
            "targetAlias": targetAlias,
        ])
    }

    func stop() {
        let activeProcess = process
        let watchdog = DispatchWorkItem {
            if activeProcess?.isRunning == true {
                activeProcess?.terminate()
            }
        }
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 8, execute: watchdog)
        defer { watchdog.cancel() }
        if process != nil {
            _ = try? requestData(method: "shutdown", params: [:])
        }
        stopProcess()
    }

    private func decode<T: Decodable>(method: String, params: [String: Any]) throws -> T {
        try Self.decoder().decode(T.self, from: requestData(method: method, params: params))
    }

    private func requestData(method: String, params: [String: Any]) throws -> Data {
        guard let process, process.isRunning, let input, let output else {
            throw SharedCoreError.processStopped
        }
        sequence += 1
        let id = sequence
        let payload = try JSONSerialization.data(withJSONObject: [
            "jsonrpc": "2.0",
            "id": id,
            "method": method,
            "params": params,
        ])
        input.write(payload + Data([0x0a]))

        while true {
            if let newline = readBuffer.firstIndex(of: 0x0a) {
                let line = readBuffer[..<newline]
                readBuffer.removeSubrange(...newline)
                guard let object = try JSONSerialization.jsonObject(with: Data(line)) as? [String: Any],
                      (object["id"] as? NSNumber)?.intValue == id else {
                    continue
                }
                if let error = object["error"] as? [String: Any] {
                    throw SharedCoreError.remote(error["message"] as? String ?? "共享核心调用失败。")
                }
                guard let result = object["result"] else {
                    throw SharedCoreError.invalidResponse
                }
                return try JSONSerialization.data(withJSONObject: result)
            }
            let data = try SharedCorePipeIO.readChunk(from: output)
            guard !data.isEmpty else {
                throw SharedCoreError.processStopped
            }
            readBuffer.append(data)
        }
    }

    private func stopProcess() {
        input = nil
        output = nil
        readBuffer.removeAll(keepingCapacity: false)
        if let process, process.isRunning {
            process.terminate()
        }
        process = nil
    }

    private static func locateExecutable() -> URL? {
        let fileManager = FileManager.default
        if let explicit = ProcessInfo.processInfo.environment["CODEX_USAGE_CORE_PATH"],
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
            candidates.append(resources.appendingPathComponent("core/\(platformDirectory)/codex-usage-core"))
        }
        let executable = URL(fileURLWithPath: CommandLine.arguments[0]).standardizedFileURL
        candidates.append(
            executable.deletingLastPathComponent().deletingLastPathComponent()
                .appendingPathComponent("dist/core/\(platformDirectory)/codex-usage-core")
        )
        candidates.append(
            URL(fileURLWithPath: FileManager.default.currentDirectoryPath, isDirectory: true)
                .appendingPathComponent("dist/core/\(platformDirectory)/codex-usage-core")
        )
        return candidates.first { fileManager.isExecutableFile(atPath: $0.path) }
    }

    func setFeishuProfile(_ profile: String) throws {
        _ = try requestData(method: "feishu/profile/set", params: ["profile": profile])
    }

    func controlFeishuService(_ action: String) throws {
        _ = try requestData(method: "feishu/service/control", params: ["action": action])
    }

    func configureFeishu(appID: String, appSecret: String) throws {
        _ = try requestData(method: "feishu/auth/configure", params: [
            "appId": appID,
            "appSecret": appSecret,
        ])
    }

    func startFeishuAuth() throws -> SharedCoreFeishuAuth {
        try decode(method: "feishu/auth/start", params: [:])
    }

    func finishFeishuAuth() throws {
        _ = try requestData(method: "feishu/auth/finish", params: [:])
    }

    func feishuPermissions() throws -> SharedCoreFeishuPermissions {
        try decode(method: "feishu/permissions/read", params: [:])
    }

    private static func locateFeishuRuntime() -> (service: URL, node: URL?)? {
        let fileManager = FileManager.default
#if arch(arm64)
        let platformDirectory = "darwin-arm64"
#else
        let platformDirectory = "darwin-x64"
#endif
        var roots: [(URL, URL?)] = []
        if let resources = Bundle.main.resourceURL {
            roots.append((
                resources.appendingPathComponent("services/feishu-bridge", isDirectory: true),
                resources.appendingPathComponent("runtime/node/\(platformDirectory)/node")
            ))
        }
        let current = URL(fileURLWithPath: fileManager.currentDirectoryPath, isDirectory: true)
        roots.append((current.appendingPathComponent("Services/FeishuBridge", isDirectory: true), nil))
        return roots.compactMap { candidate -> (URL, URL?)? in
            let (service, node) = candidate
            let entry = service.appendingPathComponent("scripts/bridge-client.js")
            guard fileManager.fileExists(atPath: entry.path) else { return nil }
            return (service, node.flatMap { fileManager.isExecutableFile(atPath: $0.path) ? $0 : nil })
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

    var value: SharedCoreDashboard {
        SharedCoreDashboard(
            coreVersion: coreVersion,
            usage: usage.snapshot,
            usageStatus: usage.status,
            rateError: usage.rateError,
            tokenError: usage.tokenError,
            activity: activity.snapshot,
            projects: projects.snapshot,
            feishu: feishu.snapshot,
            feishuLinks: feishu.links
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
    let targetAliases: [String]
    let taskLinkProtocolVersion: Int
    let taskLinkReady: Bool
    let readinessBlockers: [String]
    let links: [FeishuTaskLinkSnapshot]

    var snapshot: FeishuBridgeSnapshot {
        FeishuBridgeSnapshot(
            availability: availabilityValue,
            targetAliases: targetAliases,
            taskLinkProtocolVersion: taskLinkProtocolVersion,
            taskLinkReady: taskLinkReady,
            readinessBlockers: readinessBlockers,
            profile: profile ?? "",
            profileValid: profileValid ?? false,
            inboundConnection: inboundConnection ?? false,
            processRunning: processRunning ?? false
        )
    }

    private var availabilityValue: FeishuBridgeAvailability {
        switch availability {
        case "ready": return .ready
        case "stopped": return .stopped
        case "dryRun": return .dryRun
        case "notConfigured": return .notConfigured
        default: return .unavailable(message ?? "飞书桥暂不可用。")
        }
    }
}
