import Foundation
import XCTest
@testable import CodexUsageCore

final class CodexAppServerClientTests: XCTestCase {
    func testHandshakeIgnoresInterleavedNotificationAndSerializesReads() async throws {
        let transport = TestTransport()
        let client = CodexAppServerClient { transport }

        async let limits = client.fetchRateLimits()
        async let usage = client.fetchTokenUsage()
        let (rateResponse, usageResponse) = try await (limits, usage)

        XCTAssertEqual(rateResponse.generalBucket?.headlineRemainingPercent, 75)
        XCTAssertEqual(usageResponse.summary.lifetimeTokens, 1_234)
        XCTAssertEqual(Array(transport.sentMethods.prefix(2)), ["initialize", "initialized"])
        XCTAssertEqual(
            Set(transport.sentMethods.suffix(2)),
            Set(["account/rateLimits/read", "account/usage/read"])
        )
        await client.stop()
    }

    func testMethodNotFoundMapsToUnsupportedProtocol() async {
        let client = CodexAppServerClient { ErrorTestTransport() }

        do {
            _ = try await client.fetchRateLimits()
            XCTFail("Expected the request to fail")
        } catch let error as CodexUsageError {
            guard case .unsupportedProtocol = error else {
                return XCTFail("Unexpected error: \(error)")
            }
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
        await client.stop()
    }

    func testAuthenticationAndOfflineErrorsAreClassified() async {
        let authClient = CodexAppServerClient {
            ErrorTestTransport(code: 401, message: "ChatGPT authentication required")
        }
        do {
            _ = try await authClient.fetchTokenUsage()
            XCTFail("Expected authentication failure")
        } catch let error as CodexUsageError {
            XCTAssertEqual(error, .authenticationRequired)
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
        await authClient.stop()

        let offlineClient = CodexAppServerClient {
            ErrorTestTransport(code: -32_000, message: "Network connection offline")
        }
        do {
            _ = try await offlineClient.fetchRateLimits()
            XCTFail("Expected offline failure")
        } catch let error as CodexUsageError {
            guard case .offline = error else {
                return XCTFail("Unexpected error: \(error)")
            }
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
        await offlineClient.stop()
    }

    func testTransportExitCreatesFreshSessionForNextRequest() async throws {
        var generation = 0
        let client = CodexAppServerClient {
            generation += 1
            return ReconnectTestTransport(generation: generation)
        }

        let first = try await client.fetchRateLimits()
        XCTAssertEqual(first.generalBucket?.headlineRemainingPercent, 75)
        try await Task.sleep(nanoseconds: 100_000_000)
        let second = try await client.fetchRateLimits()
        XCTAssertEqual(second.generalBucket?.headlineRemainingPercent, 60)
        XCTAssertEqual(generation, 2)
        await client.stop()
    }

    func testThreadListIncludesTopLevelAndSubAgentMetadata() async throws {
        let transport = TestTransport()
        let client = CodexAppServerClient { transport }

        let threads = try await client.fetchThreads()
        XCTAssertEqual(threads.map(\.id), ["root", "child"])
        XCTAssertNil(threads[0].parentThreadId)
        XCTAssertEqual(threads[1].parentThreadId, "root")
        XCTAssertEqual(threads[0].cwd, "/tmp/project")
        await client.stop()
    }

    func testDraftThreadIsCreatedAndNamedWithoutStartingATurn() async throws {
        let transport = TestTransport()
        let client = CodexAppServerClient { transport }

        let threadID = try await client.createDraftThread(
            cwd: "/tmp/project",
            name: "家庭网络 · 新任务"
        )

        XCTAssertEqual(threadID, "new-thread")
        XCTAssertEqual(
            transport.sentMethods,
            ["initialize", "initialized", "thread/start", "thread/name/set"]
        )
        let threadParams = transport.params(for: "thread/start")
        XCTAssertEqual(threadParams?["cwd"] as? String, "/tmp/project")
        XCTAssertEqual(threadParams?["serviceName"] as? String, "codex_usage_bar")
        let nameParams = transport.params(for: "thread/name/set")
        XCTAssertEqual(nameParams?["threadId"] as? String, "new-thread")
        XCTAssertEqual(nameParams?["name"] as? String, "家庭网络 · 新任务")
        await client.stop()
    }

    func testDraftThreadReportsExplicitNameFailure() async {
        let transport = TestTransport(rejectName: true)
        let client = CodexAppServerClient { transport }

        do {
            _ = try await client.createDraftThread(
                cwd: "/tmp/project",
                name: "家庭网络 · 新任务"
            )
            XCTFail("Expected naming failure")
        } catch let error as CodexUsageError {
            guard case let .protocolFailure(message) = error else {
                return XCTFail("Unexpected error: \(error)")
            }
            XCTAssertTrue(message.contains("name rejected"))
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
        await client.stop()
    }
}

private final class TestTransport: AppServerTransport {
    let lines: AsyncStream<String>
    private let continuation: AsyncStream<String>.Continuation
    private let lock = NSLock()
    private let rejectName: Bool
    private var methods: [String] = []
    private var objects: [[String: Any]] = []

    var sentMethods: [String] {
        lock.lock()
        defer { lock.unlock() }
        return methods
    }

    func params(for method: String) -> [String: Any]? {
        lock.lock()
        defer { lock.unlock() }
        return objects.first { $0["method"] as? String == method }?["params"] as? [String: Any]
    }

    init(rejectName: Bool = false) {
        self.rejectName = rejectName
        var captured: AsyncStream<String>.Continuation!
        lines = AsyncStream { captured = $0 }
        continuation = captured
    }

    func start() throws {}

    func send(_ line: String) throws {
        let object = try decode(line)
        let method = object["method"] as! String
        lock.lock()
        methods.append(method)
        objects.append(object)
        lock.unlock()
        guard let id = object["id"] as? Int else { return }

        switch method {
        case "initialize":
            continuation.yield(#"{"id":\#(id),"result":{}}"#)
        case "account/rateLimits/read":
            continuation.yield(#"{"method":"account/rateLimits/updated","params":{}}"#)
            continuation.yield(#"{"id":\#(id),"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":25}}}}"#)
        case "account/usage/read":
            continuation.yield(#"{"id":\#(id),"result":{"summary":{"lifetimeTokens":1234}}}"#)
        case "thread/list":
            continuation.yield(#"{"id":\#(id),"result":{"data":[{"id":"root","name":"Root","cwd":"/tmp/project","createdAt":1,"updatedAt":2,"path":"/tmp/root.jsonl"},{"id":"child","cwd":"/tmp/project","parentThreadId":"root","createdAt":1,"updatedAt":2,"path":"/tmp/child.jsonl"}],"nextCursor":null}}"#)
        case "thread/start":
            continuation.yield(#"{"id":\#(id),"result":{"thread":{"id":"new-thread"}}}"#)
        case "thread/name/set":
            if rejectName {
                continuation.yield(#"{"id":\#(id),"error":{"code":-32000,"message":"name rejected"}}"#)
            } else {
                continuation.yield(#"{"id":\#(id),"result":{}}"#)
            }
        default:
            break
        }
    }

    func stop() { continuation.finish() }
}

private final class ErrorTestTransport: AppServerTransport {
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
        let object = try decode(line)
        guard let id = object["id"] as? Int else { return }
        if object["method"] as? String == "initialize" {
            continuation.yield(#"{"id":\#(id),"result":{}}"#)
        } else {
            let errorObject: [String: Any] = ["code": code, "message": message]
            let responseObject: [String: Any] = [
                "id": id,
                "error": errorObject,
            ]
            let data = try JSONSerialization.data(withJSONObject: responseObject)
            continuation.yield(String(decoding: data, as: UTF8.self))
        }
    }

    func stop() { continuation.finish() }
}

private final class ReconnectTestTransport: AppServerTransport {
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
        let object = try decode(line)
        guard let id = object["id"] as? Int else { return }
        if object["method"] as? String == "initialize" {
            continuation.yield(#"{"id":\#(id),"result":{}}"#)
        } else if object["method"] as? String == "account/rateLimits/read" {
            let used = generation == 1 ? 25 : 40
            continuation.yield(#"{"id":\#(id),"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":\#(used)}}}}"#)
            if generation == 1 { continuation.finish() }
        }
    }

    func stop() { continuation.finish() }
}

private func decode(_ line: String) throws -> [String: Any] {
    let data = Data(line.utf8)
    return try JSONSerialization.jsonObject(with: data) as! [String: Any]
}
