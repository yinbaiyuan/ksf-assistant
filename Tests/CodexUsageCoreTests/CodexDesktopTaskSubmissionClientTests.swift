import Foundation
import XCTest
@testable import CodexUsageCore

final class CodexDesktopTaskSubmissionClientTests: XCTestCase {
    func testDesktopOwnsInitialTurnAfterVisibleTaskIsOpened() async throws {
        let transport = SubmissionTestTransport()
        let client = CodexDesktopTaskSubmissionClient { transport }
        let opened = await MainActor.run { OpenProbe() }

        try await client.submitInitialTurn(
            threadID: "thread-123",
            cwd: "/Users/example/KSF",
            prompt: "加载项目上下文后等待。",
            openTask: { @MainActor in
                opened.markOpened()
                transport.follow(threadID: "thread-123", hostID: "local")
            }
        )

        let wasOpened = await MainActor.run { opened.wasOpened }
        XCTAssertTrue(wasOpened)
        XCTAssertEqual(
            transport.sentMethods,
            [
                "initialize",
                "thread-follower-start-turn",
            ]
        )
        let start = try XCTUnwrap(transport.payload(for: "thread-follower-start-turn"))
        XCTAssertEqual(start["version"] as? Int, 2)
        XCTAssertEqual(start["targetClientId"] as? String, "desktop-window")
        let params = try XCTUnwrap(start["params"] as? [String: Any])
        XCTAssertEqual(params["conversationId"] as? String, "thread-123")
        let turnStart = try XCTUnwrap(params["turnStart"] as? [String: Any])
        let request = try XCTUnwrap(turnStart["request"] as? [String: Any])
        XCTAssertEqual(request["threadId"] as? String, "thread-123")
        XCTAssertEqual(request["cwd"] as? String, "/Users/example/KSF")
        let input = try XCTUnwrap(request["input"] as? [[String: Any]])
        XCTAssertEqual(input.first?["text"] as? String, "加载项目上下文后等待。")
        XCTAssertNotNil(input.first?["text_elements"] as? [Any])
        let context = try XCTUnwrap(turnStart["context"] as? [String: Any])
        XCTAssertEqual(context["inheritThreadSettings"] as? Bool, true)
    }

    func testDesktopTurnRejectionIsReported() async {
        let transport = SubmissionTestTransport(rejectTurn: true)
        let client = CodexDesktopTaskSubmissionClient(
            transportFactory: { transport },
            ownerRetryDelayNanoseconds: 0
        )

        do {
            try await client.submitInitialTurn(
                threadID: "thread-123",
                cwd: "/Users/example/KSF",
                prompt: "等待下一步指令。",
                openTask: { @MainActor in
                    transport.follow(threadID: "thread-123", hostID: "local")
                }
            )
            XCTFail("Expected desktop rejection")
        } catch let error as CodexDesktopTaskSubmissionError {
            guard case let .submissionRejected(message) = error else {
                return XCTFail("Unexpected error: \(error)")
            }
            XCTAssertTrue(message.contains("turn rejected"))
        } catch {
            XCTFail("Unexpected error: \(error)")
        }
    }

    func testDesktopTurnWaitsForFollowedWindowToBecomeOwner() async throws {
        let transport = SubmissionTestTransport(notReadyAttempts: 3)
        let client = CodexDesktopTaskSubmissionClient(
            transportFactory: { transport },
            ownerRetryDelayNanoseconds: 0
        )

        try await client.submitInitialTurn(
            threadID: "thread-123",
            cwd: "/Users/example/KSF",
            prompt: "等待下一步指令。",
            openTask: { @MainActor in
                transport.follow(threadID: "thread-123", hostID: "local")
            }
        )

        XCTAssertEqual(transport.startTurnRequestCount, 4)
        XCTAssertTrue(transport.startTurnTargets.allSatisfy { $0 == "desktop-window" })
    }
}

@MainActor
private final class OpenProbe {
    private(set) var wasOpened = false

    func markOpened() {
        wasOpened = true
    }
}

private final class SubmissionTestTransport: DesktopIPCTransport, @unchecked Sendable {
    let incoming: AsyncStream<Data>
    private let continuation: AsyncStream<Data>.Continuation
    private let lock = NSLock()
    private let rejectTurn: Bool
    private let notReadyAttempts: Int
    private var startTurnAttempts = 0
    private var payloads: [[String: Any]] = []

    init(rejectTurn: Bool = false, notReadyAttempts: Int = 0) {
        self.rejectTurn = rejectTurn
        self.notReadyAttempts = notReadyAttempts
        var captured: AsyncStream<Data>.Continuation!
        incoming = AsyncStream { captured = $0 }
        continuation = captured
    }

    var sentMethods: [String] {
        lock.lock()
        defer { lock.unlock() }
        return payloads.compactMap { $0["method"] as? String ?? $0["type"] as? String }
    }

    func payload(for method: String) -> [String: Any]? {
        lock.lock()
        defer { lock.unlock() }
        return payloads.first { $0["method"] as? String == method }
    }

    var startTurnRequestCount: Int {
        lock.lock()
        defer { lock.unlock() }
        return payloads.filter { $0["method"] as? String == "thread-follower-start-turn" }.count
    }

    var startTurnTargets: [String] {
        lock.lock()
        defer { lock.unlock() }
        return payloads.compactMap { object in
            guard object["method"] as? String == "thread-follower-start-turn" else { return nil }
            return object["targetClientId"] as? String
        }
    }

    func start() throws {}

    func send(_ data: Data) throws {
        var decoder = DesktopIPCFrameDecoder()
        let frame = try XCTUnwrap(try decoder.append(data).first)
        let object = try XCTUnwrap(
            try JSONSerialization.jsonObject(with: frame) as? [String: Any]
        )
        lock.lock()
        payloads.append(object)
        lock.unlock()

        guard let method = object["method"] as? String,
              let requestID = object["requestId"] as? String else { return }

        switch method {
        case "initialize":
            respond([
                "type": "response",
                "requestId": requestID,
                "resultType": "success",
                "result": ["clientId": "usage-bar-client"],
            ])
        case "thread-follower-start-turn":
            lock.lock()
            startTurnAttempts += 1
            let isNotReady = startTurnAttempts <= notReadyAttempts
            lock.unlock()
            if isNotReady {
                respond([
                    "type": "response",
                    "requestId": requestID,
                    "resultType": "error",
                    "error": "no-client-found",
                ])
            } else if rejectTurn {
                respond([
                    "type": "response",
                    "requestId": requestID,
                    "resultType": "error",
                    "error": "turn rejected",
                ])
            } else {
                let result: [String: Any] = ["method": method, "result": ["ok": true]]
                respond([
                    "type": "response",
                    "requestId": requestID,
                    "resultType": "success",
                    "result": result,
                ])
            }
        default:
            break
        }
    }

    func stop() {
        continuation.finish()
    }

    func follow(threadID: String, hostID: String) {
        let params: [String: Any] = [
            "conversationId": threadID,
            "hostId": hostID,
            "following": true,
        ]
        respond([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "sourceClientId": "desktop-window",
            "params": params,
        ])
    }

    private func respond(_ object: [String: Any]) {
        let data = try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        continuation.yield(DesktopIPCFrameEncoder.encode(data))
    }
}
