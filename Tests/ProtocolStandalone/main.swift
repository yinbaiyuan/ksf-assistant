import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private func expect<T: Equatable>(_ actual: T, _ expected: T, _ message: String) throws {
    guard actual == expected else {
        throw TestFailure(description: "\(message): expected \(expected), got \(actual)")
    }
}

private final class FakeTransport: AppServerTransport {
    let lines: AsyncStream<String>
    private let continuation: AsyncStream<String>.Continuation
    private let lock = NSLock()
    private let rejectName: Bool
    private(set) var sentMethods: [String] = []
    private var sentObjects: [[String: Any]] = []

    init(rejectName: Bool = false) {
        self.rejectName = rejectName
        var captured: AsyncStream<String>.Continuation!
        lines = AsyncStream { captured = $0 }
        continuation = captured
    }

    func start() throws {}

    func params(for method: String) -> [String: Any]? {
        lock.lock()
        defer { lock.unlock() }
        return sentObjects.first { $0["method"] as? String == method }?["params"] as? [String: Any]
    }

    func send(_ line: String) throws {
        guard
            let data = line.data(using: .utf8),
            let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
            let method = object["method"] as? String
        else {
            throw TestFailure(description: "invalid outbound JSON")
        }

        lock.lock()
        sentMethods.append(method)
        sentObjects.append(object)
        lock.unlock()

        guard let id = object["id"] as? Int else { return }

        switch method {
        case "initialize":
            continuation.yield(#"{"id":\#(id),"result":{"userAgent":"fake"}}"#)
        case "account/rateLimits/read":
            continuation.yield(#"{"method":"account/rateLimits/updated","params":{"source":"fake"}}"#)
            continuation.yield(
                #"{"id":\#(id),"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":25,"windowDurationMins":300,"resetsAt":2000}}}}"#
            )
        case "account/usage/read":
            continuation.yield(
                #"{"id":\#(id),"result":{"summary":{"lifetimeTokens":1234},"dailyUsageBuckets":[{"startDate":"2026-08-29","tokens":99}]}}"#
            )
        case "thread/list":
            continuation.yield(
                #"{"id":\#(id),"result":{"data":[{"id":"root","cwd":"/tmp/project","createdAt":1,"updatedAt":2,"path":"/tmp/root.jsonl"}],"nextCursor":null}}"#
            )
        case "thread/start":
            continuation.yield(#"{"id":\#(id),"result":{"thread":{"id":"new-thread"}}}"#)
        case "thread/name/set":
            if rejectName {
                continuation.yield(#"{"id":\#(id),"error":{"code":-32000,"message":"name rejected"}}"#)
            } else {
                continuation.yield(#"{"id":\#(id),"result":{}}"#)
            }
        default:
            continuation.yield(#"{"id":\#(id),"error":{"code":-32601,"message":"Method not found"}}"#)
        }
    }

    func stop() {
        continuation.finish()
    }
}

private final class ErrorTransport: AppServerTransport {
    let lines: AsyncStream<String>
    private let continuation: AsyncStream<String>.Continuation
    private let code: Int
    private let message: String

    init(code: Int = -32601, message: String = "Method not found") {
        self.code = code
        self.message = message
        var captured: AsyncStream<String>.Continuation!
        lines = AsyncStream { captured = $0 }
        continuation = captured
    }

    func start() throws {}

    func send(_ line: String) throws {
        guard
            let data = line.data(using: .utf8),
            let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
            let method = object["method"] as? String,
            let id = object["id"] as? Int
        else { return }

        if method == "initialize" {
            continuation.yield(#"{"id":\#(id),"result":{}}"#)
        } else {
            let errorObject: [String: Any] = ["code": code, "message": message]
            let responseObject: [String: Any] = [
                "id": id,
                "error": errorObject,
            ]
            let messageData = try JSONSerialization.data(withJSONObject: responseObject)
            continuation.yield(String(decoding: messageData, as: UTF8.self))
        }
    }

    func stop() { continuation.finish() }
}

private final class ReconnectTransport: AppServerTransport {
    let lines: AsyncStream<String>
    private let continuation: AsyncStream<String>.Continuation
    private let generation: Int

    init(generation: Int) {
        self.generation = generation
        var captured: AsyncStream<String>.Continuation!
        lines = AsyncStream { captured = $0 }
        continuation = captured
    }

    func start() throws {}

    func send(_ line: String) throws {
        guard
            let data = line.data(using: .utf8),
            let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
            let method = object["method"] as? String,
            let id = object["id"] as? Int
        else { return }

        if method == "initialize" {
            continuation.yield(#"{"id":\#(id),"result":{}}"#)
        } else if method == "account/rateLimits/read" {
            let used = generation == 1 ? 25 : 40
            continuation.yield(
                #"{"id":\#(id),"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":\#(used)}}}}"#
            )
            if generation == 1 { continuation.finish() }
        }
    }

    func stop() { continuation.finish() }
}

@main
private enum ProtocolTestRunner {
    static func main() async {
        do {
            try await testHandshakeAndNotifications()
            try await testRPCErrorMapping()
            try await testAuthenticationAndOfflineErrorMapping()
            try await testReconnectAfterTransportExit()
            try await testDraftThreadCreation()
            try await testDraftThreadNameFailureIsReported()
            print("PASS 6 app-server protocol tests")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }

    private static func testHandshakeAndNotifications() async throws {
        let transport = FakeTransport()
        let client = CodexAppServerClient(transportFactory: { transport })
        let limits = try await client.fetchRateLimits()
        let usage = try await client.fetchTokenUsage()
        let threads = try await client.fetchThreads()

        guard limits.generalBucket?.headlineRemainingPercent == 75 else {
            throw TestFailure(description: "rate-limit response was not decoded")
        }
        guard usage.summary.lifetimeTokens == 1_234 else {
            throw TestFailure(description: "token response was not decoded")
        }
        guard threads.map(\.id) == ["root"] else {
            throw TestFailure(description: "thread response was not decoded")
        }
        guard transport.sentMethods == [
            "initialize",
            "initialized",
            "account/rateLimits/read",
            "account/usage/read",
            "thread/list",
        ] else {
            throw TestFailure(description: "unexpected request order: \(transport.sentMethods)")
        }
        await client.stop()
        print("PASS handshake, notification interleave, and serialized reads")
    }

    private static func testRPCErrorMapping() async throws {
        let client = CodexAppServerClient { ErrorTransport() }
        do {
            _ = try await client.fetchRateLimits()
            throw TestFailure(description: "method-not-found response did not fail")
        } catch let error as CodexUsageError {
            guard case .unsupportedProtocol = error else {
                throw TestFailure(description: "wrong error mapping: \(error)")
            }
        }
        await client.stop()
        print("PASS protocol error mapping")
    }

    private static func testAuthenticationAndOfflineErrorMapping() async throws {
        let authClient = CodexAppServerClient {
            ErrorTransport(code: 401, message: "ChatGPT authentication required")
        }
        do {
            _ = try await authClient.fetchTokenUsage()
            throw TestFailure(description: "authentication response did not fail")
        } catch let error as CodexUsageError {
            guard error == .authenticationRequired else {
                throw TestFailure(description: "wrong auth mapping: \(error)")
            }
        }
        await authClient.stop()

        let offlineClient = CodexAppServerClient {
            ErrorTransport(code: -32_000, message: "Network connection offline")
        }
        do {
            _ = try await offlineClient.fetchRateLimits()
            throw TestFailure(description: "offline response did not fail")
        } catch let error as CodexUsageError {
            guard case .offline = error else {
                throw TestFailure(description: "wrong offline mapping: \(error)")
            }
        }
        await offlineClient.stop()
        print("PASS authentication and offline error mapping")
    }

    private static func testReconnectAfterTransportExit() async throws {
        var generation = 0
        let client = CodexAppServerClient {
            generation += 1
            return ReconnectTransport(generation: generation)
        }
        let first = try await client.fetchRateLimits()
        guard first.generalBucket?.headlineRemainingPercent == 75 else {
            throw TestFailure(description: "first transport response was wrong")
        }
        try await Task.sleep(nanoseconds: 100_000_000)
        let second = try await client.fetchRateLimits()
        guard generation == 2, second.generalBucket?.headlineRemainingPercent == 60 else {
            throw TestFailure(description: "client did not reconnect after transport exit")
        }
        await client.stop()
        print("PASS transport exit and reconnect")
    }

    private static func testDraftThreadCreation() async throws {
        let transport = FakeTransport()
        let client = CodexAppServerClient { transport }
        let threadID = try await client.createDraftThread(
            cwd: "/tmp/project",
            name: "家庭网络 · 新任务"
        )
        try expect(threadID, "new-thread", "new thread id is returned")
        try expect(
            transport.sentMethods,
            ["initialize", "initialized", "thread/start", "thread/name/set"],
            "draft thread follows the documented request order"
        )
        let threadParams = transport.params(for: "thread/start")
        try expect(threadParams?["cwd"] as? String, "/tmp/project", "thread uses selected cwd")
        try expect(
            threadParams?["serviceName"] as? String,
            "codex_usage_bar",
            "thread identifies the local integration"
        )
        let nameParams = transport.params(for: "thread/name/set")
        try expect(nameParams?["threadId"] as? String, "new-thread", "name targets new thread")
        try expect(nameParams?["name"] as? String, "家庭网络 · 新任务", "explicit name is forwarded")
        await client.stop()
        print("PASS draft thread creation without background turn submission")
    }

    private static func testDraftThreadNameFailureIsReported() async throws {
        let transport = FakeTransport(rejectName: true)
        let client = CodexAppServerClient { transport }
        do {
            _ = try await client.createDraftThread(
                cwd: "/tmp/project",
                name: "家庭网络 · 新任务"
            )
            throw TestFailure(description: "failed explicit name was accepted")
        } catch let error as CodexUsageError {
            guard case let .protocolFailure(message) = error,
                  message.contains("name rejected") else {
                throw TestFailure(description: "wrong naming failure: \(error)")
            }
        }
        await client.stop()
        print("PASS explicit task name failure reporting")
    }
}
