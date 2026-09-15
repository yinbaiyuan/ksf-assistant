import Foundation

public enum CodexWorkspaceAvailability: String, Codable, Equatable, Sendable {
    case loading
    case available
    case offline
}

public struct CodexWorkspaceItem: Equatable, Sendable, Identifiable {
    public let id: String
    public let kind: String
    public let name: String
    public let path: String
    public let isPinned: Bool
    public let tasks: [ProjectTaskItem]
    public let usage: ProjectUsageSummary?
    public let runningCount: Int
    public let waitingCount: Int
    public let totalTaskCount: Int
    public let hiddenTaskCount: Int
    public let latestActivity: Date?

    public init(
        id: String,
        kind: String,
        name: String,
        path: String = "",
        isPinned: Bool = false,
        tasks: [ProjectTaskItem] = [],
        usage: ProjectUsageSummary? = nil,
        runningCount: Int = 0,
        waitingCount: Int = 0,
        totalTaskCount: Int = 0,
        hiddenTaskCount: Int = 0,
        latestActivity: Date? = nil
    ) {
        self.id = id
        self.kind = kind
        self.name = name
        self.path = path
        self.isPinned = isPinned
        self.tasks = tasks
        self.usage = usage
        self.runningCount = runningCount
        self.waitingCount = waitingCount
        self.totalTaskCount = totalTaskCount
        self.hiddenTaskCount = hiddenTaskCount
        self.latestActivity = latestActivity
    }

    public var activeTaskCount: Int { runningCount + waitingCount }
}

public enum CodexWorkspaceWorkset {
    public static func select(from items: [CodexWorkspaceItem], connectedTaskKeys: Set<String> = []) -> [CodexWorkspaceItem] {
        items.filter { $0.isPinned || $0.activeTaskCount > 0 || $0.tasks.contains { connectedTaskKeys.contains(TaskLinkIdentity.taskKey(for: $0.threadID)) } }
    }
}

public struct CodexWorkspaceSnapshot: Equatable, Sendable {
    public let availability: CodexWorkspaceAvailability
    public let workspaces: [CodexWorkspaceItem]
    public let observedAt: Date?
    public let message: String?

    public init(
        availability: CodexWorkspaceAvailability = .loading,
        workspaces: [CodexWorkspaceItem] = [],
        observedAt: Date? = nil,
        message: String? = nil
    ) {
        self.availability = availability
        self.workspaces = workspaces
        self.observedAt = observedAt
        self.message = message
    }
}
