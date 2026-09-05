import Foundation

public enum TaskActivityAvailability: String, Codable, Equatable, Sendable {
    case loading
    case available
    case desktopNotRunning
    case unsupportedProtocol
    case offline
}

public struct TaskActivitySnapshot: Equatable, Sendable {
    public let runningCount: Int
    public let waitingCount: Int
    public let observedAt: Date?
    public let availability: TaskActivityAvailability
    public let observations: [CodexTaskObservation]

    public init(
        runningCount: Int = 0,
        waitingCount: Int = 0,
        observedAt: Date? = nil,
        availability: TaskActivityAvailability = .loading,
        observations: [CodexTaskObservation] = []
    ) {
        self.runningCount = runningCount
        self.waitingCount = waitingCount
        self.observedAt = observedAt
        self.availability = availability
        self.observations = observations
    }
}

public enum CodexTaskRuntimeStatus: String, Codable, Equatable, Sendable {
    case active
    case idle
    case notLoaded
    case systemError
}

public enum CodexTaskActiveFlag: String, Codable, Equatable, Sendable {
    case waitingOnApproval
    case waitingOnUserInput
}

public struct CodexTaskObservation: Equatable, Sendable {
    public let id: String
    public let hostID: String
    public let agentNickname: String?
    public let sourceKind: String?
    public let runtimeStatus: CodexTaskRuntimeStatus
    public let activeFlags: Set<CodexTaskActiveFlag>
    public let pendingRequestMethods: Set<String>
    public let hasPendingPlanImplementation: Bool

    public init(
        id: String,
        hostID: String,
        agentNickname: String? = nil,
        sourceKind: String? = nil,
        runtimeStatus: CodexTaskRuntimeStatus,
        activeFlags: Set<CodexTaskActiveFlag> = [],
        pendingRequestMethods: Set<String> = [],
        hasPendingPlanImplementation: Bool = false
    ) {
        self.id = id
        self.hostID = hostID
        self.agentNickname = agentNickname
        self.sourceKind = sourceKind
        self.runtimeStatus = runtimeStatus
        self.activeFlags = activeFlags
        self.pendingRequestMethods = pendingRequestMethods
        self.hasPendingPlanImplementation = hasPendingPlanImplementation
    }
}

public enum TaskActivityClassifier {
    public enum Classification: Int, Comparable, Equatable, Sendable {
        case ignored = 0
        case completed = 1
        case running = 2
        case waiting = 3

        public static func < (lhs: Classification, rhs: Classification) -> Bool {
            lhs.rawValue < rhs.rawValue
        }
    }

    public static func summarize(
        _ observations: [CodexTaskObservation],
        observedAt: Date = Date()
    ) -> TaskActivitySnapshot {
        var classifications: [String: Classification] = [:]

        for observation in observations where !observation.isInternalTask {
            let key = "\(observation.hostID):\(observation.id)"
            let next = classify(observation)
            let current = classifications[key] ?? .ignored
            classifications[key] = max(current, next)
        }

        return TaskActivitySnapshot(
            runningCount: classifications.values.filter { $0 == .running }.count,
            waitingCount: classifications.values.filter { $0 == .waiting }.count,
            observedAt: observedAt,
            availability: .available,
            observations: observations
        )
    }

    public static func classification(for observation: CodexTaskObservation) -> Classification {
        classify(observation)
    }

    public static func waitingReason(
        for observation: CodexTaskObservation
    ) -> ProjectTaskWaitingReason? {
        guard classify(observation) == .waiting else { return nil }
        if observation.activeFlags.contains(.waitingOnApproval)
            || observation.pendingRequestMethods.contains(where: isApprovalRequest)
        {
            return .approval
        }
        if observation.hasPendingPlanImplementation { return .planConfirmation }
        if observation.activeFlags.contains(.waitingOnUserInput)
            || observation.pendingRequestMethods.contains(where: isUserInputRequest)
        {
            return .userInput
        }
        return .actionRequired
    }

    private static func classify(_ observation: CodexTaskObservation) -> Classification {
        if observation.isInternalTask { return .ignored }
        if observation.hasPendingPlanImplementation
            || !observation.activeFlags.intersection([.waitingOnApproval, .waitingOnUserInput]).isEmpty
            || !observation.pendingRequestMethods.isEmpty
        {
            return .waiting
        }
        return observation.runtimeStatus == .active ? .running : .ignored
    }

    private static func isApprovalRequest(_ method: String) -> Bool {
        let value = method.lowercased()
        return value.contains("requestapproval")
            || value.contains("permission") && value.contains("request")
    }

    private static func isUserInputRequest(_ method: String) -> Bool {
        let value = method.lowercased()
        return value.contains("requestuserinfo")
            || value.contains("requestoptionpicker")
            || value.contains("requestsetupcodexcontextpicker")
            || value.contains("elicitation")
    }

}

private extension CodexTaskObservation {
    var isInternalTask: Bool {
        if let agentNickname, !agentNickname.isEmpty { return true }
        guard let sourceKind else { return false }
        return sourceKind.lowercased().contains("subagent")
    }
}
