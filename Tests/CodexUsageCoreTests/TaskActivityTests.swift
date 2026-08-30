import Foundation
import XCTest
@testable import CodexUsageCore

final class TaskActivityTests: XCTestCase {
    func testLocalTokenPollingRunsOnlyForConfirmedRunningTasks() {
        XCTAssertTrue(
            LocalTokenRefreshPolicy.shouldPoll(
                for: TaskActivitySnapshot(runningCount: 1, availability: .available)
            )
        )
        XCTAssertFalse(
            LocalTokenRefreshPolicy.shouldPoll(
                for: TaskActivitySnapshot(waitingCount: 1, availability: .available)
            )
        )
        XCTAssertFalse(
            LocalTokenRefreshPolicy.shouldPoll(
                for: TaskActivitySnapshot(runningCount: 1, availability: .offline)
            )
        )
        XCTAssertEqual(LocalTokenRefreshPolicy.activeIntervalNanoseconds, 10_000_000_000)
    }

    func testWaitingStatesWinOverRunningAndTasksAreDeduplicated() {
        let observations = [
            CodexTaskObservation(
                id: "running",
                hostID: "local",
                runtimeStatus: .active,
                activeFlags: []
            ),
            CodexTaskObservation(
                id: "question",
                hostID: "local",
                runtimeStatus: .active,
                activeFlags: [.waitingOnUserInput]
            ),
            CodexTaskObservation(
                id: "approval",
                hostID: "remote-a",
                runtimeStatus: .active,
                activeFlags: [.waitingOnApproval]
            ),
            CodexTaskObservation(
                id: "plan",
                hostID: "remote-a",
                runtimeStatus: .idle,
                hasPendingPlanImplementation: true
            ),
            CodexTaskObservation(
                id: "running",
                hostID: "local",
                runtimeStatus: .active,
                activeFlags: []
            ),
        ]

        let summary = TaskActivityClassifier.summarize(observations)
        XCTAssertEqual(summary.runningCount, 1)
        XCTAssertEqual(summary.waitingCount, 3)
    }

    func testPendingUserRequestsWaitWhileInactiveAndInternalTasksAreExcluded() {
        let observations = [
            CodexTaskObservation(
                id: "option",
                hostID: "local",
                runtimeStatus: .idle,
                pendingRequestMethods: ["item/tool/requestOptionPicker"]
            ),
            CodexTaskObservation(
                id: "subagent",
                hostID: "local",
                agentNickname: "helper",
                runtimeStatus: .active
            ),
            CodexTaskObservation(
                id: "error",
                hostID: "local",
                runtimeStatus: .systemError
            ),
            CodexTaskObservation(
                id: "not-loaded",
                hostID: "local",
                runtimeStatus: .notLoaded
            ),
        ]

        let summary = TaskActivityClassifier.summarize(observations)
        XCTAssertEqual(summary.runningCount, 0)
        XCTAssertEqual(summary.waitingCount, 1)
    }

    func testLengthPrefixedFramesHandleSplitsAndCoalescing() throws {
        let first = Data(#"{"type":"one"}"#.utf8)
        let second = Data(#"{"type":"two"}"#.utf8)
        let bytes = DesktopIPCFrameEncoder.encode(first) + DesktopIPCFrameEncoder.encode(second)
        var decoder = DesktopIPCFrameDecoder(maxFrameBytes: 1_024)

        XCTAssertEqual(try decoder.append(bytes.prefix(3)), [])
        XCTAssertEqual(try decoder.append(bytes.dropFirst(3).prefix(7)), [])
        XCTAssertEqual(
            try decoder.append(bytes.dropFirst(10)),
            [first, second]
        )
    }

    func testLengthPrefixedFrameRejectsOversizedPayload() throws {
        var decoder = DesktopIPCFrameDecoder(maxFrameBytes: 4)
        let oversized = DesktopIPCFrameEncoder.encode(Data("12345".utf8))
        XCTAssertThrowsError(try decoder.append(oversized)) { error in
            XCTAssertEqual(error as? DesktopIPCFrameError, .frameTooLarge(5))
        }
    }

    func testMinimalSnapshotDecoderFindsWaitingAndPlanState() throws {
        let payload = Data("""
        {
          "type":"broadcast",
          "method":"thread-stream-state-changed",
          "version":11,
          "sourceClientId":"owner",
          "params":{
            "conversationId":"task",
            "hostId":"remote",
            "change":{
              "type":"snapshot",
              "conversationState":{
                "source":{"custom":"remote"},
                "threadRuntimeStatus":{"type":"active","activeFlags":["waitingOnUserInput"]},
                "requests":{"r":{"method":"requestUserInput","content":"ignored"}},
                "turns":[{"items":[{"type":"planImplementation","isCompleted":false,"content":"ignored"}]}]
              }
            }
          }
        }
        """.utf8)

        guard case let .stateSnapshot(_, observation) = try DesktopIPCMessageDecoder.decode(payload) else {
            return XCTFail("Expected state snapshot")
        }
        XCTAssertEqual(observation.id, "task")
        XCTAssertEqual(observation.hostID, "remote")
        XCTAssertEqual(observation.activeFlags, [.waitingOnUserInput])
        XCTAssertEqual(observation.pendingRequestMethods, ["requestUserInput"])
        XCTAssertTrue(observation.hasPendingPlanImplementation)
    }

    func testRuntimePatchIsDirectAndRequestPatchRequiresSnapshot() throws {
        func payload(path: String, value: String) -> Data {
            Data("""
            {
              "type":"broadcast",
              "method":"thread-stream-state-changed",
              "version":11,
              "sourceClientId":"owner",
              "params":{
                "conversationId":"task",
                "hostId":"local",
                "change":{"type":"patches","patches":[{"op":"replace","path":[\(path)],"value":\(value)}]}
              }
            }
            """.utf8)
        }

        guard case let .statePatches(_, _, _, runtimePatch, runtimeResnapshot) = try DesktopIPCMessageDecoder.decode(
            payload(path: "\"threadRuntimeStatus\",\"type\"", value: "\"idle\"")
        ) else {
            return XCTFail("Expected runtime patch")
        }
        XCTAssertEqual(runtimePatch?.status, .idle)
        XCTAssertFalse(runtimeResnapshot)

        guard case let .statePatches(_, _, _, _, requestResnapshot) = try DesktopIPCMessageDecoder.decode(
            payload(path: "\"requests\",\"request-id\"", value: "{}")
        ) else {
            return XCTFail("Expected request patch")
        }
        XCTAssertTrue(requestResnapshot)
    }

    func testUnsupportedPrivateProtocolVersionFailsClosed() {
        let payload = Data("""
        {"type":"broadcast","method":"thread-stream-state-changed","version":12,"sourceClientId":"owner","params":{}}
        """.utf8)

        XCTAssertThrowsError(try DesktopIPCMessageDecoder.decode(payload)) { error in
            XCTAssertEqual(
                error as? DesktopIPCProtocolError,
                .unsupportedVersion(method: "thread-stream-state-changed", received: 12)
            )
        }
    }

    func testCandidateTasksAreDiscoveredWithoutWindowFollowing() async throws {
        let transport = ActivityClientTestTransport()
        let client = CodexDesktopTaskActivityClient(
            transportFactory: { transport },
            desktopIsRunning: { true },
            now: { Date(timeIntervalSince1970: 200) }
        )
        var updates = client.updates.makeAsyncIterator()

        await client.start()
        _ = await updates.next()
        _ = await updates.next()
        await client.reconcileLocalTaskCandidates(["task-current", "task-background"])
        await client.refresh()
        XCTAssertEqual(transport.sentMessageCount(method: "thread-owner-discovery"), 0)

        let initialize = try await transport.waitForSentMessage(method: "initialize")
        transport.emit([
            "type": "response",
            "requestId": initialize["requestId"] as? String ?? "",
            "result": ["clientId": "usage-client"],
        ])
        _ = await updates.next()

        for (taskID, ownerID) in [
            ("task-current", "owner-current"),
            ("task-background", "owner-background"),
        ] {
            let discovery = try await transport.waitForSentMessage(
                method: "thread-owner-discovery",
                taskID: taskID
            )
            transport.emit([
                "type": "response",
                "requestId": discovery["requestId"] as? String ?? "",
                "result": ["handledByClientId": ownerID],
            ])
            _ = try await transport.waitForSentMessage(
                method: "thread-stream-following-changed",
                taskID: taskID
            )
            transport.emit([
                "type": "broadcast",
                "method": "thread-stream-state-changed",
                "version": 11,
                "sourceClientId": ownerID,
                "params": [
                    "conversationId": taskID,
                    "hostId": "local",
                    "change": [
                        "type": "snapshot",
                        "conversationState": [
                            "source": "vscode",
                            "threadRuntimeStatus": ["type": "active", "activeFlags": [String]()] as [String: Any],
                        ] as [String: Any],
                    ] as [String: Any],
                ] as [String: Any],
            ])
        }

        var latest: TaskActivitySnapshot?
        for _ in 0..<2 { latest = await updates.next() }
        XCTAssertEqual(latest?.runningCount, 2)
        XCTAssertEqual(latest?.observations.count, 2)
        await client.stop()
    }

    func testVisibleTaskOwnerMigrationPreservesRunningThenPlanWaitingState() async throws {
        let transport = ActivityClientTestTransport()
        let client = CodexDesktopTaskActivityClient(
            transportFactory: { transport },
            desktopIsRunning: { true },
            now: { Date(timeIntervalSince1970: 300) }
        )
        var updates = client.updates.makeAsyncIterator()

        await client.start()
        _ = await updates.next()
        _ = await updates.next()
        await client.reconcileLocalTaskCandidates(["plan-task"])

        let initialize = try await transport.waitForSentMessage(method: "initialize")
        transport.emit([
            "type": "response",
            "requestId": initialize["requestId"] as? String ?? "",
            "result": ["clientId": "usage-client"],
        ])
        _ = await updates.next()

        let firstDiscovery = try await transport.waitForSentMessage(
            method: "thread-owner-discovery",
            taskID: "plan-task"
        )
        transport.emit([
            "type": "response",
            "requestId": firstDiscovery["requestId"] as? String ?? "",
            "result": ["handledByClientId": "owner-old"],
        ])
        _ = try await transport.waitForSentMessage(
            method: "thread-stream-following-changed",
            taskID: "plan-task"
        )
        transport.emit(taskSnapshot(
            taskID: "plan-task",
            ownerID: "owner-old",
            runtimeStatus: "idle"
        ))
        let idle = await updates.next()
        XCTAssertEqual(idle?.runningCount, 0)
        XCTAssertEqual(idle?.waitingCount, 0)

        let discoveryCount = transport.sentMessageCount(method: "thread-owner-discovery")
        transport.emit([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "sourceClientId": "visible-window",
            "params": [
                "conversationId": "plan-task",
                "hostId": "local",
                "following": true,
            ] as [String: Any],
        ])
        let migratedDiscovery = try await transport.waitForSentMessage(
            method: "thread-owner-discovery",
            taskID: "plan-task",
            afterCount: discoveryCount
        )
        let followingCount = transport.sentMessageCount(method: "thread-stream-following-changed")
        transport.emit([
            "type": "response",
            "requestId": migratedDiscovery["requestId"] as? String ?? "",
            "result": ["handledByClientId": "owner-new"],
        ])
        _ = try await transport.waitForSentMessage(
            method: "thread-stream-following-changed",
            taskID: "plan-task",
            afterCount: followingCount
        )

        transport.emit(taskSnapshot(
            taskID: "plan-task",
            ownerID: "owner-new",
            runtimeStatus: "active"
        ))
        let running = await updates.next()
        XCTAssertEqual(running?.runningCount, 1)
        XCTAssertEqual(running?.waitingCount, 0)

        transport.emit(taskSnapshot(
            taskID: "plan-task",
            ownerID: "owner-new",
            runtimeStatus: "idle",
            pendingPlan: true
        ))
        let waiting = await updates.next()
        XCTAssertEqual(waiting?.runningCount, 0)
        XCTAssertEqual(waiting?.waitingCount, 1)
        XCTAssertEqual(
            waiting?.observations.first.flatMap(TaskActivityClassifier.waitingReason(for:)),
            .planConfirmation
        )
        await client.stop()
    }

    private func taskSnapshot(
        taskID: String,
        ownerID: String,
        runtimeStatus: String,
        pendingPlan: Bool = false
    ) -> [String: Any] {
        var conversationState: [String: Any] = [
            "source": "vscode",
            "threadRuntimeStatus": [
                "type": runtimeStatus,
                "activeFlags": [String](),
            ] as [String: Any],
        ]
        if pendingPlan {
            conversationState["turnHistory"] = [
                "kind": "canonical",
                "history": [
                    "entitiesByKey": [
                        "turn:latest": [
                            "items": [[
                                "type": "planImplementation",
                                "isCompleted": false,
                            ] as [String: Any]],
                        ] as [String: Any],
                    ],
                    "islands": [[
                        "entries": [["value": "turn:latest"]],
                    ]],
                ] as [String: Any],
            ] as [String: Any]
        }
        return [
            "type": "broadcast",
            "method": "thread-stream-state-changed",
            "version": 11,
            "sourceClientId": ownerID,
            "params": [
                "conversationId": taskID,
                "hostId": "local",
                "change": [
                    "type": "snapshot",
                    "conversationState": conversationState,
                ] as [String: Any],
            ] as [String: Any],
        ]
    }
}

private struct ActivityClientTestFailure: Error {}

private final class ActivityClientTestTransport: DesktopIPCTransport, @unchecked Sendable {
    let incoming: AsyncStream<Data>
    private let continuation: AsyncStream<Data>.Continuation
    private let lock = NSLock()
    private var sentFrames: [Data] = []

    init() {
        var captured: AsyncStream<Data>.Continuation!
        incoming = AsyncStream { captured = $0 }
        continuation = captured
    }

    func start() throws {}

    func send(_ data: Data) throws {
        lock.lock()
        sentFrames.append(data)
        lock.unlock()
    }

    func stop() {
        continuation.finish()
    }

    func emit(_ object: [String: Any]) {
        let payload = try! JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
        continuation.yield(DesktopIPCFrameEncoder.encode(payload))
    }

    func waitForSentMessage(
        method: String,
        taskID: String? = nil,
        afterCount: Int = 0
    ) async throws -> [String: Any] {
        for _ in 0..<200 {
            let matches = sentMessages().filter { message in
                guard message["method"] as? String == method else { return false }
                guard let taskID else { return true }
                let params = message["params"] as? [String: Any]
                return params?["conversationId"] as? String == taskID
            }
            if matches.count > afterCount, let match = matches.last { return match }
            try await Task.sleep(nanoseconds: 5_000_000)
        }
        throw ActivityClientTestFailure()
    }

    func sentMessageCount(method: String) -> Int {
        sentMessages().filter { $0["method"] as? String == method }.count
    }

    private func sentMessages() -> [[String: Any]] {
        lock.lock()
        let frames = sentFrames
        lock.unlock()
        return frames.compactMap { frame in
            var decoder = DesktopIPCFrameDecoder()
            guard let payload = try? decoder.append(frame).first else { return nil }
            return (try? JSONSerialization.jsonObject(with: payload)) as? [String: Any]
        }
    }
}
