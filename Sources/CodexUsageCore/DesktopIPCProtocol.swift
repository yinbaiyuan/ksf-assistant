import Foundation

public enum DesktopIPCProtocolError: Error, Equatable, LocalizedError {
    case invalidMessage
    case unsupportedVersion(method: String, received: Int?)
    case incompleteSnapshot

    public var errorDescription: String? {
        switch self {
        case .invalidMessage:
            return "Codex desktop IPC returned an invalid message."
        case let .unsupportedVersion(method, received):
            let version = received.map(String.init) ?? "missing"
            return "Codex desktop IPC method \(method) uses unsupported version \(version)."
        case .incompleteSnapshot:
            return "Codex desktop IPC returned an incomplete task snapshot."
        }
    }
}

public enum DesktopIPCEvent: Equatable, Sendable {
    case initialized(clientID: String)
    case followingChanged(
        sourceClientID: String,
        taskID: String,
        hostID: String,
        following: Bool
    )
    case ownerResponse(requestID: String, ownerClientID: String?)
    case stateSnapshot(sourceClientID: String, observation: CodexTaskObservation)
    case statePatches(
        sourceClientID: String,
        taskID: String,
        hostID: String,
        runtimePatch: DesktopIPCRuntimePatch?,
        requiresSnapshot: Bool
    )
    case clientDiscoveryRequested(requestID: String)
    case ignored
}

public struct DesktopIPCRuntimePatch: Equatable, Sendable {
    public let status: CodexTaskRuntimeStatus?
    public let activeFlags: Set<CodexTaskActiveFlag>?

    public init(
        status: CodexTaskRuntimeStatus? = nil,
        activeFlags: Set<CodexTaskActiveFlag>? = nil
    ) {
        self.status = status
        self.activeFlags = activeFlags
    }
}

public enum DesktopIPCMessageDecoder {
    public static func decode(_ data: Data) throws -> DesktopIPCEvent {
        let envelope: Envelope
        do {
            envelope = try JSONDecoder().decode(Envelope.self, from: data)
        } catch {
            throw DesktopIPCProtocolError.invalidMessage
        }

        if envelope.type == "client-discovery-request", let requestID = envelope.requestID {
            return .clientDiscoveryRequested(requestID: requestID)
        }

        if envelope.type == "response", let requestID = envelope.requestID {
            if let clientID = envelope.result?.clientID ?? envelope.clientID {
                return .initialized(clientID: clientID)
            }
            return .ownerResponse(
                requestID: requestID,
                ownerClientID: envelope.result?.handledByClientID ?? envelope.handledByClientID
            )
        }

        guard envelope.type == "broadcast", let method = envelope.method else {
            return .ignored
        }

        switch method {
        case "thread-stream-following-changed":
            try requireVersion(1, envelope: envelope)
            guard
                let sourceClientID = envelope.sourceClientID,
                let taskID = envelope.params?.conversationID,
                let hostID = envelope.params?.hostID,
                let following = envelope.params?.following
            else {
                throw DesktopIPCProtocolError.invalidMessage
            }
            return .followingChanged(
                sourceClientID: sourceClientID,
                taskID: taskID,
                hostID: hostID,
                following: following
            )

        case "thread-stream-state-changed":
            try requireVersion(11, envelope: envelope)
            guard
                let sourceClientID = envelope.sourceClientID,
                let taskID = envelope.params?.conversationID,
                let hostID = envelope.params?.hostID,
                let change = envelope.params?.change
            else {
                throw DesktopIPCProtocolError.invalidMessage
            }

            switch change.type {
            case "snapshot":
                guard let state = change.conversationState, let runtimeStatus = state.runtimeStatus else {
                    throw DesktopIPCProtocolError.incompleteSnapshot
                }
                let observation = CodexTaskObservation(
                    id: taskID,
                    hostID: hostID,
                    agentNickname: state.agentNickname,
                    sourceKind: state.source?.kind,
                    runtimeStatus: runtimeStatus.type,
                    activeFlags: runtimeStatus.activeFlags,
                    pendingRequestMethods: state.requests?.methods ?? [],
                    hasPendingPlanImplementation: state.hasPendingPlanImplementation
                )
                return .stateSnapshot(sourceClientID: sourceClientID, observation: observation)

            case "patches":
                var status: CodexTaskRuntimeStatus?
                var activeFlags: Set<CodexTaskActiveFlag>?
                var requiresSnapshot = false
                for patch in change.patches {
                    let loweredPath = patch.path.map { $0.lowercased() }
                    if loweredPath.contains("threadruntimestatus") {
                        status = patch.runtimeStatus ?? status
                        activeFlags = patch.activeFlags ?? activeFlags
                        if patch.runtimeStatus == nil && patch.activeFlags == nil {
                            requiresSnapshot = true
                        }
                    }
                    if loweredPath.contains("requests")
                        || loweredPath.contains("planimplementation")
                        || loweredPath.contains("iscompleted")
                    {
                        requiresSnapshot = true
                    }
                }
                return .statePatches(
                    sourceClientID: sourceClientID,
                    taskID: taskID,
                    hostID: hostID,
                    runtimePatch: status == nil && activeFlags == nil
                        ? nil
                        : DesktopIPCRuntimePatch(status: status, activeFlags: activeFlags),
                    requiresSnapshot: requiresSnapshot
                )

            default:
                throw DesktopIPCProtocolError.invalidMessage
            }

        default:
            return .ignored
        }
    }

    private static func requireVersion(_ expected: Int, envelope: Envelope) throws {
        guard envelope.version == expected else {
            throw DesktopIPCProtocolError.unsupportedVersion(
                method: envelope.method ?? "unknown",
                received: envelope.version
            )
        }
    }
}

private struct Envelope: Decodable {
    let type: String
    let method: String?
    let requestID: String?
    let sourceClientID: String?
    let version: Int?
    let params: Params?
    let result: ResultPayload?
    let clientID: String?
    let handledByClientID: String?

    private enum CodingKeys: String, CodingKey {
        case type
        case method
        case requestID = "requestId"
        case requestIDUpper = "requestID"
        case sourceClientID = "sourceClientId"
        case sourceClientIDUpper = "sourceClientID"
        case version
        case params
        case result
        case clientID = "clientId"
        case clientIDUpper = "clientID"
        case handledByClientID = "handledByClientId"
        case handledByClientIDUpper = "handledByClientID"
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        type = try container.decode(String.self, forKey: .type)
        method = try container.decodeIfPresent(String.self, forKey: .method)
        requestID = try container.decodeIfPresent(String.self, forKey: .requestID)
            ?? container.decodeIfPresent(String.self, forKey: .requestIDUpper)
        sourceClientID = try container.decodeIfPresent(String.self, forKey: .sourceClientID)
            ?? container.decodeIfPresent(String.self, forKey: .sourceClientIDUpper)
        version = try container.decodeIfPresent(Int.self, forKey: .version)
        params = try container.decodeIfPresent(Params.self, forKey: .params)
        result = try container.decodeIfPresent(ResultPayload.self, forKey: .result)
        clientID = try container.decodeIfPresent(String.self, forKey: .clientID)
            ?? container.decodeIfPresent(String.self, forKey: .clientIDUpper)
        handledByClientID = try container.decodeIfPresent(String.self, forKey: .handledByClientID)
            ?? container.decodeIfPresent(String.self, forKey: .handledByClientIDUpper)
    }
}

private struct ResultPayload: Decodable {
    let clientID: String?
    let handledByClientID: String?

    private enum CodingKeys: String, CodingKey {
        case clientID = "clientId"
        case clientIDUpper = "clientID"
        case handledByClientID = "handledByClientId"
        case handledByClientIDUpper = "handledByClientID"
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        clientID = try container.decodeIfPresent(String.self, forKey: .clientID)
            ?? container.decodeIfPresent(String.self, forKey: .clientIDUpper)
        handledByClientID = try container.decodeIfPresent(String.self, forKey: .handledByClientID)
            ?? container.decodeIfPresent(String.self, forKey: .handledByClientIDUpper)
    }
}

private struct Params: Decodable {
    let conversationID: String?
    let hostID: String?
    let following: Bool?
    let change: ChangePayload?

    private enum CodingKeys: String, CodingKey {
        case conversationID = "conversationId"
        case conversationIDUpper = "conversationID"
        case hostID = "hostId"
        case hostIDUpper = "hostID"
        case following
        case change
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        conversationID = try container.decodeIfPresent(String.self, forKey: .conversationID)
            ?? container.decodeIfPresent(String.self, forKey: .conversationIDUpper)
        hostID = try container.decodeIfPresent(String.self, forKey: .hostID)
            ?? container.decodeIfPresent(String.self, forKey: .hostIDUpper)
        following = try container.decodeIfPresent(Bool.self, forKey: .following)
        change = try container.decodeIfPresent(ChangePayload.self, forKey: .change)
    }
}

private struct ChangePayload: Decodable {
    let type: String
    let conversationState: ConversationStateMinimal?
    let patches: [PatchMinimal]

    private enum CodingKeys: String, CodingKey {
        case type
        case conversationState
        case patches
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        type = try container.decode(String.self, forKey: .type)
        conversationState = try container.decodeIfPresent(ConversationStateMinimal.self, forKey: .conversationState)
        patches = try container.decodeIfPresent([PatchMinimal].self, forKey: .patches) ?? []
    }
}

private struct PatchMinimal: Decodable {
    let path: [String]
    let runtimeStatus: CodexTaskRuntimeStatus?
    let activeFlags: Set<CodexTaskActiveFlag>?

    private enum CodingKeys: String, CodingKey {
        case path
        case value
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        path = try container.decodeIfPresent([PatchPathComponent].self, forKey: .path)?.map(\.value) ?? []
        let loweredPath = path.map { $0.lowercased() }
        guard loweredPath.contains("threadruntimestatus"), container.contains(.value) else {
            runtimeStatus = nil
            activeFlags = nil
            return
        }

        if loweredPath.last == "threadruntimestatus",
           let value = try? container.decode(RuntimeStatusMinimal.self, forKey: .value)
        {
            runtimeStatus = value.type
            activeFlags = value.activeFlags
        } else if loweredPath.last == "type" {
            runtimeStatus = try? container.decode(CodexTaskRuntimeStatus.self, forKey: .value)
            activeFlags = nil
        } else if loweredPath.last == "activeflags" {
            runtimeStatus = nil
            let rawFlags = try? container.decode([String].self, forKey: .value)
            activeFlags = rawFlags.map { Set($0.compactMap(CodexTaskActiveFlag.init(rawValue:))) }
        } else {
            runtimeStatus = nil
            activeFlags = nil
        }
    }
}

private struct PatchPathComponent: Decodable {
    let value: String

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let string = try? container.decode(String.self) {
            value = string
        } else if let integer = try? container.decode(Int.self) {
            value = String(integer)
        } else {
            value = ""
        }
    }
}

private struct ConversationStateMinimal: Decodable {
    let agentNickname: String?
    let source: SourceMinimal?
    let runtimeStatus: RuntimeStatusMinimal?
    let requests: RequestCollection?
    let turns: TurnCollection?
    let turnHistory: TurnHistoryMinimal?

    private enum CodingKeys: String, CodingKey {
        case agentNickname
        case source
        case runtimeStatus = "threadRuntimeStatus"
        case requests
        case turns
        case turnHistory
    }

    var hasPendingPlanImplementation: Bool {
        let requestPlan = requests?.methods.contains(where: {
            $0.lowercased().contains("planimplementation")
        }) ?? false
        return requestPlan
            || (turns?.hasPendingPlanImplementation ?? false)
            || (turnHistory?.hasPendingPlanImplementation ?? false)
    }
}

private struct SourceMinimal: Decodable {
    let kind: String?

    private struct AnyKey: CodingKey {
        let stringValue: String
        let intValue: Int? = nil
        init?(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { return nil }
    }

    init(from decoder: Decoder) throws {
        if let container = try? decoder.singleValueContainer(),
           let value = try? container.decode(String.self)
        {
            kind = value
            return
        }

        let container = try decoder.container(keyedBy: AnyKey.self)
        let keys = Set(container.allKeys.map(\.stringValue))
        if keys.contains("subAgent") {
            kind = "subAgent"
        } else if keys.contains("custom") {
            kind = "custom"
        } else {
            kind = keys.sorted().first
        }
    }
}

private struct RuntimeStatusMinimal: Decodable {
    let type: CodexTaskRuntimeStatus
    let activeFlags: Set<CodexTaskActiveFlag>

    private enum CodingKeys: String, CodingKey {
        case type
        case activeFlags
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        type = try container.decode(CodexTaskRuntimeStatus.self, forKey: .type)
        let rawFlags = try container.decodeIfPresent([String].self, forKey: .activeFlags) ?? []
        activeFlags = Set(rawFlags.compactMap(CodexTaskActiveFlag.init(rawValue:)))
    }
}

private struct RequestCollection: Decodable {
    let methods: Set<String>

    init(from decoder: Decoder) throws {
        if var container = try? decoder.unkeyedContainer() {
            var values = Set<String>()
            while !container.isAtEnd {
                if let request = try? container.decode(RequestMinimal.self), let method = request.method {
                    values.insert(method)
                }
            }
            methods = values
            return
        }

        let container = try decoder.container(keyedBy: AnyStringKey.self)
        methods = Set(container.allKeys.compactMap { key in
            (try? container.decode(RequestMinimal.self, forKey: key))?.method
        })
    }
}

private struct RequestMinimal: Decodable {
    let method: String?

    private enum CodingKeys: String, CodingKey {
        case method
        case requestType
        case request
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        method = try container.decodeIfPresent(String.self, forKey: .method)
            ?? container.decodeIfPresent(String.self, forKey: .requestType)
            ?? container.decodeIfPresent(RequestMinimal.self, forKey: .request)?.method
    }
}

private struct TurnCollection: Decodable {
    let turns: [TurnMinimal]

    var hasPendingPlanImplementation: Bool {
        turns.contains(where: \.hasPendingPlanImplementation)
    }

    init(from decoder: Decoder) throws {
        if var container = try? decoder.unkeyedContainer() {
            var values: [TurnMinimal] = []
            while !container.isAtEnd {
                if let value = try? container.decode(TurnMinimal.self) { values.append(value) }
            }
            turns = values
            return
        }

        let container = try decoder.container(keyedBy: AnyStringKey.self)
        turns = container.allKeys.compactMap { try? container.decode(TurnMinimal.self, forKey: $0) }
    }
}

private struct TurnMinimal: Decodable {
    let items: [TurnItemMinimal]

    private enum CodingKeys: String, CodingKey { case items }

    var hasPendingPlanImplementation: Bool {
        items.contains { $0.type == "planImplementation" && !$0.isCompleted }
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        items = try container.decodeIfPresent([TurnItemMinimal].self, forKey: .items) ?? []
    }
}

private struct TurnItemMinimal: Decodable {
    let type: String
    let isCompleted: Bool

    private enum CodingKeys: String, CodingKey {
        case type
        case isCompleted
        case completed
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        type = try container.decodeIfPresent(String.self, forKey: .type) ?? ""
        isCompleted = try container.decodeIfPresent(Bool.self, forKey: .isCompleted)
            ?? container.decodeIfPresent(Bool.self, forKey: .completed)
            ?? false
    }
}

private struct TurnHistoryMinimal: Decodable {
    let history: TurnHistoryContainer?

    private enum CodingKeys: String, CodingKey { case history }

    var hasPendingPlanImplementation: Bool {
        history?.entitiesByKey.values.contains(where: \.hasPendingPlanImplementation) ?? false
    }
}

private struct TurnHistoryContainer: Decodable {
    let entitiesByKey: [String: TurnMinimal]

    private enum CodingKeys: String, CodingKey { case entitiesByKey }
}

private struct AnyStringKey: CodingKey {
    let stringValue: String
    let intValue: Int? = nil
    init?(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { return nil }
}
