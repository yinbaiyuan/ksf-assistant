import Foundation

public enum KSFProjectAvailability: String, Codable, Equatable, Sendable {
    case loading
    case available
    case unavailable
    case unsupportedProtocol
}

public struct KSFEngineeringMapping: Codable, Equatable, Hashable, Sendable, Identifiable {
    public let id: String
    public let role: String
    public let rootPath: String
    public let factEntrypoints: String
    public let purpose: String

    public init(
        id: String,
        role: String,
        rootPath: String,
        factEntrypoints: String = "",
        purpose: String = ""
    ) {
        self.id = id
        self.role = role
        self.rootPath = rootPath
        self.factEntrypoints = factEntrypoints
        self.purpose = purpose
    }
}

public struct KSFProject: Codable, Equatable, Hashable, Sendable, Identifiable {
    public let id: String
    public let name: String
    public let status: String
    public let summary: String
    public let memoryMode: String
    public let cardPath: String
    public let projectDirectory: String
    public let phase: String?
    public let focus: String?
    public let engineeringMappings: [KSFEngineeringMapping]

    public init(
        id: String,
        name: String,
        status: String = "active",
        summary: String = "",
        memoryMode: String = "single",
        cardPath: String,
        projectDirectory: String,
        phase: String? = nil,
        focus: String? = nil,
        engineeringMappings: [KSFEngineeringMapping] = []
    ) {
        self.id = id
        self.name = name
        self.status = status
        self.summary = summary
        self.memoryMode = memoryMode
        self.cardPath = cardPath
        self.projectDirectory = projectDirectory
        self.phase = phase
        self.focus = focus
        self.engineeringMappings = engineeringMappings
    }
}

public struct KSFProjectCatalogResponse: Codable, Equatable, Sendable {
    public let `protocol`: String
    public let generatedAt: String
    public let ksfRoot: String
    public let projects: [KSFProject]
}

public struct KSFRouteCategory: Codable, Equatable, Hashable, Sendable {
    public let categoryID: String?
    public let name: String?
    public let validationStatus: String?

    private enum CodingKeys: String, CodingKey {
        case categoryID = "category_id"
        case name
        case validationStatus = "validation_status"
    }

    public init(categoryID: String? = nil, name: String? = nil, validationStatus: String? = nil) {
        self.categoryID = categoryID
        self.name = name
        self.validationStatus = validationStatus
    }
}

public struct KSFRouteJob: Codable, Equatable, Hashable, Sendable {
    public let jobID: String?
    public let name: String?
    public let role: String?
    public let validationStatus: String?

    private enum CodingKeys: String, CodingKey {
        case jobID = "job_id"
        case name
        case role
        case validationStatus = "validation_status"
    }

    public init(
        jobID: String? = nil,
        name: String? = nil,
        role: String? = nil,
        validationStatus: String? = nil
    ) {
        self.jobID = jobID
        self.name = name
        self.role = role
        self.validationStatus = validationStatus
    }
}

public struct KSFRouteAbility: Codable, Equatable, Hashable, Sendable {
    public let abilityID: String?
    public let name: String?
    public let jobID: String?
    public let validationStatus: String?

    private enum CodingKeys: String, CodingKey {
        case abilityID = "ability_id"
        case name
        case jobID = "job_id"
        case validationStatus = "validation_status"
    }

    public init(
        abilityID: String? = nil,
        name: String? = nil,
        jobID: String? = nil,
        validationStatus: String? = nil
    ) {
        self.abilityID = abilityID
        self.name = name
        self.jobID = jobID
        self.validationStatus = validationStatus
    }
}

public struct KSFDispatchableSkill: Codable, Equatable, Hashable, Sendable {
    public let skillID: String?
    public let skillStage: String?
    public let abilityID: String?

    private enum CodingKeys: String, CodingKey {
        case skillID = "skill_id"
        case skillStage = "skill_stage"
        case abilityID = "ability_id"
    }

    public init(skillID: String? = nil, skillStage: String? = nil, abilityID: String? = nil) {
        self.skillID = skillID
        self.skillStage = skillStage
        self.abilityID = abilityID
    }
}

public struct KSFRouteSummary: Codable, Equatable, Hashable, Sendable {
    public let category: KSFRouteCategory?
    public let jobs: [KSFRouteJob]
    public let abilities: [KSFRouteAbility]
    public let dispatchableSkills: [KSFDispatchableSkill]
    public let receiptSHA256: String?

    public init(
        category: KSFRouteCategory? = nil,
        jobs: [KSFRouteJob] = [],
        abilities: [KSFRouteAbility] = [],
        dispatchableSkills: [KSFDispatchableSkill] = [],
        receiptSHA256: String? = nil
    ) {
        self.category = category
        self.jobs = jobs
        self.abilities = abilities
        self.dispatchableSkills = dispatchableSkills
        self.receiptSHA256 = receiptSHA256
    }

    public var mainJob: KSFRouteJob? {
        jobs.first { $0.role == "main" } ?? jobs.first
    }
}

public struct KSFRouteAbilityGroup: Equatable, Sendable {
    public let ability: KSFRouteAbility
    public let skills: [KSFDispatchableSkill]

    public init(ability: KSFRouteAbility, skills: [KSFDispatchableSkill]) {
        self.ability = ability
        self.skills = skills
    }
}

public struct KSFRouteJobGroup: Equatable, Sendable {
    public let job: KSFRouteJob?
    public let abilities: [KSFRouteAbilityGroup]

    public init(job: KSFRouteJob?, abilities: [KSFRouteAbilityGroup]) {
        self.job = job
        self.abilities = abilities
    }
}

public struct KSFRoutePresentation: Equatable, Sendable {
    public let category: KSFRouteCategory?
    public let jobGroups: [KSFRouteJobGroup]
    public let unassignedSkills: [KSFDispatchableSkill]

    public init(route: KSFRouteSummary) {
        category = route.category
        let skillsByAbility = Dictionary(grouping: route.dispatchableSkills.compactMap { skill in
            skill.abilityID.map { ($0, skill) }
        }, by: \.0)
        let abilityGroups = route.abilities.map { ability in
            KSFRouteAbilityGroup(
                ability: ability,
                skills: ability.abilityID.flatMap { abilityID in
                    skillsByAbility[abilityID]?.map(\.1)
                } ?? []
            )
        }
        let abilitiesByJob = Dictionary(grouping: abilityGroups.compactMap { group in
            group.ability.jobID.map { ($0, group) }
        }, by: \.0)
        let knownJobIDs = Set(route.jobs.compactMap(\.jobID))
        jobGroups = route.jobs.map { job in
            let abilities = job.jobID.flatMap { jobID in
                abilitiesByJob[jobID]?.map(\.1)
            } ?? []
            return KSFRouteJobGroup(job: job, abilities: abilities)
        } + [
            KSFRouteJobGroup(
                job: nil,
                abilities: abilityGroups.filter { group in
                    guard let jobID = group.ability.jobID else { return true }
                    return !knownJobIDs.contains(jobID)
                }
            )
        ].filter { !$0.abilities.isEmpty }

        let knownAbilityIDs = Set(route.abilities.compactMap(\.abilityID))
        unassignedSkills = route.dispatchableSkills.filter { skill in
            guard let abilityID = skill.abilityID else { return true }
            return !knownAbilityIDs.contains(abilityID)
        }
    }
}

public struct KSFTaskBinding: Codable, Equatable, Hashable, Sendable {
    public let projectCard: String
    public let boundAt: String
    public let observedAt: String?
    public let route: KSFRouteSummary?

    public init(projectCard: String, boundAt: String, observedAt: String? = nil, route: KSFRouteSummary? = nil) {
        self.projectCard = projectCard
        self.boundAt = boundAt
        self.observedAt = observedAt
        self.route = route
    }
}

public struct KSFTaskProjection: Codable, Equatable, Sendable {
    public let `protocol`: String
    public let threadKey: String
    public let bindings: [KSFTaskBinding]
    public let updatedAt: String

    public var currentBinding: KSFTaskBinding? { bindings.last }

    public init(protocol: String = "ksf-task-project-projection-v1", threadKey: String, bindings: [KSFTaskBinding], updatedAt: String) {
        self.protocol = `protocol`
        self.threadKey = threadKey
        self.bindings = bindings
        self.updatedAt = updatedAt
    }
}

public struct KSFResolvedTaskProjection: Codable, Equatable, Sendable {
    public let threadId: String
    public let projection: KSFTaskProjection
}

public struct KSFTaskProjectionResolution: Codable, Equatable, Sendable {
    public let `protocol`: String
    public let projections: [KSFResolvedTaskProjection]

    public var byThreadID: [String: KSFTaskProjection] {
        Dictionary(uniqueKeysWithValues: projections.map { ($0.threadId, $0.projection) })
    }
}

public struct CodexThreadMetadata: Codable, Equatable, Hashable, Sendable, Identifiable {
    public let id: String
    public let name: String?
    public let cwd: String
    public let parentThreadId: String?
    public let agentNickname: String?
    public let createdAt: Int64
    public let updatedAt: Int64
    public let path: String?

    public init(
        id: String,
        name: String? = nil,
        cwd: String,
        parentThreadId: String? = nil,
        agentNickname: String? = nil,
        createdAt: Int64,
        updatedAt: Int64,
        path: String? = nil
    ) {
        self.id = id
        self.name = name
        self.cwd = cwd
        self.parentThreadId = parentThreadId
        self.agentNickname = agentNickname
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.path = path
    }
}

public struct CodexThreadListResponse: Codable, Equatable, Sendable {
    public let data: [CodexThreadMetadata]
    public let nextCursor: String?
}

public struct ProjectBindingTransition: Codable, Equatable, Hashable, Sendable {
    public let projectID: String
    public let boundAt: Date

    public init(projectID: String, boundAt: Date) {
        self.projectID = projectID
        self.boundAt = boundAt
    }
}

public struct ProjectTaskTimeline: Codable, Equatable, Sendable {
    public let threadID: String
    public let transitions: [ProjectBindingTransition]

    public init(threadID: String, transitions: [ProjectBindingTransition]) {
        self.threadID = threadID
        self.transitions = transitions.sorted { $0.boundAt < $1.boundAt }
    }
}

public struct ProjectUsageSummary: Codable, Equatable, Hashable, Sendable {
    public let cumulativeTokens: Int64
    public let todayTokens: Int64
    public let trackingStartedAt: Date
    public let isComplete: Bool
    public let uncountedThreadCount: Int

    public init(
        cumulativeTokens: Int64 = 0,
        todayTokens: Int64 = 0,
        trackingStartedAt: Date,
        isComplete: Bool = true,
        uncountedThreadCount: Int = 0
    ) {
        self.cumulativeTokens = cumulativeTokens
        self.todayTokens = todayTokens
        self.trackingStartedAt = trackingStartedAt
        self.isComplete = isComplete
        self.uncountedThreadCount = uncountedThreadCount
    }
}

public struct ProjectLaunchAction: Equatable, Hashable, Sendable, Identifiable {
    public let id: String
    public let title: String
    public let symbol: String
    public let scriptPath: String
    public let workingDirectory: String

    public init(
        projectID: String,
        title: String = "启动项目",
        symbol: String = "play.fill",
        scriptPath: String,
        workingDirectory: String
    ) {
        id = "\(projectID):start.sh"
        self.title = title
        self.symbol = symbol
        self.scriptPath = scriptPath
        self.workingDirectory = workingDirectory
    }
}

public enum ProjectTaskWaitingReason: Int, Equatable, Comparable, Sendable {
    case actionRequired = 1
    case userInput = 2
    case planConfirmation = 3
    case approval = 4

    public static func < (lhs: ProjectTaskWaitingReason, rhs: ProjectTaskWaitingReason) -> Bool {
        lhs.rawValue < rhs.rawValue
    }
}

public struct ProjectTaskRuntime: Decodable, Equatable, Sendable {
    public struct Progress: Decodable, Equatable, Sendable {
        public let summary: String?
        public let percent: Int?
    }

    public let scope: String
    public let reportedStatus: String
    public let reportedAt: Date?
    public let reportFreshness: String
    public let routeFreshness: String
    public let observedStatus: String
    public let observedAt: Date?
    public let progress: Progress?

    public var reportLabel: String {
        switch reportFreshness {
        case "recent": return "Agent 已上报"
        case "stale": return "Agent 上报已过期"
        default: return "Agent 上报时效未知"
        }
    }

    public var routeLabel: String {
        switch routeFreshness {
        case "current": return "KSF 来源已验证 · 当前有效"
        case "stale": return "KSF 来源已过期 · 不作为当前路由"
        case "unavailable": return "KSF 来源暂无法验证"
        default: return "尚无 KSF 已验证来源"
        }
    }

    public var reportedStatusLabel: String {
        switch reportedStatus {
        case "running": return "进行中"
        case "waiting": return "等待中"
        case "blocked": return "受阻"
        case "completed": return "已完成"
        default: return "未知"
        }
    }
}

/// A live, display-only project task. This model intentionally has no Codable
/// conformance so raw task identity and titles cannot enter app caches by accident.
public struct ProjectTaskItem: Equatable, Sendable, Identifiable {
    public let id: String
    public let threadID: String
    public let hostID: String
    public let name: String?
    public let classification: TaskActivityClassifier.Classification
    public let waitingReason: ProjectTaskWaitingReason?
    public let route: KSFRouteSummary?
    public let taskRuntime: ProjectTaskRuntime?
    public let createdAt: Date
    public let projectID: String

    public init(
        threadID: String,
        hostID: String,
        name: String? = nil,
        classification: TaskActivityClassifier.Classification,
        waitingReason: ProjectTaskWaitingReason? = nil,
        route: KSFRouteSummary? = nil,
        taskRuntime: ProjectTaskRuntime? = nil,
        createdAt: Date,
        projectID: String
    ) {
        id = "\(hostID):\(threadID)"
        self.threadID = threadID
        self.hostID = hostID
        self.name = name
        self.classification = classification
        self.waitingReason = waitingReason
        self.route = route
        self.taskRuntime = taskRuntime
        self.createdAt = createdAt
        self.projectID = projectID
    }
}

public enum ProjectDashboardKind: Equatable, Sendable {
    case project
    case unassigned
    case unavailablePinned
}

public struct ProjectDashboardItem: Equatable, Sendable, Identifiable {
    public let id: String
    public let kind: ProjectDashboardKind
    public let project: KSFProject?
    public let isPinned: Bool
    public let tasks: [ProjectTaskItem]
    public let latestActivity: Date?
    public let usage: ProjectUsageSummary?
    public let launchAction: ProjectLaunchAction?
    public let preferredEngineeringID: String?

    public init(
        id: String,
        project: KSFProject?,
        kind: ProjectDashboardKind? = nil,
        isPinned: Bool,
        tasks: [ProjectTaskItem] = [],
        latestActivity: Date? = nil,
        usage: ProjectUsageSummary? = nil,
        launchAction: ProjectLaunchAction? = nil,
        preferredEngineeringID: String? = nil
    ) {
        self.id = id
        self.kind = kind ?? (project == nil ? .unavailablePinned : .project)
        self.project = project
        self.isPinned = isPinned
        self.tasks = tasks
        self.latestActivity = latestActivity
        self.usage = usage
        self.launchAction = launchAction
        self.preferredEngineeringID = preferredEngineeringID
    }

    public var isAvailable: Bool { project != nil }
    public var isUnassigned: Bool { kind == .unassigned }
    public var runningCount: Int { tasks.filter { $0.classification == .running }.count }
    public var waitingCount: Int { tasks.filter { $0.classification == .waiting }.count }
    public var activeTaskCount: Int { runningCount + waitingCount }

    public func replacingTasks(_ tasks: [ProjectTaskItem]) -> ProjectDashboardItem {
        ProjectDashboardItem(
            id: id,
            project: project,
            kind: kind,
            isPinned: isPinned,
            tasks: tasks,
            latestActivity: latestActivity,
            usage: usage,
            launchAction: launchAction,
            preferredEngineeringID: preferredEngineeringID
        )
    }
}

public struct ProjectDashboardSnapshot: Equatable, Sendable {
    public let availability: KSFProjectAvailability
    public let projects: [ProjectDashboardItem]
    public let catalog: [KSFProject]
    public let observedAt: Date?
    public let message: String?

    public init(
        availability: KSFProjectAvailability = .loading,
        projects: [ProjectDashboardItem] = [],
        catalog: [KSFProject] = [],
        observedAt: Date? = nil,
        message: String? = nil
    ) {
        self.availability = availability
        self.projects = projects
        self.catalog = catalog
        self.observedAt = observedAt
        self.message = message
    }
}

public enum KSFProjectWorkset {
    public static func select(from items: [ProjectDashboardItem], connectedTaskKeys: Set<String> = []) -> [ProjectDashboardItem] {
        items.filter { item in
            item.isPinned || item.activeTaskCount > 0 || item.tasks.contains { connectedTaskKeys.contains(TaskLinkIdentity.taskKey(for: $0.threadID)) }
        }
    }
}

public enum KSFProjectListOrdering {
    public static func reconcile(previous: [String], candidates: [String]) -> [String] {
        var seen = Set<String>()
        var result: [String] = []
        for id in previous + candidates where !id.isEmpty && seen.insert(id).inserted {
            result.append(id)
        }
        return result
    }

    public static func moving(
        _ id: String,
        in stableOrder: [String],
        toPinned: Bool,
        pinnedProjectIDs: Set<String>
    ) -> [String] {
        var result = reconcile(previous: stableOrder, candidates: [id])
        result.removeAll { $0 == id }
        if toPinned {
            let insertionIndex = result.lastIndex { pinnedProjectIDs.contains($0) }
                .map { result.index(after: $0) } ?? result.startIndex
            result.insert(id, at: insertionIndex)
        } else {
            result.append(id)
        }
        return result
    }

    public static func sort(
        _ items: [ProjectDashboardItem],
        stableOrder: [String]
    ) -> [ProjectDashboardItem] {
        var stablePositions: [String: Int] = [:]
        for (index, id) in stableOrder.enumerated() where stablePositions[id] == nil {
            stablePositions[id] = index
        }
        let fallbackPositions = Dictionary(
            uniqueKeysWithValues: items.enumerated().map { ($0.element.id, $0.offset) }
        )
        let fallbackStart = stablePositions.count

        return items.sorted { left, right in
            if left.isUnassigned != right.isUnassigned {
                return left.isUnassigned
            }
            if left.isPinned != right.isPinned {
                return left.isPinned && !right.isPinned
            }
            let leftPosition = stablePositions[left.id]
                ?? fallbackStart + (fallbackPositions[left.id] ?? 0)
            let rightPosition = stablePositions[right.id]
                ?? fallbackStart + (fallbackPositions[right.id] ?? 0)
            if leftPosition != rightPosition { return leftPosition < rightPosition }
            return left.id < right.id
        }
    }
}

public enum ProjectTaskListOrdering {
    public static func reconcile(previous: [String], candidates: [ProjectTaskItem]) -> [String] {
        var seen = Set<String>()
        var result = previous.filter { !$0.isEmpty && seen.insert($0).inserted }
        let newTasks = candidates
            .filter { !seen.contains($0.id) }
            .sorted {
                if $0.createdAt != $1.createdAt { return $0.createdAt < $1.createdAt }
                return $0.id < $1.id
            }
        for task in newTasks where seen.insert(task.id).inserted {
            result.append(task.id)
        }
        return result
    }

    public static func sort(
        _ tasks: [ProjectTaskItem],
        stableOrder: [String]
    ) -> [ProjectTaskItem] {
        let byID = Dictionary(uniqueKeysWithValues: tasks.map { ($0.id, $0) })
        let known = stableOrder.compactMap { byID[$0] }
        let knownIDs = Set(known.map(\.id))
        let newTasks = tasks
            .filter { !knownIDs.contains($0.id) }
            .sorted {
                if $0.createdAt != $1.createdAt { return $0.createdAt < $1.createdAt }
                return $0.id < $1.id
            }
        return known + newTasks
    }
}

public enum CodexTaskDeepLinkError: Error, Equatable, LocalizedError {
    case invalidTaskID

    public var errorDescription: String? {
        "无法打开任务：任务标识无效。"
    }
}

public enum CodexTaskDeepLink {
    public static func url(for taskID: String) throws -> URL {
        guard !taskID.isEmpty,
              taskID.unicodeScalars.allSatisfy({ !CharacterSet.controlCharacters.contains($0) })
        else {
            throw CodexTaskDeepLinkError.invalidTaskID
        }
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "/%?#")
        guard let encoded = taskID.addingPercentEncoding(withAllowedCharacters: allowed) else {
            throw CodexTaskDeepLinkError.invalidTaskID
        }
        var components = URLComponents()
        components.scheme = "codex"
        components.host = "threads"
        components.percentEncodedPath = "/\(encoded)"
        guard let url = components.url else { throw CodexTaskDeepLinkError.invalidTaskID }
        return url
    }
}

public enum KSFProjectDashboardBuilder {
    public static let unassignedProjectID = "runtime://unassigned-tasks"

    public static func localTaskCandidateIDs(
        catalog: [KSFProject],
        threads: [CodexThreadMetadata],
        projections: [String: KSFTaskProjection]
    ) -> Set<String> {
        let projectsByID = Dictionary(uniqueKeysWithValues: catalog.map { ($0.id, $0) })
        return Set(threads.compactMap { metadata in
            guard metadata.parentThreadId == nil,
                  metadata.agentNickname?.isEmpty != false,
                  metadata.path != nil
            else { return nil }

            let projected = projections[metadata.id]?.currentBinding
            let cwdMatch = engineeringMatch(matching: metadata.cwd, projects: catalog)
            let projectID = projected?.projectCard ?? cwdMatch?.projectID
            guard let projectID, projectsByID[projectID] != nil else { return nil }
            return metadata.id
        })
    }

    public static func build(
        catalog: [KSFProject],
        activeTasks: [CodexTaskObservation],
        threads: [CodexThreadMetadata],
        projections: [String: KSFTaskProjection],
        pinnedProjectIDs: Set<String>,
        usage: [String: ProjectUsageSummary],
        launchActions: [String: ProjectLaunchAction],
        now: Date = Date()
    ) -> [ProjectDashboardItem] {
        let projectsByID = Dictionary(uniqueKeysWithValues: catalog.map { ($0.id, $0) })
        let metadataByID = threads.reduce(into: [String: CodexThreadMetadata]()) { result, metadata in
            if result[metadata.id]?.updatedAt ?? .min <= metadata.updatedAt {
                result[metadata.id] = metadata
            }
        }
        var aggregates: [String: Aggregate] = [:]
        var unassignedAggregate = Aggregate()

        let activeTasks = deduplicated(activeTasks)
        for observation in activeTasks {
            let classification = TaskActivityClassifier.classification(for: observation)
            guard classification != .ignored else { continue }
            let metadata = metadataByID[observation.id]
            guard metadata?.parentThreadId == nil,
                  metadata?.agentNickname?.isEmpty != false
            else { continue }
            let projected = projections[observation.id]?.currentBinding
            let cwdMatch = engineeringMatch(matching: metadata?.cwd, projects: catalog)
            let projectID = projected?.projectCard ?? cwdMatch?.projectID
            let assignedProjectID = projectID.flatMap { projectsByID[$0] == nil ? nil : $0 }
            let taskProjectID = assignedProjectID ?? unassignedProjectID

            var aggregate = assignedProjectID.map { aggregates[$0] ?? Aggregate() }
                ?? unassignedAggregate
            let created = metadata.map { Date(timeIntervalSince1970: TimeInterval($0.createdAt)) } ?? now
            aggregate.tasks.append(ProjectTaskItem(
                threadID: observation.id,
                hostID: observation.hostID,
                name: metadata?.name,
                classification: classification,
                waitingReason: TaskActivityClassifier.waitingReason(for: observation),
                route: projected?.route,
                createdAt: created,
                projectID: taskProjectID
            ))
            let updated = metadata.map { Date(timeIntervalSince1970: TimeInterval($0.updatedAt)) } ?? now
            aggregate.latestActivity = max(aggregate.latestActivity ?? .distantPast, updated)
            if let assignedProjectID, let cwdMatch, cwdMatch.projectID == assignedProjectID {
                let priority = classification == .waiting ? 2 : 1
                let candidate = EngineeringCandidate(
                    priority: priority,
                    updatedAt: updated,
                    engineeringID: cwdMatch.engineeringID
                )
                if aggregate.engineeringCandidate == nil || candidate > aggregate.engineeringCandidate! {
                    aggregate.engineeringCandidate = candidate
                }
            }
            if let assignedProjectID {
                aggregates[assignedProjectID] = aggregate
            } else {
                unassignedAggregate = aggregate
            }
        }

        let activeThreadIDs = Set(activeTasks.map(\.id))
        for metadata in metadataByID.values where !activeThreadIDs.contains(metadata.id) {
            guard metadata.parentThreadId == nil,
                  metadata.agentNickname?.isEmpty != false,
                  metadata.path != nil
            else { continue }
            let projected = projections[metadata.id]?.currentBinding
            let cwdMatch = engineeringMatch(matching: metadata.cwd, projects: catalog)
            let projectID = projected?.projectCard ?? cwdMatch?.projectID
            guard let projectID, projectsByID[projectID] != nil else { continue }

            var aggregate = aggregates[projectID] ?? Aggregate()
            let created = Date(timeIntervalSince1970: TimeInterval(metadata.createdAt))
            aggregate.tasks.append(ProjectTaskItem(
                threadID: metadata.id,
                hostID: "local",
                name: metadata.name,
                classification: .completed,
                route: projected?.route,
                createdAt: created,
                projectID: projectID
            ))
            let updated = Date(timeIntervalSince1970: TimeInterval(metadata.updatedAt))
            aggregate.latestActivity = max(aggregate.latestActivity ?? .distantPast, updated)
            if let cwdMatch, cwdMatch.projectID == projectID {
                let candidate = EngineeringCandidate(
                    priority: 0,
                    updatedAt: updated,
                    engineeringID: cwdMatch.engineeringID
                )
                if aggregate.engineeringCandidate == nil || candidate > aggregate.engineeringCandidate! {
                    aggregate.engineeringCandidate = candidate
                }
            }
            aggregates[projectID] = aggregate
        }

        let includedIDs = Set(aggregates.keys).union(pinnedProjectIDs).subtracting([unassignedProjectID])
        let catalogOrder = catalog.map(\.id).filter { includedIDs.contains($0) }
        let missingOrder = includedIDs.subtracting(Set(catalogOrder)).sorted()
        let stableOrder = KSFProjectListOrdering.reconcile(
            previous: [],
            candidates: catalogOrder + missingOrder
        )
        let items = stableOrder.map { id in
            let aggregate = aggregates[id] ?? Aggregate()
            return ProjectDashboardItem(
                id: id,
                project: projectsByID[id],
                isPinned: pinnedProjectIDs.contains(id),
                tasks: aggregate.tasks.sorted {
                    if $0.createdAt != $1.createdAt { return $0.createdAt < $1.createdAt }
                    return $0.id < $1.id
                },
                latestActivity: aggregate.latestActivity,
                usage: usage[id],
                launchAction: launchActions[id],
                preferredEngineeringID: aggregate.engineeringCandidate?.engineeringID
            )
        }
        var result = KSFProjectListOrdering.sort(items, stableOrder: stableOrder)
        if !unassignedAggregate.tasks.isEmpty || pinnedProjectIDs.contains(unassignedProjectID) {
            result.insert(ProjectDashboardItem(
                id: unassignedProjectID,
                project: nil,
                kind: .unassigned,
                isPinned: pinnedProjectIDs.contains(unassignedProjectID),
                tasks: unassignedAggregate.tasks.sorted {
                    if $0.createdAt != $1.createdAt { return $0.createdAt < $1.createdAt }
                    return $0.id < $1.id
                },
                latestActivity: unassignedAggregate.latestActivity
            ), at: 0)
        }
        return result
    }

    private static func engineeringMatch(
        matching cwd: String?,
        projects: [KSFProject]
    ) -> (projectID: String, engineeringID: String)? {
        guard let cwd else { return nil }
        let normalized = URL(fileURLWithPath: cwd).standardizedFileURL.path
        for project in projects {
            if let mapping = project.engineeringMappings.first(where: {
                URL(fileURLWithPath: $0.rootPath).standardizedFileURL.path == normalized
            }) {
                return (project.id, mapping.id)
            }
        }
        return nil
    }

    private static func deduplicated(_ observations: [CodexTaskObservation]) -> [CodexTaskObservation] {
        var merged: [String: CodexTaskObservation] = [:]
        for observation in observations {
            guard TaskActivityClassifier.classification(for: observation) != .ignored else { continue }
            let key = "\(observation.hostID):\(observation.id)"
            guard let current = merged[key] else {
                merged[key] = observation
                continue
            }
            merged[key] = CodexTaskObservation(
                id: observation.id,
                hostID: observation.hostID,
                agentNickname: current.agentNickname ?? observation.agentNickname,
                sourceKind: current.sourceKind ?? observation.sourceKind,
                runtimeStatus: current.runtimeStatus == .active || observation.runtimeStatus == .active
                    ? .active : observation.runtimeStatus,
                activeFlags: current.activeFlags.union(observation.activeFlags),
                pendingRequestMethods: current.pendingRequestMethods.union(observation.pendingRequestMethods),
                hasPendingPlanImplementation: current.hasPendingPlanImplementation
                    || observation.hasPendingPlanImplementation
            )
        }
        return merged.values.sorted {
            if $0.hostID != $1.hostID { return $0.hostID < $1.hostID }
            return $0.id < $1.id
        }
    }

    private struct Aggregate {
        var tasks: [ProjectTaskItem] = []
        var latestActivity: Date?
        var engineeringCandidate: EngineeringCandidate?
    }

    private struct EngineeringCandidate: Comparable {
        let priority: Int
        let updatedAt: Date
        let engineeringID: String

        static func < (lhs: EngineeringCandidate, rhs: EngineeringCandidate) -> Bool {
            if lhs.priority != rhs.priority { return lhs.priority < rhs.priority }
            return lhs.updatedAt < rhs.updatedAt
        }
    }
}
