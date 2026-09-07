import Foundation

public struct CoreServiceFeishuAuth: Decodable {
    public let schemaVersion: Int
    public let status: String
    public let identity: String
    public let profile: String
    public let identityValid: Bool
    public let profileValid: Bool
    public let grantedScopeCount: Int
    public let missingCapabilities: [String]?
    public let verificationUrl: String?
    public let userCode: String?
    public let qrDataURL: String?

    public var isAuthorized: Bool { schemaVersion == 1 && status == "authorized" && identity == "user" && profile == "default" && identityValid && profileValid }
    public var isPending: Bool { schemaVersion == 1 && status == "pending" }
    public var statusText: String {
        guard schemaVersion == 1 else { return "状态不兼容" }
        if isAuthorized { return "已授权" }
        if isPending { return "等待飞书确认" }
        return status == "failed" ? "授权状态待复核" : "未验证用户授权"
    }
}

public struct FeishuSetupState: Codable, Equatable {
    public let version: Int
    public let stage: String
    public let mode: String?
    public let verificationURL: String?
    public let userCode: String?
    public let lastError: String?
    public let readyToActivate: Bool?

    public static let unknown = FeishuSetupState(
        version: 1, stage: "unknown", mode: nil,
        verificationURL: nil, userCode: nil, lastError: nil, readyToActivate: nil
    )
}

public struct FeishuSettingsOverview: Decodable {
    public struct Health: Decodable { public let core: String; public let bridge: String; public let inbound: String; public let detail: String? }
    public struct Permissions: Decodable {
        public let application: String
        public let user: String
        public let missing: [String]

        private enum CodingKeys: String, CodingKey { case application, user, missing }

        public init(from decoder: Decoder) throws {
            let values = try decoder.container(keyedBy: CodingKeys.self)
            application = try values.decode(String.self, forKey: .application)
            user = try values.decode(String.self, forKey: .user)
            missing = try values.decodeIfPresent([String].self, forKey: .missing) ?? []
        }
    }
    public struct Feature: Decodable, Identifiable { public let id: String; public let title: String; public let description: String; public let state: String; public let writable: Bool }
    public let state: String
    public let summary: String
    public let profile: String
    public let health: Health
    public let permissions: Permissions
    public let features: [Feature]
    public let targets: [String]
}


public struct FeishuConfigurationSnapshot: Decodable {
    public struct Summary: Decodable {
        public let state: String
        public let title: String
        public let detail: String
        public let tone: String
    }
    public struct Fact: Decodable, Identifiable {
        public let id: String
        public let title: String
        public let state: String
        public let value: String
        public let source: String
        public let checkedAt: String?
        public let stale: Bool

        public var evidenceHelp: String {
            "来源：\(source)\n检查时间：\(checkedAt.flatMap { $0.isEmpty ? nil : $0 } ?? "未知")"
        }
    }
    public struct Action: Decodable, Identifiable {
        public let id: String
        public let title: String
        public let enabled: Bool
        public let reason: String?
        public let confirmation: String?

        public var requiresConfirmation: Bool {
            !["finish_app", "finish_auth"].contains(id)
                || !(confirmation?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "").isEmpty
        }

        public func confirmationText(targetAlias: String? = nil, mode: String? = nil, featureTitle: String? = nil, appID: String? = nil) -> String? {
            guard let confirmation = confirmation?.trimmingCharacters(in: .whitespacesAndNewlines), !confirmation.isEmpty else { return nil }
            switch id {
            case "test_message":
                guard let targetAlias, !targetAlias.isEmpty else { return nil }
                return "\(confirmation)\n身份：机器人（bot）\n目标：\(targetAlias)\n正文：【KSFAssistant 接入验收】这是一条由本人确认发送的连接测试消息，无需回复。\n仅验证本次发送，不代表整体配置已就绪。"
            case "connect_app":
                guard let appID, !appID.isEmpty else { return nil }
                return "\(confirmation)\n待接入 App ID：\(appID)"
            default: return confirmation
            }
        }
    }
    public struct Flow: Decodable, Identifiable {
        public let id: String
        public let kind: String
        public let state: String
        public let expiresAt: String?
        public let verificationURL: String?
        public let userCode: String?
        public let qrDataURL: String?
    }
    public struct Issue: Decodable {
        public let component: String
        public let code: String
        public let message: String
    }
    public struct Connection: Decodable {
        public let availability: String?
        public let processRunning: Bool?
        public let processState: String?
        public let inboundConnection: Bool?
        public let configured: Bool?
        public let targetAliases: [String]?
    }
    public struct Diagnostics: Decodable {
        public struct Operation: Decodable, Identifiable {
            public let title: String?
            public let statusText: String?
            public let stageText: String?
            public let code: String?
            public let requestId: String
            public var id: String { requestId }
            public let action: String
            public let outcome: String
            public let stage: String
            public let message: String
            public let updatedAt: String
        }
        public let serviceVersion: String
        public let cliVersion: String
        public let cliState: String
        public let permissionRevision: String
        public let missingUserScopes: [String]?
        public let missingApplicationScopes: [String]?
        public let selfTarget: String?
        public let recentOperations: [Operation]?
        public var versionSummary: String {
            "飞书服务 \(serviceVersion.isEmpty ? "未知" : "v" + serviceVersion) · lark-cli \(cliVersion.isEmpty ? "未知" : "v" + cliVersion)"
        }
    }
    public let diagnostics: Diagnostics?
    public let schemaVersion: Int
    public let epoch: String
    public let revision: UInt64
    public let contextRevision: String
    public let observedAt: String
    public let refreshing: Bool
    public let summary: Summary
    public let facts: [Fact]
    public let actions: [Action]
    public let setup: FeishuSetupState
    public let auth: CoreServiceFeishuAuth?
    public let overview: FeishuSettingsOverview?
    public let connection: Connection
    public let flow: Flow?
    public let issues: [Issue]?

    public func action(_ id: String) -> Action? { actions.first { $0.id == id } }
    public func fact(_ id: String) -> Fact? { facts.first { $0.id == id } }
    public var context: FeishuConfigurationContext {
        FeishuConfigurationContext(epoch: epoch, revision: revision, contextRevision: contextRevision)
    }
}

public struct FeishuConfigurationContext: Equatable {
    public let epoch: String
    public let revision: UInt64
    public let contextRevision: String
}

public struct FeishuConfigurationActionRequest: Encodable {
    public let action: String
    public let requestId: String
    public let epoch: String
    public let revision: UInt64
    public let contextRevision: String
    public let confirm: Bool
    public let appId: String?
    public let appSecret: String?
    public let targetAlias: String?
    public let feature: String?
    public let mode: String?
    public let flowId: String?
}

public struct FeishuConfigurationActionResult: Decodable {
    public let outcome: String
    public let snapshot: FeishuConfigurationSnapshot
    public let message: String?
}

public struct FeishuConfigurationState {
    public enum Phase { case loading, current, unknown }
    public var phase: Phase = .loading
    public var snapshot: FeishuConfigurationSnapshot?
    public var reading = false
    public var acting = false
    public var message: String?
    public var outcome: String?
    public var lastAction: String?
    public var refreshError: String?
    public init() {}

    public func showsConnectionIndicator(transportReady: Bool, taskLinkReady: Bool) -> Bool {
        transportReady && taskLinkReady && snapshot?.auth?.isAuthorized != false
    }

    public var summaryTitle: String {
        switch phase {
        case .loading: return "正在读取飞书配置"
        case .unknown: return "配置状态未知"
        case .current: return snapshot?.summary.title ?? "配置状态未知"
        }
    }

    public var flow: FeishuConfigurationSnapshot.Flow? {
        guard let flow = actionFlow, flow.state == "pending" else { return nil }
        return flow
    }

    public var actionFlow: FeishuConfigurationSnapshot.Flow? {
        guard phase == .current, let flow = snapshot?.flow, !flow.id.isEmpty else { return nil }
        if flow.state == "completed" { return flow }
        guard flow.state == "pending" else { return nil }
        if let expiry = flow.expiresAt, !expiry.isEmpty {
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            let date = formatter.date(from: expiry) ?? ISO8601DateFormatter().date(from: expiry)
            guard let date, date > Date() else { return nil }
        }
        return flow
    }

    public func allows(_ id: String) -> Bool {
        phase == .current && !acting && refreshError == nil && snapshot?.action(id)?.enabled == true
    }

    public var priorityAction: String? {
        guard phase == .current,
              (snapshot?.action("start_auth")?.enabled == true || (snapshot?.fact("authorizedUser")?.state == "missing" && snapshot?.fact("application")?.state == "present" && snapshot?.flow == nil)) else { return nil }
        return "start_auth"
    }

    public var pendingSetupActions: [FeishuConfigurationSnapshot.Action] {
        snapshot?.actions.filter { ["bind_operator"].contains($0.id) && $0.enabled } ?? []
    }
}

private extension FeishuConfigurationState {
    // Core revision excludes observation timestamps. Retain current wire context internally,
    // but do not repaint the native page for timestamp-only background observations.
    var presentationKey: String {
        let operations = snapshot?.diagnostics?.recentOperations?.map {
            "\($0.id):\($0.outcome):\($0.stage):\($0.message):\($0.updatedAt)"
        }.joined(separator: "|") ?? ""
        var fields: [String] = [String(describing: phase), snapshot?.epoch ?? ""]
        fields.append(String(snapshot?.revision ?? 0))
        fields.append(snapshot?.contextRevision ?? "")
        fields += [String(reading), String(acting), message ?? "", outcome ?? ""]
        fields += [lastAction ?? "", refreshError ?? "", operations]
        return fields.joined(separator: "\u{1f}")
    }
}

@MainActor
public final class FeishuConfigurationSession {
    public typealias Transport = (String, Data) async throws -> Data
    public private(set) var state = FeishuConfigurationState() {
        didSet {
            if state.presentationKey != oldValue.presentationKey { onChange?(state) }
        }
    }
    public var onChange: ((FeishuConfigurationState) -> Void)?
    private let transport: Transport
    private let canSubmit: () -> Bool
    private var generation: UInt64 = 0
    private var localRequestRevision: UInt64 = 0
    private var pollingTask: Task<Void, Never>?
    private var closed = false

    public init(canSubmit: @escaping () -> Bool = { true }, transport: @escaping Transport) {
        self.transport = transport
        self.canSubmit = canSubmit
    }

    public func startPolling(sleep: @escaping () async throws -> Void = { try await Task.sleep(nanoseconds: 2_500_000_000) }) {
        guard !closed, pollingTask == nil else { return }
        pollingTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.read()
                do { try await sleep() } catch { return }
            }
        }
    }

    public func stopPolling() {
        pollingTask?.cancel()
        pollingTask = nil
        if !state.acting {
            localRequestRevision &+= 1
            state.reading = false
        }
    }

    public func invalidate() {
        stopPolling()
        generation &+= 1
        localRequestRevision &+= 1
        state = FeishuConfigurationState()
    }

    public func shutdown() {
        closed = true
        invalidate()
        state.phase = .unknown
    }

    public func restore(_ snapshot: FeishuConfigurationSnapshot) throws {
        try validate(snapshot)
        generation &+= 1
        localRequestRevision &+= 1
        state = FeishuConfigurationState()
        apply(snapshot)
    }

    public func read(refresh: Bool = false) async {
        guard !closed, !state.acting, !Task.isCancelled else { return }
        localRequestRevision &+= 1
        let requestRevision = localRequestRevision
        let requestGeneration = generation
        if refresh || state.snapshot == nil { state.reading = true }
        do {
            let data = try await transport("feishu/configuration/read", JSONEncoder().encode(["refresh": refresh]))
            let snapshot = try JSONDecoder().decode(FeishuConfigurationSnapshot.self, from: data)
            guard !Task.isCancelled, requestRevision == localRequestRevision, requestGeneration == generation else { return }
            try validate(snapshot)
            apply(snapshot)
        } catch {
            guard !Task.isCancelled, requestRevision == localRequestRevision, requestGeneration == generation else { return }
            if state.snapshot == nil { state.phase = .unknown }
            state.refreshError = "状态更新失败，请刷新重试。"
        }
        state.reading = state.reading && state.refreshError == nil && state.snapshot?.refreshing == true
    }

    public func perform(
        _ action: String, confirm: Bool = false, appID: String? = nil, appSecret: String? = nil,
        targetAlias: String? = nil, feature: String? = nil, mode: String? = nil, flowID: String? = nil,
        expectedContext: FeishuConfigurationContext? = nil
    ) async {
        guard !closed, state.phase == .current, !state.acting, let snapshot = state.snapshot,
              let affordance = snapshot.action(action), affordance.enabled else { return }
        guard canSubmit() else {
            state.message = "当前无法提交配置操作，请恢复本机交互并关闭其他批准窗口后重新确认。"
            return
        }
        if let expectedContext,
           snapshot.epoch != expectedContext.epoch || snapshot.contextRevision != expectedContext.contextRevision {
            state.message = "配置已更新，请重新确认本次操作。"
            return
        }
        if affordance.requiresConfirmation {
            let featureTitle = snapshot.overview?.features.first { $0.id == feature }?.title
            guard affordance.confirmationText(targetAlias: targetAlias, mode: mode, featureTitle: featureTitle, appID: appID) != nil else {
                state.message = "缺少有效的操作确认内容，请检查配置后重试。"
                return
            }
            guard confirm else { return }
        }
        if action == "test_message" {
            let targets = snapshot.overview?.targets ?? snapshot.connection.targetAliases ?? []
            guard let targetAlias, !targetAlias.isEmpty, targets.contains(targetAlias) else { return }
        }
        if ["finish_app", "finish_auth", "cancel_flow"].contains(action) {
            guard let flowID, !flowID.isEmpty, flowID == state.actionFlow?.id else { return }
            if action == "cancel_flow", state.flow == nil { return }
        }
        localRequestRevision &+= 1
        let requestRevision = localRequestRevision
        let requestGeneration = generation
        let request = FeishuConfigurationActionRequest(
            action: action, requestId: UUID().uuidString, epoch: snapshot.epoch, revision: snapshot.revision,
            contextRevision: snapshot.contextRevision, confirm: confirm, appId: appID, appSecret: appSecret,
            targetAlias: targetAlias, feature: feature, mode: mode, flowId: flowID
        )
        state.lastAction = action
        state.acting = true
        state.reading = false
        state.message = nil
        state.outcome = nil
        do {
            let data = try await transport("feishu/configuration/action", JSONEncoder().encode(request))
            let result = try JSONDecoder().decode(FeishuConfigurationActionResult.self, from: data)
            guard requestRevision == localRequestRevision, requestGeneration == generation else { return }
            try validate(result.snapshot)
            guard ["completed", "pending", "failed", "unknown"].contains(result.outcome) else { throw ConfigurationError.incompatible }
            apply(result.snapshot)
            state.outcome = result.outcome
            state.message = result.message
        } catch {
            guard requestRevision == localRequestRevision, requestGeneration == generation else { return }
            // Query the original request; never repeat the mutation after a transport error.
            do {
                let data = try await transport("feishu/configuration/result", JSONEncoder().encode(["requestId": request.requestId]))
                let result = try JSONDecoder().decode(FeishuConfigurationActionResult.self, from: data)
                guard requestRevision == localRequestRevision, requestGeneration == generation else { return }
                try validate(result.snapshot)
                apply(result.snapshot)
                state.outcome = result.outcome
                state.message = "\(affordance.title)：\(result.message ?? "结果待核实")"
            } catch {
                state.phase = .unknown
                state.outcome = "unknown"
                state.message = "\(affordance.title)：暂时无法查询结果；不会重复执行，请刷新查看诊断。"
            }
        }
        state.acting = false
    }

    private func validate(_ snapshot: FeishuConfigurationSnapshot) throws {
        guard snapshot.schemaVersion == 1, !snapshot.epoch.isEmpty,
              !snapshot.summary.title.isEmpty else { throw ConfigurationError.incompatible }
        if let current = state.snapshot, current.epoch == snapshot.epoch, snapshot.revision < current.revision {
            throw ConfigurationError.stale
        }
    }

    private func apply(_ snapshot: FeishuConfigurationSnapshot) {
        if let current = state.snapshot, current.epoch != snapshot.epoch { generation &+= 1 }
        if state.outcome == "pending", state.snapshot?.flow?.kind == "user",
           snapshot.flow == nil, snapshot.auth?.identityValid == true {
            state.outcome = "completed"
            state.message = nil
        }
        var next = state
        next.snapshot = snapshot
        next.phase = .current
        next.refreshError = nil
        state = next
    }

    private enum ConfigurationError: Error { case incompatible, stale }
}
