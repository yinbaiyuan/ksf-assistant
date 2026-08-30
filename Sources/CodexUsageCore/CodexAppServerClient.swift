import Foundation

public protocol CodexUsageProviding {
    func fetchRateLimits() async throws -> RateLimitsResponse
    func fetchTokenUsage() async throws -> TokenUsageResponse
}

public protocol AppServerTransport: AnyObject {
    var lines: AsyncStream<String> { get }
    func start() throws
    func send(_ line: String) throws
    func stop()
}

public enum CodexUsageError: Error, Equatable, LocalizedError {
    case codexMissing
    case authenticationRequired
    case unsupportedProtocol(String)
    case offline(String)
    case protocolFailure(String)

    public var errorDescription: String? {
        switch self {
        case .codexMissing:
            return "Codex executable was not found."
        case .authenticationRequired:
            return "Sign in to Codex with ChatGPT to read account usage."
        case let .unsupportedProtocol(message):
            return "This Codex build does not support the required account methods. \(message)"
        case let .offline(message):
            return "Codex usage is temporarily unavailable. \(message)"
        case let .protocolFailure(message):
            return "Codex App Server returned an invalid response. \(message)"
        }
    }
}

private final class PendingRequestRegistry {
    private let lock = NSLock()
    private var continuations: [Int: CheckedContinuation<Data, Error>] = [:]

    func register(_ continuation: CheckedContinuation<Data, Error>, for id: Int) {
        lock.lock()
        continuations[id] = continuation
        lock.unlock()
    }

    func take(id: Int) -> CheckedContinuation<Data, Error>? {
        lock.lock()
        let continuation = continuations.removeValue(forKey: id)
        lock.unlock()
        return continuation
    }

    func fail(id: Int, with error: Error) {
        take(id: id)?.resume(throwing: error)
    }

    func failAll(with error: Error) {
        lock.lock()
        let current = Array(continuations.values)
        continuations.removeAll()
        lock.unlock()
        current.forEach { $0.resume(throwing: error) }
    }
}

public actor CodexAppServerClient: CodexUsageProviding {
    public typealias TransportFactory = () throws -> AppServerTransport

    private let transportFactory: TransportFactory
    private let decoder = JSONDecoder()
    private var transport: AppServerTransport?
    private var readerTask: Task<Void, Never>?
    private var startTask: Task<Void, Error>?
    private var nextRequestID = 0
    private let pending = PendingRequestRegistry()
    private var ready = false
    private var stopping = false
    private var requestLocked = false
    private var requestWaiters: [CheckedContinuation<Void, Never>] = []

    public init(transportFactory: @escaping TransportFactory) {
        self.transportFactory = transportFactory
    }

    public func fetchRateLimits() async throws -> RateLimitsResponse {
        await acquireRequestLock()
        defer { releaseRequestLock() }
        let data = try await request(method: "account/rateLimits/read", params: nil)
        return try decode(RateLimitsResponse.self, from: data)
    }

    public func fetchTokenUsage() async throws -> TokenUsageResponse {
        await acquireRequestLock()
        defer { releaseRequestLock() }
        let data = try await request(method: "account/usage/read", params: nil)
        return try decode(TokenUsageResponse.self, from: data)
    }

    public func fetchThreads() async throws -> [CodexThreadMetadata] {
        await acquireRequestLock()
        defer { releaseRequestLock() }

        var cursor: String?
        var result: [CodexThreadMetadata] = []
        repeat {
            var params: [String: Any] = [
                "limit": 100,
                "archived": false,
                "sortKey": "updated_at",
                "sortDirection": "desc",
                "sourceKinds": [
                    "cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview",
                    "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown",
                ],
            ]
            if let cursor { params["cursor"] = cursor }
            let data = try await request(method: "thread/list", params: params)
            let page = try decode(CodexThreadListResponse.self, from: data)
            result.append(contentsOf: page.data)
            cursor = page.nextCursor
        } while cursor != nil && result.count < 10_000
        return result
    }

    /// Creates and names an idle thread. The initial turn is deliberately not
    /// submitted on this App Server transport: Codex Desktop must own that
    /// submission so its visible window receives the live stream.
    public func createDraftThread(cwd: String, name: String) async throws -> String {
        await acquireRequestLock()
        defer { releaseRequestLock() }

        let startData = try await request(
            method: "thread/start",
            params: [
                "cwd": cwd,
                "serviceName": "codex_usage_bar",
            ]
        )
        let started = try decode(ThreadStartResult.self, from: startData)
        guard !started.thread.id.isEmpty else {
            throw CodexUsageError.protocolFailure("thread/start returned an empty thread id.")
        }

        _ = try await request(
            method: "thread/name/set",
            params: [
                "threadId": started.thread.id,
                "name": name,
            ]
        )
        return started.thread.id
    }

    public func stop() {
        stopping = true
        readerTask?.cancel()
        readerTask = nil
        transport?.stop()
        transport = nil
        ready = false
        startTask = nil
        failPending(with: CodexUsageError.offline("The App Server session stopped."))
        stopping = false
    }

    private func request(method: String, params: [String: Any]?) async throws -> Data {
        try await ensureStarted()
        return try await sendAndAwait(method: method, params: params)
    }

    private func ensureStarted() async throws {
        if ready { return }
        if let startTask {
            return try await startTask.value
        }

        let task = Task { try await self.startSession() }
        startTask = task

        do {
            try await task.value
            ready = true
            startTask = nil
        } catch {
            startTask = nil
            ready = false
            transport?.stop()
            transport = nil
            throw error
        }
    }

    private func startSession() async throws {
        let newTransport = try transportFactory()
        try newTransport.start()
        transport = newTransport
        stopping = false

        let stream = newTransport.lines
        readerTask = Task { [weak self] in
            for await line in stream {
                guard !Task.isCancelled else { break }
                await self?.handle(line: line)
            }
            await self?.handleTransportEnd()
        }

        _ = try await sendAndAwait(
            method: "initialize",
            params: [
                "clientInfo": [
                    "name": "codex_usage_bar",
                    "title": "Codex Usage Bar",
                    "version": "0.2.0",
                ],
            ]
        )
        try sendNotification(method: "initialized", params: [:])
    }

    private func sendAndAwait(method: String, params: [String: Any]?) async throws -> Data {
        guard let transport else {
            throw CodexUsageError.offline("No App Server transport is running.")
        }

        let id = nextRequestID
        nextRequestID += 1
        var object: [String: Any] = ["method": method, "id": id]
        if let params { object["params"] = params }
        let line = try encodeJSONObject(object)

        return try await withCheckedThrowingContinuation { continuation in
            pending.register(continuation, for: id)
            scheduleTimeout(for: id)
            do {
                try transport.send(line)
            } catch {
                pending.fail(id: id, with: mapTransportError(error))
            }
        }
    }

    private func sendNotification(method: String, params: [String: Any]) throws {
        guard let transport else {
            throw CodexUsageError.offline("No App Server transport is running.")
        }
        try transport.send(encodeJSONObject(["method": method, "params": params]))
    }

    private func encodeJSONObject(_ object: [String: Any]) throws -> String {
        do {
            let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
            guard let string = String(data: data, encoding: .utf8) else {
                throw CodexUsageError.protocolFailure("Could not encode a UTF-8 request.")
            }
            return string
        } catch let error as CodexUsageError {
            throw error
        } catch {
            throw CodexUsageError.protocolFailure(error.localizedDescription)
        }
    }

    private func handle(line: String) {
        guard let data = line.data(using: .utf8) else { return }
        var matchedContinuation: CheckedContinuation<Data, Error>?

        do {
            guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                return
            }

            // Unknown notifications must never corrupt pending requests.
            guard let id = object["id"] as? Int else { return }
            guard let continuation = pending.take(id: id) else { return }
            matchedContinuation = continuation

            if let errorObject = object["error"] as? [String: Any] {
                continuation.resume(throwing: mapRPCError(errorObject))
                return
            }

            guard let result = object["result"] else {
                continuation.resume(throwing: CodexUsageError.protocolFailure("Response \(id) has no result."))
                return
            }

            let resultData: Data
            if result is NSNull {
                resultData = Data("null".utf8)
            } else {
                resultData = try JSONSerialization.data(withJSONObject: result)
            }
            continuation.resume(returning: resultData)
        } catch {
            matchedContinuation?.resume(
                throwing: CodexUsageError.protocolFailure(error.localizedDescription)
            )
        }
    }

    private func scheduleTimeout(for id: Int) {
        Task { [weak self] in
            try? await Task.sleep(nanoseconds: 15_000_000_000)
            await self?.timeoutRequest(id: id)
        }
    }

    private func timeoutRequest(id: Int) {
        pending.fail(
            id: id,
            with: CodexUsageError.offline("Codex App Server did not respond within 15 seconds.")
        )
    }

    private func handleTransportEnd() {
        guard !stopping else { return }
        ready = false
        startTask = nil
        transport = nil
        failPending(with: CodexUsageError.offline("Codex App Server exited."))
    }

    private func failPending(with error: Error) {
        pending.failAll(with: error)
    }

    private func decode<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        do {
            return try decoder.decode(type, from: data)
        } catch {
            throw CodexUsageError.protocolFailure(error.localizedDescription)
        }
    }

    private func mapRPCError(_ object: [String: Any]) -> CodexUsageError {
        let code = object["code"] as? Int
        let message = object["message"] as? String ?? "Unknown error"
        let normalized = message.lowercased()

        if code == -32601 || normalized.contains("method not found") || normalized.contains("unsupported") {
            return .unsupportedProtocol(message)
        }
        if code == 401 || normalized.contains("unauthorized") || normalized.contains("sign in") || normalized.contains("authentication") {
            return .authenticationRequired
        }
        if normalized.contains("network") || normalized.contains("connection") || normalized.contains("offline") {
            return .offline(message)
        }
        return .protocolFailure(message)
    }

    private func mapTransportError(_ error: Error) -> CodexUsageError {
        if let mapped = error as? CodexUsageError { return mapped }
        return .offline(error.localizedDescription)
    }

    private func acquireRequestLock() async {
        if !requestLocked {
            requestLocked = true
            return
        }
        await withCheckedContinuation { continuation in
            requestWaiters.append(continuation)
        }
    }

    private func releaseRequestLock() {
        if requestWaiters.isEmpty {
            requestLocked = false
        } else {
            requestWaiters.removeFirst().resume()
        }
    }
}

private struct ThreadStartResult: Decodable {
    let thread: StartedThread

    struct StartedThread: Decodable {
        let id: String
    }
}
