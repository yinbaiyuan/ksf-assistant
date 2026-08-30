import Foundation

public enum CodexDesktopTaskSubmissionError: Error, Equatable, LocalizedError {
    case timedOut
    case invalidResponse
    case taskWasNotOpened
    case ownerUnavailable
    case submissionRejected(String)

    public var errorDescription: String? {
        switch self {
        case .timedOut:
            return "Codex 桌面端未在 20 秒内接管新任务。"
        case .invalidResponse:
            return "Codex 桌面 IPC 返回了无法识别的响应。"
        case .taskWasNotOpened:
            return "Codex 已打开，但没有跟随新任务。"
        case .ownerUnavailable:
            return "Codex 没有可用的任务窗口。"
        case let .submissionRejected(message):
            return "Codex 未能提交首次任务说明。\(message)"
        }
    }
}

/// Opens an already-persisted thread, waits until a Codex Desktop window
/// follows it, then asks that exact window to submit the initial turn. This is kept
/// separate from account usage: it is a narrow compatibility adapter for the
/// desktop-only follower protocol.
public actor CodexDesktopTaskSubmissionClient {
    public typealias TransportFactory = @Sendable () throws -> DesktopIPCTransport
    public typealias OpenTask = @MainActor @Sendable () throws -> Void

    private let transportFactory: TransportFactory
    private let ownerRetryDelayNanoseconds: UInt64
    private let maximumOwnerAttempts: Int

    public init(
        transportFactory: @escaping TransportFactory,
        ownerRetryDelayNanoseconds: UInt64 = 250_000_000,
        maximumOwnerAttempts: Int = 40
    ) {
        self.transportFactory = transportFactory
        self.ownerRetryDelayNanoseconds = ownerRetryDelayNanoseconds
        self.maximumOwnerAttempts = max(1, maximumOwnerAttempts)
    }

    public func submitInitialTurn(
        threadID: String,
        cwd: String,
        prompt: String,
        openTask: @escaping OpenTask
    ) async throws {
        let transport = try transportFactory()
        try transport.start()

        let channel = SubmissionEventChannel()
        let readTask = Task {
            var frameDecoder = DesktopIPCFrameDecoder()
            do {
                for await chunk in transport.incoming {
                    guard !Task.isCancelled else { break }
                    for frame in try frameDecoder.append(chunk) {
                        channel.yield(try SubmissionEvent.decode(frame))
                    }
                }
                channel.finish()
            } catch {
                channel.finish(throwing: error)
            }
        }
        let timeoutTask = Task {
            try? await Task.sleep(nanoseconds: 20_000_000_000)
            guard !Task.isCancelled else { return }
            channel.finish(throwing: CodexDesktopTaskSubmissionError.timedOut)
            transport.stop()
        }

        defer {
            timeoutTask.cancel()
            readTask.cancel()
            channel.finish()
            transport.stop()
        }

        var iterator = channel.stream.makeAsyncIterator()
        let initializeID = "codex-usage-bar-submit-initialize-\(UUID().uuidString)"
        try send(DesktopIPCMessageEncoder.initialize(requestID: initializeID), through: transport)

        let clientID = try await awaitInitialization(
            requestID: initializeID,
            iterator: &iterator,
            transport: transport
        )

        try await openTask()

        let follower = try await awaitFollower(
            threadID: threadID,
            iterator: &iterator,
            transport: transport
        )
        for attempt in 1...maximumOwnerAttempts {
            let startRequestID = "codex-usage-bar-submit-turn-\(UUID().uuidString)"
            try send(
                DesktopIPCMessageEncoder.startTurn(
                    requestID: startRequestID,
                    sourceClientID: clientID,
                    targetClientID: follower.sourceClientID,
                    taskID: threadID,
                    cwd: cwd,
                    prompt: prompt
                ),
                through: transport
            )
            let response = try await awaitTurnSubmission(
                requestID: startRequestID,
                iterator: &iterator,
                transport: transport
            )
            if response.isSuccess { return }
            guard response.errorMessage == "no-client-found" else {
                throw CodexDesktopTaskSubmissionError.submissionRejected(
                    response.errorMessage ?? ""
                )
            }
            guard attempt < maximumOwnerAttempts else {
                throw CodexDesktopTaskSubmissionError.ownerUnavailable
            }
            try await Task.sleep(nanoseconds: ownerRetryDelayNanoseconds)
        }
    }

    private func awaitInitialization(
        requestID: String,
        iterator: inout AsyncThrowingStream<SubmissionEvent, Error>.Iterator,
        transport: DesktopIPCTransport
    ) async throws -> String {
        while let event = try await iterator.next() {
            try handleDiscovery(event, transport: transport)
            guard case let .response(response) = event, response.requestID == requestID else {
                continue
            }
            guard response.isSuccess, let clientID = response.clientID, !clientID.isEmpty else {
                throw CodexDesktopTaskSubmissionError.invalidResponse
            }
            return clientID
        }
        throw CodexDesktopTaskSubmissionError.invalidResponse
    }

    private func awaitFollower(
        threadID: String,
        iterator: inout AsyncThrowingStream<SubmissionEvent, Error>.Iterator,
        transport: DesktopIPCTransport
    ) async throws -> Follower {
        while let event = try await iterator.next() {
            try handleDiscovery(event, transport: transport)
            if case let .following(follower) = event,
               follower.threadID == threadID,
               follower.following {
                return follower
            }
        }
        throw CodexDesktopTaskSubmissionError.taskWasNotOpened
    }

    private func awaitTurnSubmission(
        requestID: String,
        iterator: inout AsyncThrowingStream<SubmissionEvent, Error>.Iterator,
        transport: DesktopIPCTransport
    ) async throws -> SubmissionResponse {
        while let event = try await iterator.next() {
            try handleDiscovery(event, transport: transport)
            guard case let .response(response) = event, response.requestID == requestID else {
                continue
            }
            return response
        }
        throw CodexDesktopTaskSubmissionError.invalidResponse
    }

    private func handleDiscovery(
        _ event: SubmissionEvent,
        transport: DesktopIPCTransport
    ) throws {
        guard case let .clientDiscoveryRequested(requestID) = event else { return }
        try send(
            DesktopIPCMessageEncoder.clientDiscoveryResponse(requestID: requestID),
            through: transport
        )
    }

    private func send(_ payload: Data, through transport: DesktopIPCTransport) throws {
        try transport.send(DesktopIPCFrameEncoder.encode(payload))
    }
}

private final class SubmissionEventChannel: @unchecked Sendable {
    let stream: AsyncThrowingStream<SubmissionEvent, Error>
    private let continuation: AsyncThrowingStream<SubmissionEvent, Error>.Continuation

    init() {
        var captured: AsyncThrowingStream<SubmissionEvent, Error>.Continuation!
        stream = AsyncThrowingStream { captured = $0 }
        continuation = captured
    }

    func yield(_ event: SubmissionEvent) {
        continuation.yield(event)
    }

    func finish(throwing error: Error? = nil) {
        if let error {
            continuation.finish(throwing: error)
        } else {
            continuation.finish()
        }
    }
}

private struct Follower: Equatable {
    let sourceClientID: String
    let threadID: String
    let hostID: String
    let following: Bool
}

private struct SubmissionResponse: Equatable {
    let requestID: String
    let resultType: String?
    let clientID: String?
    let handledByClientID: String?
    let errorMessage: String?

    var isSuccess: Bool {
        errorMessage == nil && resultType != "error"
    }
}

private enum SubmissionEvent: Equatable {
    case response(SubmissionResponse)
    case following(Follower)
    case clientDiscoveryRequested(requestID: String)
    case ignored

    static func decode(_ data: Data) throws -> SubmissionEvent {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = object["type"] as? String else {
            throw CodexDesktopTaskSubmissionError.invalidResponse
        }

        if type == "client-discovery-request",
           let requestID = string(object, keys: ["requestId", "requestID"]) {
            return .clientDiscoveryRequested(requestID: requestID)
        }

        if type == "response",
           let requestID = string(object, keys: ["requestId", "requestID"]) {
            let result = object["result"] as? [String: Any]
            return .response(SubmissionResponse(
                requestID: requestID,
                resultType: object["resultType"] as? String,
                clientID: string(result, keys: ["clientId", "clientID"])
                    ?? string(object, keys: ["clientId", "clientID"]),
                handledByClientID: string(object, keys: ["handledByClientId", "handledByClientID"])
                    ?? string(result, keys: ["handledByClientId", "handledByClientID"]),
                errorMessage: errorMessage(object["error"])
            ))
        }

        if type == "broadcast",
           object["method"] as? String == "thread-stream-following-changed",
           object["version"] as? Int == 1,
           let sourceClientID = string(object, keys: ["sourceClientId", "sourceClientID"]),
           let params = object["params"] as? [String: Any],
           let threadID = string(params, keys: ["conversationId", "conversationID"]),
           let hostID = string(params, keys: ["hostId", "hostID"]),
           let following = params["following"] as? Bool {
            return .following(Follower(
                sourceClientID: sourceClientID,
                threadID: threadID,
                hostID: hostID,
                following: following
            ))
        }

        return .ignored
    }

    private static func string(
        _ object: [String: Any]?,
        keys: [String]
    ) -> String? {
        guard let object else { return nil }
        for key in keys {
            if let value = object[key] as? String { return value }
        }
        return nil
    }

    private static func errorMessage(_ value: Any?) -> String? {
        if let value = value as? String { return value }
        if let value = value as? [String: Any] {
            return value["message"] as? String ?? value["code"] as? String
        }
        return nil
    }
}
