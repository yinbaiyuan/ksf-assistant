import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private func expect<T: Equatable>(_ actual: T, _ expected: T, _ message: String) throws {
    guard actual == expected else {
        throw TestFailure(description: "\(message): expected \(expected), got \(actual)")
    }
}

@main
private enum TaskActivityTestRunner {
    static func main() async {
        do {
            let observations = [
                CodexTaskObservation(id: "run", hostID: "local", runtimeStatus: .active),
                CodexTaskObservation(
                    id: "wait",
                    hostID: "remote",
                    runtimeStatus: .active,
                    activeFlags: [.waitingOnUserInput]
                ),
                CodexTaskObservation(
                    id: "plan",
                    hostID: "remote",
                    runtimeStatus: .idle,
                    hasPendingPlanImplementation: true
                ),
                CodexTaskObservation(
                    id: "child",
                    hostID: "local",
                    agentNickname: "helper",
                    runtimeStatus: .active
                ),
            ]
            let summary = TaskActivityClassifier.summarize(observations)
            try expect(summary.runningCount, 1, "running tasks")
            try expect(summary.waitingCount, 2, "waiting tasks")
            try expect(
                LocalTokenRefreshPolicy.shouldPoll(
                    for: TaskActivitySnapshot(
                        runningCount: 1,
                        availability: .available
                    )
                ),
                true,
                "running task enables local Token polling"
            )
            try expect(
                LocalTokenRefreshPolicy.shouldPoll(
                    for: TaskActivitySnapshot(
                        waitingCount: 1,
                        availability: .available
                    )
                ),
                false,
                "waiting-only activity lets local Token polling sleep"
            )
            try expect(
                LocalTokenRefreshPolicy.shouldPoll(
                    for: TaskActivitySnapshot(
                        runningCount: 1,
                        availability: .offline
                    )
                ),
                false,
                "unavailable task state stops local Token polling"
            )
            try expect(
                LocalTokenRefreshPolicy.activeIntervalNanoseconds,
                10_000_000_000,
                "active polling interval"
            )

            let first = Data(#"{"type":"one"}"#.utf8)
            let second = Data(#"{"type":"two"}"#.utf8)
            let bytes = DesktopIPCFrameEncoder.encode(first) + DesktopIPCFrameEncoder.encode(second)
            var decoder = DesktopIPCFrameDecoder(maxFrameBytes: 1_024)
            try expect(try decoder.append(bytes.prefix(2)), [], "partial header")
            try expect(try decoder.append(bytes.dropFirst(2)), [first, second], "coalesced frames")
            var limitedDecoder = DesktopIPCFrameDecoder(maxFrameBytes: 4)
            do {
                _ = try limitedDecoder.append(DesktopIPCFrameEncoder.encode(Data("12345".utf8)))
                throw TestFailure(description: "oversized IPC frame was accepted")
            } catch DesktopIPCFrameError.frameTooLarge(5) {
                // Expected.
            }

            let snapshotPayload = Data("""
            {
              "type":"broadcast",
              "method":"thread-stream-state-changed",
              "version":11,
              "sourceClientId":"owner-1",
              "params":{
                "conversationId":"task-1",
                "hostId":"remote-1",
                "change":{
                  "type":"snapshot",
                  "conversationState":{
                    "source":{"custom":"worktree"},
                    "threadRuntimeStatus":{"type":"active","activeFlags":["waitingOnApproval"]},
                    "requests":{"request-1":{"method":"requestUserInput","prompt":"must not be modeled"}},
                    "turnHistory":{"history":{"entitiesByKey":{"tail":{"items":[{"type":"planImplementation","isCompleted":false,"content":"must not be modeled"}]}}}}
                  }
                }
              }
            }
            """.utf8)
            let event = try DesktopIPCMessageDecoder.decode(snapshotPayload)
            guard case let .stateSnapshot(sourceClientID, observation) = event else {
                throw TestFailure(description: "expected a state snapshot, got \(event)")
            }
            try expect(sourceClientID, "owner-1", "snapshot owner")
            try expect(observation.id, "task-1", "snapshot task id")
            try expect(observation.hostID, "remote-1", "snapshot host id")
            try expect(observation.activeFlags, [.waitingOnApproval], "snapshot active flags")
            try expect(observation.pendingRequestMethods, ["requestUserInput"], "snapshot requests")
            try expect(observation.hasPendingPlanImplementation, true, "snapshot plan confirmation")

            let subAgentPayload = Data("""
            {
              "type":"broadcast",
              "method":"thread-stream-state-changed",
              "version":11,
              "sourceClientId":"owner-2",
              "params":{
                "conversationId":"child-1",
                "hostId":"local",
                "change":{
                  "type":"snapshot",
                  "conversationState":{
                    "source":{"subAgent":{"kind":"delegate"}},
                    "threadRuntimeStatus":{"type":"active","activeFlags":[]}
                  }
                }
              }
            }
            """.utf8)
            guard case let .stateSnapshot(_, child) = try DesktopIPCMessageDecoder.decode(subAgentPayload) else {
                throw TestFailure(description: "expected a child snapshot")
            }
            try expect(child.sourceKind, "subAgent", "sub-agent source")

            let patchPayload = Data("""
            {
              "type":"broadcast",
              "method":"thread-stream-state-changed",
              "version":11,
              "sourceClientId":"owner-1",
              "params":{
                "conversationId":"task-1",
                "hostId":"remote-1",
                "change":{"type":"patches","patches":[{"op":"add","path":["requests","request-2"],"value":{"content":"ignored"}}]}
              }
            }
            """.utf8)
            guard case let .statePatches(_, taskID, hostID, _, requiresSnapshot) = try DesktopIPCMessageDecoder.decode(patchPayload) else {
                throw TestFailure(description: "expected a patch event")
            }
            try expect(taskID, "task-1", "patch task id")
            try expect(hostID, "remote-1", "patch host id")
            try expect(requiresSnapshot, true, "request patch must re-snapshot")

            let runtimePatchPayload = Data("""
            {
              "type":"broadcast",
              "method":"thread-stream-state-changed",
              "version":11,
              "sourceClientId":"owner-1",
              "params":{
                "conversationId":"task-1",
                "hostId":"remote-1",
                "change":{"type":"patches","patches":[{"op":"replace","path":["threadRuntimeStatus","type"],"value":"idle"}]}
              }
            }
            """.utf8)
            guard case let .statePatches(_, _, _, runtimePatch, runtimeNeedsSnapshot) = try DesktopIPCMessageDecoder.decode(runtimePatchPayload) else {
                throw TestFailure(description: "expected a runtime patch event")
            }
            try expect(runtimePatch?.status, .idle, "runtime patch status")
            try expect(runtimeNeedsSnapshot, false, "ordinary runtime patch applies directly")

            try await testIPCHandshakeAndLifecycle()
            try await testCandidateDiscoveryWithoutWindowFollowing()
            try await testVisibleTaskOwnerMigration()

            print("PASS task classification, IPC framing, minimal decoding, handshake, lifecycle, candidate discovery, and owner migration")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }

    private static func testIPCHandshakeAndLifecycle() async throws {
        let transport = FakeDesktopIPCTransport()
        let client = CodexDesktopTaskActivityClient(
            transportFactory: { transport },
            desktopIsRunning: { true },
            now: { Date(timeIntervalSince1970: 100) }
        )
        var updates = client.updates.makeAsyncIterator()

        await client.start()
        try expect((await updates.next())?.availability, .loading, "initial loading state")
        try expect((await updates.next())?.availability, .loading, "connection loading state")

        let initialize = try await transport.waitForSentMessage(method: "initialize")
        let initializeID = initialize["requestId"] as? String ?? ""
        transport.emit([
            "type": "response",
            "requestId": initializeID,
            "result": ["clientId": "usage-client"],
        ])
        try expect((await updates.next())?.availability, .available, "initialized availability")

        transport.emit([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "sourceClientId": "window-1",
            "params": ["conversationId": "task-live", "hostId": "remote", "following": true] as [String: Any],
        ])
        let discovery = try await transport.waitForSentMessage(method: "thread-owner-discovery")
        let discoveryID = discovery["requestId"] as? String ?? ""
        transport.emit([
            "type": "response",
            "requestId": discoveryID,
            "result": ["handledByClientId": "owner-1"],
        ])
        _ = try await transport.waitForSentMessage(
            method: "thread-stream-following-changed",
            following: true
        )

        let liveSnapshot: [String: Any] = [
            "type": "broadcast",
            "method": "thread-stream-state-changed",
            "version": 11,
            "sourceClientId": "owner-1",
            "params": [
                "conversationId": "task-live",
                "hostId": "remote",
                "change": [
                    "type": "snapshot",
                    "conversationState": [
                        "source": "appServer",
                        "threadRuntimeStatus": ["type": "active", "activeFlags": [String]()] as [String: Any],
                    ] as [String: Any],
                ] as [String: Any],
            ] as [String: Any],
        ]
        transport.emit(liveSnapshot)
        let running = await updates.next()
        try expect(running?.runningCount, 1, "live running count")
        try expect(running?.waitingCount, 0, "live waiting count")

        await client.refresh()
        _ = try await transport.waitForSentMessage(
            method: "thread-stream-following-changed",
            following: true,
            minimumMatches: 2
        )
        transport.emit(liveSnapshot)
        let refreshed = await updates.next()
        try expect(refreshed?.availability, .available, "refresh keeps available state")
        try expect(refreshed?.runningCount, 1, "refresh keeps the last running count")
        try expect(refreshed?.waitingCount, 0, "refresh keeps the last waiting count")

        transport.emit([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "sourceClientId": "window-2",
            "params": ["conversationId": "task-live", "hostId": "remote", "following": true] as [String: Any],
        ])
        transport.emit([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "sourceClientId": "window-1",
            "params": ["conversationId": "task-live", "hostId": "remote", "following": false] as [String: Any],
        ])
        transport.emit([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "sourceClientId": "window-2",
            "params": ["conversationId": "task-live", "hostId": "remote", "following": false] as [String: Any],
        ])
        let retained = await updates.next()
        try expect(retained?.runningCount, 1, "unfollow keeps an active background task")
        try expect(retained?.waitingCount, 0, "unfollow keeps the background task classification")

        let idleSnapshot: [String: Any] = [
            "type": "broadcast",
            "method": "thread-stream-state-changed",
            "version": 11,
            "sourceClientId": "owner-1",
            "params": [
                "conversationId": "task-live",
                "hostId": "remote",
                "change": [
                    "type": "snapshot",
                    "conversationState": [
                        "source": "appServer",
                        "threadRuntimeStatus": ["type": "idle", "activeFlags": [String]()] as [String: Any],
                    ] as [String: Any],
                ] as [String: Any],
            ] as [String: Any],
        ]
        transport.emit(idleSnapshot)
        let removed = await updates.next()
        try expect(removed?.runningCount, 0, "idle removes an unfollowed task")
        try expect(removed?.waitingCount, 0, "idle clears the unfollowed task classification")
        await client.stop()
    }

    private static func testCandidateDiscoveryWithoutWindowFollowing() async throws {
        let transport = FakeDesktopIPCTransport()
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
        try expect(
            transport.sentMessageCount(method: "thread-owner-discovery"),
            0,
            "candidate discovery waits for IPC initialization"
        )

        let initialize = try await transport.waitForSentMessage(method: "initialize")
        let initializeID = initialize["requestId"] as? String ?? ""
        transport.emit([
            "type": "response",
            "requestId": initializeID,
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
            let discoveryID = discovery["requestId"] as? String ?? ""
            transport.emit([
                "type": "response",
                "requestId": discoveryID,
                "result": ["handledByClientId": ownerID],
            ])
            _ = try await transport.waitForSentMessage(
                method: "thread-stream-following-changed",
                following: true,
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
        try expect(latest?.runningCount, 2, "two candidate tasks run without window following")
        try expect(latest?.observations.count, 2, "both candidate task identities are observed")
        await client.stop()
    }

    private static func testVisibleTaskOwnerMigration() async throws {
        let transport = FakeDesktopIPCTransport()
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
            following: true,
            taskID: "plan-task"
        )
        transport.emit(taskSnapshot(
            taskID: "plan-task",
            ownerID: "owner-old",
            runtimeStatus: "idle"
        ))
        let idle = await updates.next()
        try expect(idle?.runningCount, 0, "old owner starts idle")

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
            minimumMatches: discoveryCount + 1
        )
        let followingCount = transport.sentMessageCount(method: "thread-stream-following-changed")
        transport.emit([
            "type": "response",
            "requestId": migratedDiscovery["requestId"] as? String ?? "",
            "result": ["handledByClientId": "owner-new"],
        ])
        _ = try await transport.waitForSentMessage(
            method: "thread-stream-following-changed",
            following: true,
            taskID: "plan-task",
            minimumMatches: followingCount + 1
        )

        transport.emit(taskSnapshot(
            taskID: "plan-task",
            ownerID: "owner-new",
            runtimeStatus: "active"
        ))
        let running = await updates.next()
        try expect(running?.runningCount, 1, "new owner reports plan writing as running")
        try expect(running?.waitingCount, 0, "plan writing is not waiting")

        transport.emit(taskSnapshot(
            taskID: "plan-task",
            ownerID: "owner-new",
            runtimeStatus: "idle",
            pendingPlan: true
        ))
        let waiting = await updates.next()
        try expect(waiting?.runningCount, 0, "completed plan stops running")
        try expect(waiting?.waitingCount, 1, "completed plan waits for user handling")
        try expect(
            waiting?.observations.first.flatMap(TaskActivityClassifier.waitingReason(for:)),
            .planConfirmation,
            "completed plan waiting reason"
        )
        await client.stop()
    }

    private static func taskSnapshot(
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

private final class FakeDesktopIPCTransport: DesktopIPCTransport, @unchecked Sendable {
    let incoming: AsyncStream<Data>
    private let continuation: AsyncStream<Data>.Continuation
    private let lock = NSLock()
    private var sentFrames: [Data] = []

    init() {
        var capturedContinuation: AsyncStream<Data>.Continuation!
        incoming = AsyncStream { capturedContinuation = $0 }
        continuation = capturedContinuation
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
        following: Bool? = nil,
        taskID: String? = nil,
        minimumMatches: Int = 1
    ) async throws -> [String: Any] {
        for _ in 0..<200 {
            let messages = sentMessages()
            let matches = messages.filter { message in
                guard message["method"] as? String == method else { return false }
                let params = message["params"] as? [String: Any]
                if let following, params?["following"] as? Bool != following { return false }
                if let taskID, params?["conversationId"] as? String != taskID { return false }
                return true
            }
            if matches.count >= minimumMatches, let match = matches.last {
                return match
            }
            try await Task.sleep(nanoseconds: 5_000_000)
        }
        throw TestFailure(description: "timed out waiting for IPC method \(method)")
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
