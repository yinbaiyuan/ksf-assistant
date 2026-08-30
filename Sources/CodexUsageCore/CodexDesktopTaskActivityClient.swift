import Foundation

public enum DesktopIPCMessageEncoder {
    public static func initialize(requestID: String) throws -> Data {
        return try encode([
            "type": "request",
            "requestId": requestID,
            "method": "initialize",
            "params": ["clientType": "codex-usage-bar"],
        ])
    }

    public static func ownerDiscovery(
        requestID: String,
        taskID: String,
        hostID: String,
        sourceClientID: String? = nil
    ) throws -> Data {
        var object: [String: Any] = [
            "type": "request",
            "requestId": requestID,
            "method": "thread-owner-discovery",
            "version": 1,
            "params": ["conversationId": taskID, "hostId": hostID],
        ]
        if let sourceClientID { object["sourceClientId"] = sourceClientID }
        return try encode(object)
    }

    public static func startTurn(
        requestID: String,
        sourceClientID: String,
        targetClientID: String,
        taskID: String,
        cwd: String,
        prompt: String
    ) throws -> Data {
        let textInput: [String: Any] = [
            "type": "text",
            "text": prompt,
            "text_elements": [Any](),
        ]
        let request: [String: Any] = [
            "threadId": taskID,
            "input": [textInput],
            "cwd": cwd,
        ]
        let turnStart: [String: Any] = [
            "request": request,
            "context": ["inheritThreadSettings": true],
        ]
        let params: [String: Any] = [
            "conversationId": taskID,
            "turnStart": turnStart,
        ]
        return try encode([
            "type": "request",
            "requestId": requestID,
            "sourceClientId": sourceClientID,
            "targetClientId": targetClientID,
            "timeoutMs": 15_000,
            "method": "thread-follower-start-turn",
            "version": 2,
            "params": params,
        ])
    }

    public static func followingChanged(
        taskID: String,
        hostID: String,
        following: Bool,
        targetClientID: String
    ) throws -> Data {
        let params: [String: Any] = [
            "conversationId": taskID,
            "hostId": hostID,
            "following": following,
        ]
        return try encode([
            "type": "broadcast",
            "method": "thread-stream-following-changed",
            "version": 1,
            "targetClientIds": [targetClientID],
            "params": params,
        ])
    }

    public static func clientDiscoveryResponse(requestID: String) throws -> Data {
        try encode([
            "type": "client-discovery-response",
            "requestId": requestID,
            "response": ["canHandle": false],
        ])
    }

    private static func encode(_ object: [String: Any]) throws -> Data {
        try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    }
}

public actor CodexDesktopTaskActivityClient: CodexTaskActivityProviding {
    public nonisolated let updates: AsyncStream<TaskActivitySnapshot>

    private let continuation: AsyncStream<TaskActivitySnapshot>.Continuation
    private let transportFactory: @Sendable () throws -> DesktopIPCTransport
    private let desktopIsRunning: @Sendable () -> Bool
    private let now: @Sendable () -> Date

    private var transport: DesktopIPCTransport?
    private var readTask: Task<Void, Never>?
    private var retryTask: Task<Void, Never>?
    private var snapshotTasks: [TaskKey: Task<Void, Never>] = [:]
    private var frameDecoder = DesktopIPCFrameDecoder()
    // Desktop windows advertise what they currently display. Project candidates
    // are the separate source for loaded local tasks that can keep running offscreen.
    private var followedBy: [TaskKey: Set<String>] = [:]
    private var candidateKeys: Set<TaskKey> = []
    private var owners: [TaskKey: String] = [:]
    private var observations: [TaskKey: CodexTaskObservation] = [:]
    private var pendingOwnerRequests: [String: TaskKey] = [:]
    private var requestSequence = 0
    private var connectionGeneration = 0
    private var retryAttempt = 0
    private var started = false
    private var ready = false
    private var protocolBlocked = false

    public init(
        transportFactory: @escaping @Sendable () throws -> DesktopIPCTransport,
        desktopIsRunning: @escaping @Sendable () -> Bool,
        now: @escaping @Sendable () -> Date = { Date() }
    ) {
        var capturedContinuation: AsyncStream<TaskActivitySnapshot>.Continuation!
        updates = AsyncStream { capturedContinuation = $0 }
        continuation = capturedContinuation
        self.transportFactory = transportFactory
        self.desktopIsRunning = desktopIsRunning
        self.now = now
    }

    deinit {
        continuation.finish()
        readTask?.cancel()
        retryTask?.cancel()
        snapshotTasks.values.forEach { $0.cancel() }
        transport?.stop()
    }

    public func start() async {
        guard !started else { return }
        started = true
        continuation.yield(TaskActivitySnapshot(availability: .loading))
        connect(resetBackoff: true)
    }

    public func refresh() async {
        guard started else {
            await start()
            return
        }

        if transport != nil, !protocolBlocked {
            retryAttempt = 0
            retryTask?.cancel()
            retryTask = nil
            do {
                if ready {
                    try discoverCandidates(revalidatingOwners: true)
                    for key in owners.keys where !candidateKeys.contains(key) {
                        try requestSnapshot(for: key)
                    }
                }
            } catch {
                publishConnectionFailure()
                disconnectCurrent(sendUnfollow: false)
                scheduleReconnect()
            }
            return
        }

        connect(resetBackoff: true)
    }

    public func reconcileLocalTaskCandidates(_ threadIDs: Set<String>) async {
        let next = Set(threadIDs.compactMap { threadID -> TaskKey? in
            guard !threadID.isEmpty else { return nil }
            return TaskKey(taskID: threadID, hostID: "local")
        })
        let removed = candidateKeys.subtracting(next)
        candidateKeys = next

        do {
            for key in removed where followedBy[key]?.isEmpty != false {
                if let observation = observations[key],
                   !shouldRetain(key, observation: observation)
                {
                    try unsubscribeAndRemove(key)
                }
            }
            if ready { try discoverUnownedCandidates() }
        } catch {
            publishConnectionFailure()
            disconnectCurrent(sendUnfollow: false)
            scheduleReconnect()
        }
    }

    public func stop() async {
        guard started else { return }
        started = false
        retryTask?.cancel()
        retryTask = nil
        disconnectCurrent(sendUnfollow: true)
    }

    private func connect(resetBackoff: Bool) {
        guard started else { return }
        if resetBackoff { retryAttempt = 0 }
        ready = false
        protocolBlocked = false
        retryTask?.cancel()
        retryTask = nil
        disconnectCurrent(sendUnfollow: false)
        clearTaskState()
        continuation.yield(TaskActivitySnapshot(availability: .loading))

        guard desktopIsRunning() else {
            continuation.yield(TaskActivitySnapshot(
                runningCount: 0,
                waitingCount: 0,
                observedAt: now(),
                availability: .desktopNotRunning
            ))
            scheduleReconnect()
            return
        }

        do {
            let nextTransport = try transportFactory()
            try nextTransport.start()
            transport = nextTransport
            connectionGeneration += 1
            let generation = connectionGeneration
            frameDecoder = DesktopIPCFrameDecoder()
            readTask = Task { [weak self, nextTransport] in
                for await chunk in nextTransport.incoming {
                    guard !Task.isCancelled else { break }
                    await self?.consume(chunk, generation: generation)
                }
                await self?.connectionEnded(generation: generation)
            }
            try sendPayload(DesktopIPCMessageEncoder.initialize(requestID: nextRequestID(prefix: "initialize")))
        } catch {
            transport?.stop()
            transport = nil
            publishConnectionFailure()
            scheduleReconnect()
        }
    }

    private func consume(_ chunk: Data, generation: Int) {
        guard generation == connectionGeneration, started, !protocolBlocked else { return }
        do {
            for frame in try frameDecoder.append(chunk) {
                try handle(DesktopIPCMessageDecoder.decode(frame))
            }
        } catch {
            protocolBlocked = true
            continuation.yield(TaskActivitySnapshot(availability: .unsupportedProtocol))
            disconnectCurrent(sendUnfollow: false)
        }
    }

    private func handle(_ event: DesktopIPCEvent) throws {
        switch event {
        case .initialized:
            retryAttempt = 0
            ready = true
            try discoverUnownedCandidates()
            publishAvailable()

        case let .followingChanged(sourceClientID, taskID, hostID, following):
            let key = TaskKey(taskID: taskID, hostID: hostID)
            if following {
                let inserted = followedBy[key, default: []].insert(sourceClientID).inserted
                if inserted, !pendingOwnerRequests.values.contains(key) {
                    try discoverOwner(for: key)
                }
            } else {
                followedBy[key]?.remove(sourceClientID)
                if followedBy[key]?.isEmpty != false {
                    followedBy.removeValue(forKey: key)
                    if owners[key] != nil { try requestSnapshot(for: key) }
                    publishAvailable()
                }
            }

        case let .ownerResponse(requestID, ownerClientID):
            guard let key = pendingOwnerRequests.removeValue(forKey: requestID) else { return }
            guard let ownerClientID, !ownerClientID.isEmpty else { return }
            if let previousOwner = owners[key], previousOwner != ownerClientID {
                try sendPayload(DesktopIPCMessageEncoder.followingChanged(
                    taskID: key.taskID,
                    hostID: key.hostID,
                    following: false,
                    targetClientID: previousOwner
                ))
            }
            owners[key] = ownerClientID
            try requestSnapshot(for: key)

        case let .stateSnapshot(sourceClientID, observation):
            let key = TaskKey(taskID: observation.id, hostID: observation.hostID)
            guard owners[key] == nil || owners[key] == sourceClientID else { return }
            owners[key] = sourceClientID
            observations[key] = observation
            if shouldRetain(key, observation: observation) {
                publishAvailable()
            } else {
                try unsubscribeAndRemove(key)
            }

        case let .statePatches(sourceClientID, taskID, hostID, runtimePatch, requiresSnapshot):
            let key = TaskKey(taskID: taskID, hostID: hostID)
            guard owners[key] == sourceClientID else { return }
            if let runtimePatch, let current = observations[key] {
                let updated = CodexTaskObservation(
                    id: current.id,
                    hostID: current.hostID,
                    agentNickname: current.agentNickname,
                    sourceKind: current.sourceKind,
                    runtimeStatus: runtimePatch.status ?? current.runtimeStatus,
                    activeFlags: runtimePatch.activeFlags ?? current.activeFlags,
                    pendingRequestMethods: current.pendingRequestMethods,
                    hasPendingPlanImplementation: current.hasPendingPlanImplementation
                )
                observations[key] = updated
                if shouldRetain(key, observation: updated) {
                    publishAvailable()
                } else {
                    try unsubscribeAndRemove(key)
                }
            }
            if requiresSnapshot { scheduleSnapshot(for: key) }

        case let .clientDiscoveryRequested(requestID):
            try sendPayload(DesktopIPCMessageEncoder.clientDiscoveryResponse(requestID: requestID))

        case .ignored:
            break
        }
    }

    private func unsubscribeAndRemove(_ key: TaskKey) throws {
        if let owner = owners[key] {
            try sendPayload(DesktopIPCMessageEncoder.followingChanged(
                taskID: key.taskID,
                hostID: key.hostID,
                following: false,
                targetClientID: owner
            ))
        }
        followedBy.removeValue(forKey: key)
        owners.removeValue(forKey: key)
        observations.removeValue(forKey: key)
        snapshotTasks.removeValue(forKey: key)?.cancel()
        pendingOwnerRequests = pendingOwnerRequests.filter { $0.value != key }
        publishAvailable()
    }

    private func requestSnapshot(for key: TaskKey) throws {
        guard let owner = owners[key] else { return }
        try sendPayload(DesktopIPCMessageEncoder.followingChanged(
            taskID: key.taskID,
            hostID: key.hostID,
            following: true,
            targetClientID: owner
        ))
    }

    private func discoverUnownedCandidates() throws {
        try discoverCandidates(revalidatingOwners: false)
    }

    private func discoverCandidates(revalidatingOwners: Bool) throws {
        for key in candidateKeys
            where (revalidatingOwners || owners[key] == nil)
                && !pendingOwnerRequests.values.contains(key)
        {
            try discoverOwner(for: key)
        }
    }

    private func discoverOwner(for key: TaskKey) throws {
        let requestID = nextRequestID(prefix: "owner")
        pendingOwnerRequests[requestID] = key
        try sendPayload(DesktopIPCMessageEncoder.ownerDiscovery(
            requestID: requestID,
            taskID: key.taskID,
            hostID: key.hostID
        ))
    }

    private func shouldRetain(_ key: TaskKey, observation: CodexTaskObservation) -> Bool {
        candidateKeys.contains(key)
            || followedBy[key]?.isEmpty == false
            || TaskActivityClassifier.classification(for: observation) != .ignored
    }

    private func scheduleSnapshot(for key: TaskKey) {
        snapshotTasks[key]?.cancel()
        snapshotTasks[key] = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 150_000_000)
            guard !Task.isCancelled else { return }
            await self?.performScheduledSnapshot(for: key)
        }
    }

    private func performScheduledSnapshot(for key: TaskKey) {
        snapshotTasks.removeValue(forKey: key)
        do {
            try requestSnapshot(for: key)
        } catch {
            publishConnectionFailure()
            disconnectCurrent(sendUnfollow: false)
            scheduleReconnect()
        }
    }

    private func sendPayload(_ payload: Data) throws {
        guard let transport else { throw DesktopIPCProtocolError.invalidMessage }
        try transport.send(DesktopIPCFrameEncoder.encode(payload))
    }

    private func nextRequestID(prefix: String) -> String {
        requestSequence += 1
        return "codex-usage-bar-\(prefix)-\(requestSequence)"
    }

    private func publishAvailable() {
        let summary = TaskActivityClassifier.summarize(Array(observations.values), observedAt: now())
        continuation.yield(summary)
    }

    private func publishConnectionFailure() {
        if desktopIsRunning() {
            continuation.yield(TaskActivitySnapshot(availability: .offline))
        } else {
            continuation.yield(TaskActivitySnapshot(
                runningCount: 0,
                waitingCount: 0,
                observedAt: now(),
                availability: .desktopNotRunning
            ))
        }
    }

    private func connectionEnded(generation: Int) {
        guard generation == connectionGeneration, started, !protocolBlocked else { return }
        transport = nil
        clearTaskState()
        publishConnectionFailure()
        scheduleReconnect()
    }

    private func scheduleReconnect() {
        guard started, !protocolBlocked else { return }
        retryTask?.cancel()
        let exponent = min(retryAttempt, 5)
        let delay = min(pow(2.0, Double(exponent)), 30.0)
        retryAttempt += 1
        retryTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
            guard !Task.isCancelled else { return }
            await self?.connectAfterRetry()
        }
    }

    private func connectAfterRetry() {
        connect(resetBackoff: false)
    }

    private func disconnectCurrent(sendUnfollow: Bool) {
        connectionGeneration += 1
        ready = false
        if sendUnfollow, transport != nil {
            for (key, owner) in owners {
                if let payload = try? DesktopIPCMessageEncoder.followingChanged(
                    taskID: key.taskID,
                    hostID: key.hostID,
                    following: false,
                    targetClientID: owner
                ) {
                    try? sendPayload(payload)
                }
            }
        }
        readTask?.cancel()
        readTask = nil
        transport?.stop()
        transport = nil
        clearTaskState()
    }

    private func clearTaskState() {
        followedBy.removeAll()
        owners.removeAll()
        observations.removeAll()
        pendingOwnerRequests.removeAll()
        snapshotTasks.values.forEach { $0.cancel() }
        snapshotTasks.removeAll()
    }

    private struct TaskKey: Hashable, Sendable {
        let taskID: String
        let hostID: String
    }
}
