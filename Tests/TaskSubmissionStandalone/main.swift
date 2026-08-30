import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private final class FakeSubmissionTransport: DesktopIPCTransport, @unchecked Sendable {
    let incoming: AsyncStream<Data>
    private let continuation: AsyncStream<Data>.Continuation
    private let lock = NSLock()
    private var payloads: [[String: Any]] = []
    private var startTurnAttempts = 0

    init() {
        var captured: AsyncStream<Data>.Continuation!
        incoming = AsyncStream { captured = $0 }
        continuation = captured
    }

    var methods: [String] {
        lock.lock()
        defer { lock.unlock() }
        return payloads.compactMap { $0["method"] as? String ?? $0["type"] as? String }
    }

    func payload(for method: String) -> [String: Any]? {
        lock.lock()
        defer { lock.unlock() }
        return payloads.first { $0["method"] as? String == method }
    }

    func start() throws {}

    func send(_ data: Data) throws {
        var decoder = DesktopIPCFrameDecoder()
        guard let frame = try decoder.append(data).first,
              let object = try JSONSerialization.jsonObject(with: frame) as? [String: Any] else {
            throw TestFailure(description: "invalid framed request")
        }
        lock.lock()
        payloads.append(object)
        lock.unlock()

        guard let method = object["method"] as? String,
              let requestID = object["requestId"] as? String else { return }
        switch method {
        case "initialize":
            emit([
                "type": "response",
                "requestId": requestID,
                "resultType": "success",
                "result": ["clientId": "usage-bar"],
            ])
        case "thread-follower-start-turn":
            lock.lock()
            startTurnAttempts += 1
            let isReady = startTurnAttempts > 1
            lock.unlock()
            guard isReady else {
                emit([
                    "type": "response",
                    "requestId": requestID,
                    "resultType": "error",
                    "error": "no-client-found",
                ])
                return
            }
            let result: [String: Any] = ["method": method, "result": ["ok": true]]
            emit([
                "type": "response",
                "requestId": requestID,
                "resultType": "success",
                "result": result,
            ])
        default:
            break
        }
    }

    func stop() {
        continuation.finish()
    }

    func follow(threadID: String) {
        let params: [String: Any] = [
            "conversationId": threadID,
            "hostId": "local",
            "following": true,
        ]
        emit([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "sourceClientId": "desktop-window",
            "params": params,
        ])
    }

    private func emit(_ object: [String: Any]) {
        let data = try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        continuation.yield(DesktopIPCFrameEncoder.encode(data))
    }
}

@main
private enum TaskSubmissionTestRunner {
    @MainActor
    static func main() async {
        do {
            let transport = FakeSubmissionTransport()
            let client = CodexDesktopTaskSubmissionClient(
                transportFactory: { transport },
                ownerRetryDelayNanoseconds: 0
            )
            var opened = false
            try await client.submitInitialTurn(
                threadID: "thread-123",
                cwd: "/Users/example/KSF",
                prompt: "加载项目上下文后等待。",
                openTask: { @MainActor in
                    opened = true
                    transport.follow(threadID: "thread-123")
                }
            )
            guard opened else { throw TestFailure(description: "task was not opened") }
            guard transport.methods == [
                "initialize",
                "thread-follower-start-turn",
                "thread-follower-start-turn",
            ] else {
                throw TestFailure(description: "wrong IPC order: \(transport.methods)")
            }
            guard let start = transport.payload(for: "thread-follower-start-turn"),
                  start["version"] as? Int == 2,
                  start["targetClientId"] as? String == "desktop-window",
                  let params = start["params"] as? [String: Any],
                  let turnStart = params["turnStart"] as? [String: Any],
                  let request = turnStart["request"] as? [String: Any],
                  request["threadId"] as? String == "thread-123",
                  request["cwd"] as? String == "/Users/example/KSF",
                  let input = request["input"] as? [[String: Any]],
                  input.first?["text"] as? String == "加载项目上下文后等待。" else {
                throw TestFailure(description: "turn submission payload is incomplete")
            }
            print("PASS visible Codex Desktop owns and starts the initial turn")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
