import Darwin
import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private func expect<T: Equatable>(_ actual: T, _ expected: T, _ message: String) throws {
    guard actual == expected else {
        throw TestFailure(description: "\(message): expected \(expected), got \(actual)")
    }
}

private func project(_ id: String, root: String) -> KSFProject {
    KSFProject(
        id: id,
        name: id,
        cardPath: "/ksf/\(id).md",
        projectDirectory: "/ksf/\(id)",
        engineeringMappings: [KSFEngineeringMapping(id: "git", role: "主工程", rootPath: root)]
    )
}

private func observation(
    _ id: String,
    status: CodexTaskRuntimeStatus = .active,
    flags: Set<CodexTaskActiveFlag> = [],
    nickname: String? = nil
) -> CodexTaskObservation {
    CodexTaskObservation(
        id: id,
        hostID: "local",
        agentNickname: nickname,
        runtimeStatus: status,
        activeFlags: flags
    )
}

private func dashboardTask(
    _ id: String,
    _ classification: TaskActivityClassifier.Classification,
    createdAt: TimeInterval = 1
) -> ProjectTaskItem {
    ProjectTaskItem(
        threadID: id,
        hostID: "local",
        classification: classification,
        waitingReason: classification == .waiting ? .userInput : nil,
        createdAt: Date(timeIntervalSince1970: createdAt),
        projectID: id
    )
}

private func testProjectAggregation() throws {
    let alpha = project("alpha", root: "/git/alpha")
    let beta = project("beta", root: "/git/beta")
    let route = KSFRouteSummary(
        category: KSFRouteCategory(categoryID: "cat", name: "产品开发"),
        jobs: [KSFRouteJob(jobID: "job", name: "产品工程师", role: "main")],
        abilities: [KSFRouteAbility(abilityID: "ability", name: "界面设计")],
        dispatchableSkills: [KSFDispatchableSkill(skillID: "design-md", skillStage: "active")]
    )
    let projection = KSFTaskProjection(
        threadKey: "opaque",
        bindings: [KSFTaskBinding(projectCard: "beta", boundAt: "2026-08-30T00:00:00Z", route: route)],
        updatedAt: "2026-08-30T00:00:00Z"
    )
    let tasks = [
        observation("running"),
        observation("running"),
        observation("waiting", flags: [.waitingOnUserInput]),
        observation("child", nickname: "helper"),
    ]
    let threads = [
        CodexThreadMetadata(id: "running", name: "实现功能", cwd: "/git/alpha", createdAt: 1, updatedAt: 10),
        CodexThreadMetadata(id: "waiting", name: "确认选择", cwd: "/unrelated", createdAt: 2, updatedAt: 20),
        CodexThreadMetadata(id: "child", cwd: "/git/alpha", createdAt: 1, updatedAt: 30),
    ]
    let items = KSFProjectDashboardBuilder.build(
        catalog: [alpha, beta],
        activeTasks: tasks,
        threads: threads,
        projections: ["waiting": projection],
        pinnedProjectIDs: ["missing"],
        selectedProjectID: "alpha",
        usage: [:],
        actions: [:]
    )

    try expect(items.map(\.id), ["missing", "alpha", "beta"], "pinned projects stay above current-task projects")
    try expect(items[0].isAvailable, false, "missing pinned project remains unavailable")
    try expect(items[1].runningCount, 1, "exact cwd maps a top-level task")
    try expect(items[1].tasks.first?.name, "实现功能", "task keeps its original App Server name")
    try expect(items[1].preferredEngineeringID, "git", "exact cwd prioritizes the matching engineering root")
    try expect(items[2].waitingCount, 1, "waiting task is counted once")
    try expect(items[2].runningCount, 0, "waiting and running are mutually exclusive")
    try expect(items[2].tasks.first?.waitingReason, .userInput, "waiting task keeps its own reason")
    try expect(items[2].tasks.first?.route?.mainJob?.name, "产品工程师", "task keeps its own route")
    try expect(items[2].primaryRoute?.mainJob?.name, "产品工程师", "projected route is retained")

    let idle = ProjectDashboardItem(id: "idle", project: project("idle", root: "/git/idle"), isPinned: false)
    try expect(
        KSFProjectWorkset.select(from: items + [idle]).map(\.id),
        ["missing", "alpha", "beta"],
        "home workset includes only pinned or current-task projects"
    )

    let stableOrder = KSFProjectListOrdering.reconcile(
        previous: ["alpha", "missing", "beta"],
        candidates: ["gamma", "alpha"]
    )
    try expect(stableOrder, ["alpha", "missing", "beta", "gamma"], "new projects append without moving prior projects")
    let activityChanged = [
        ProjectDashboardItem(id: "gamma", project: nil, isPinned: false, tasks: [dashboardTask("gamma", .running)], latestActivity: Date(timeIntervalSince1970: 40)),
        ProjectDashboardItem(id: "beta", project: nil, isPinned: false, tasks: [dashboardTask("beta", .waiting)], latestActivity: Date(timeIntervalSince1970: 50)),
        ProjectDashboardItem(id: "missing", project: nil, isPinned: true),
        ProjectDashboardItem(id: "alpha", project: nil, isPinned: false, tasks: [dashboardTask("alpha", .running)], latestActivity: Date(timeIntervalSince1970: 60)),
    ]
    try expect(
        KSFProjectListOrdering.sort(activityChanged, stableOrder: stableOrder).map(\.id),
        ["missing", "alpha", "beta", "gamma"],
        "activity and recency changes never reorder existing project rows"
    )
    let pinnedOrder = KSFProjectListOrdering.moving(
        "beta",
        in: stableOrder,
        toPinned: true,
        pinnedProjectIDs: ["missing", "beta"]
    )
    let afterPin = activityChanged.map { item in
        ProjectDashboardItem(
            id: item.id,
            project: item.project,
            isPinned: item.id == "missing" || item.id == "beta",
            tasks: item.tasks,
            latestActivity: item.latestActivity
        )
    }
    try expect(
        KSFProjectListOrdering.sort(afterPin, stableOrder: pinnedOrder).map(\.id),
        ["missing", "beta", "alpha", "gamma"],
        "newly pinned project appends after existing pinned projects"
    )

    let nested = KSFProjectDashboardBuilder.build(
        catalog: [alpha],
        activeTasks: [observation("nested")],
        threads: [CodexThreadMetadata(id: "nested", cwd: "/git/alpha/subdir", createdAt: 1, updatedAt: 1)],
        projections: [:],
        pinnedProjectIDs: [],
        usage: [:],
        actions: [:]
    )
    try expect(nested.count, 0, "cwd matching is exact and never guesses a nested directory")

    let mixedWait = CodexTaskObservation(
        id: "mixed",
        hostID: "local",
        runtimeStatus: .active,
        activeFlags: [.waitingOnApproval, .waitingOnUserInput],
        hasPendingPlanImplementation: true
    )
    try expect(TaskActivityClassifier.waitingReason(for: mixedWait), .approval, "approval wins waiting-reason priority")
    let orderedTasks = [
        dashboardTask("later", .running, createdAt: 20),
        dashboardTask("earlier", .waiting, createdAt: 10),
    ]
    let firstTaskOrder = ProjectTaskListOrdering.reconcile(previous: [], candidates: orderedTasks)
    try expect(firstTaskOrder, ["local:earlier", "local:later"], "initial task order follows creation time")
    let changedTasks = [
        dashboardTask("later", .waiting, createdAt: 20),
        dashboardTask("new", .running, createdAt: 5),
        dashboardTask("earlier", .running, createdAt: 10),
    ]
    let secondTaskOrder = ProjectTaskListOrdering.reconcile(previous: firstTaskOrder, candidates: changedTasks)
    try expect(secondTaskOrder, ["local:earlier", "local:later", "local:new"], "new tasks append after the stable order")
    try expect(
        ProjectTaskListOrdering.sort(changedTasks, stableOrder: secondTaskOrder).map(\.threadID),
        ["earlier", "later", "new"],
        "task status changes never reorder rows"
    )
    try expect(
        try CodexTaskDeepLink.url(for: "task/with space").absoluteString,
        "codex://threads/task%2Fwith%20space",
        "task deep link encodes one path segment"
    )
}

private func testLocalTaskCandidateSelection() throws {
    let alpha = project("alpha", root: "/git/alpha")
    let beta = project("beta", root: "/git/beta")
    let projection = KSFTaskProjection(
        threadKey: "opaque",
        bindings: [KSFTaskBinding(
            projectCard: "beta",
            boundAt: "2026-08-30T00:00:00Z",
            route: KSFRouteSummary()
        )],
        updatedAt: "2026-08-30T00:00:00Z"
    )
    let threads = [
        CodexThreadMetadata(
            id: "projected",
            cwd: "/ksf",
            createdAt: 1,
            updatedAt: 10,
            path: "/sessions/projected.jsonl"
        ),
        CodexThreadMetadata(
            id: "mapped",
            cwd: "/git/alpha",
            createdAt: 2,
            updatedAt: 20,
            path: "/sessions/mapped.jsonl"
        ),
        CodexThreadMetadata(
            id: "child",
            cwd: "/git/alpha",
            parentThreadId: "mapped",
            createdAt: 3,
            updatedAt: 30,
            path: "/sessions/child.jsonl"
        ),
        CodexThreadMetadata(
            id: "remote",
            cwd: "/git/alpha",
            createdAt: 4,
            updatedAt: 40
        ),
        CodexThreadMetadata(
            id: "unassigned",
            cwd: "/ksf",
            createdAt: 5,
            updatedAt: 50,
            path: "/sessions/unassigned.jsonl"
        ),
    ]

    try expect(
        KSFProjectDashboardBuilder.localTaskCandidateIDs(
            catalog: [alpha, beta],
            threads: threads,
            projections: ["projected": projection]
        ),
        ["projected", "mapped"],
        "local candidate discovery stays top-level and conservatively project-bound"
    )
}

private func testProjectTokenAccounting() throws {
    let fileManager = FileManager.default
    let root = fileManager.temporaryDirectory.appendingPathComponent("project-token-\(UUID().uuidString)")
    try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? fileManager.removeItem(at: root) }

    let parentPath = root.appendingPathComponent("parent.jsonl")
    let childPath = root.appendingPathComponent("child.jsonl")
    let parentLines = [
        #"{"timestamp":"2026-08-29T23:50:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100}}}}"#,
        #"{"timestamp":"2026-08-30T00:10:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":150}}}}"#,
        #"{"timestamp":"2026-08-30T00:20:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":210}}}}"#,
        #"{"timestamp":"2026-08-30T00:30:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":260}}}}"#,
    ]
    let childLines = [
        #"{"timestamp":"2026-08-30T00:24:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":40}}}}"#,
        #"{"timestamp":"2026-08-30T00:35:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":70}}}}"#,
    ]
    try Data((parentLines.joined(separator: "\n") + "\n").utf8).write(to: parentPath)
    try Data((childLines.joined(separator: "\n") + "\n").utf8).write(to: childPath)

    let formatter = ISO8601DateFormatter()
    let alphaBinding = formatter.date(from: "2026-08-30T00:00:00Z")!
    let betaBinding = formatter.date(from: "2026-08-30T00:25:00Z")!
    let now = formatter.date(from: "2026-08-30T12:00:00Z")!
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(secondsFromGMT: 0)!
    let threads = [
        CodexThreadMetadata(id: "parent", cwd: "/git", createdAt: 1_700_000_000, updatedAt: 1, path: parentPath.path),
        CodexThreadMetadata(id: "child", cwd: "/git", parentThreadId: "parent", createdAt: Int64(alphaBinding.timeIntervalSince1970), updatedAt: 1, path: childPath.path),
    ]
    let usage = ProjectTokenUsageReader().read(
        projectIDs: ["alpha", "beta"],
        threads: threads,
        timelines: [ProjectTaskTimeline(threadID: "parent", transitions: [
            ProjectBindingTransition(projectID: "alpha", boundAt: alphaBinding),
            ProjectBindingTransition(projectID: "beta", boundAt: betaBinding),
        ])],
        trackingStartedAt: alphaBinding,
        now: now,
        calendar: calendar
    )
    try expect(usage["alpha"]?.cumulativeTokens, 150, "bound parent deltas and inherited child usage stay with alpha before switch")
    try expect(usage["beta"]?.cumulativeTokens, 80, "post-switch parent and child deltas move to beta")
    try expect(usage["alpha"]?.todayTokens, 150, "today total follows local calendar")
}

private func testActionManifestSecurity() throws {
    let fileManager = FileManager.default
    let root = fileManager.temporaryDirectory.appendingPathComponent("project-actions-\(UUID().uuidString)")
    try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? fileManager.removeItem(at: root) }
    let mapping = KSFEngineeringMapping(id: "git", role: "主工程", rootPath: root.path)
    let manifest = root.appendingPathComponent(ProjectActionManifestLoader.fileName)
    let valid = #"{"protocol":"codex-usage-bar-actions-v1","actions":[{"id":"dev","title":"启动","symbol":"play.fill","primary":true,"executable":"/usr/bin/env","arguments":["swift","run"],"workingDirectory":"."}]}"#
    try Data(valid.utf8).write(to: manifest)
    try fileManager.setAttributes([.posixPermissions: NSNumber(value: Int16(0o600))], ofItemAtPath: manifest.path)
    let actions = try ProjectActionManifestLoader().load(mapping: mapping)
    try expect(actions.count, 1, "valid structured action loads")
    try expect(actions[0].workingDirectory, root.resolvingSymlinksInPath().path, "working directory is resolved inside root")

    let traversal = #"{"protocol":"codex-usage-bar-actions-v1","actions":[{"id":"bad","title":"越界","executable":"/bin/echo","arguments":[],"workingDirectory":"../"}]}"#
    try Data(traversal.utf8).write(to: manifest)
    do {
        _ = try ProjectActionManifestLoader().load(mapping: mapping)
        throw TestFailure(description: "path traversal was accepted")
    } catch ProjectActionManifestError.workingDirectoryOutsideRoot {
        // Expected.
    }

    try Data(valid.utf8).write(to: manifest)
    try fileManager.setAttributes([.posixPermissions: NSNumber(value: Int16(0o666))], ofItemAtPath: manifest.path)
    do {
        _ = try ProjectActionManifestLoader().load(mapping: mapping)
        throw TestFailure(description: "group/world-writable manifest was accepted")
    } catch ProjectActionManifestError.invalidPermissions {
        // Expected.
    }
}

private func testProjectTaskBootstrapPrompt() throws {
    let project = KSFProject(
        id: "project-card",
        name: "家庭网络",
        cardPath: "/Users/test/KSF/10项目/家庭网络/项目记忆卡.md",
        projectDirectory: "/Users/test/KSF/10项目/家庭网络",
        engineeringMappings: [
            KSFEngineeringMapping(
                id: "server",
                role: "服务端",
                rootPath: "/Users/test/Git/home-network-server"
            ),
            KSFEngineeringMapping(
                id: "app",
                role: "客户端",
                rootPath: "/Users/test/Git/home-network-app"
            ),
        ]
    )
    let bootstrap = try ProjectTaskBootstrap.prepare(
        project: project,
        ksfRootPath: "/Users/test/KSF"
    )
    let prompt = bootstrap.prompt
    guard bootstrap.cwd == "/Users/test/KSF",
          bootstrap.name == "家庭网络 · 新任务",
          prompt.contains("KSF 项目「家庭网络」"),
          prompt.contains(project.cardPath),
          prompt.contains("按 KSF 规范加载"),
          prompt.contains("不要开始具体工作"),
          prompt.contains("不要修改文件"),
          prompt.contains("不要生成实施方案"),
          prompt.contains("等待用户下一步指令"),
          !prompt.contains("home-network-server"),
          !prompt.contains("home-network-app"),
          prompt.count < 300 else {
        throw TestFailure(description: "bootstrap prompt lost its project context or stop boundary")
    }

    do {
        _ = try ProjectTaskBootstrap.prepare(
            project: project,
            ksfRootPath: "relative/KSF"
        )
        throw TestFailure(description: "relative KSF path was accepted")
    } catch ProjectTaskBootstrapError.invalidPath {
        // Expected.
    }

    let invalidName = KSFProject(
        id: "invalid",
        name: "家庭\n网络",
        cardPath: project.cardPath,
        projectDirectory: project.projectDirectory
    )
    do {
        _ = try ProjectTaskBootstrap.prepare(
            project: invalidName,
            ksfRootPath: "/Users/test/KSF"
        )
        throw TestFailure(description: "control characters in the project name were accepted")
    } catch ProjectTaskBootstrapError.invalidProjectName {
        // Expected.
    }
}

@main
private enum ProjectStandaloneTestRunner {
    static func main() {
        let tests: [(String, () throws -> Void)] = [
            ("project aggregation", testProjectAggregation),
            ("local task candidate selection", testLocalTaskCandidateSelection),
            ("project token accounting", testProjectTokenAccounting),
            ("action manifest security", testActionManifestSecurity),
            ("project task bootstrap prompt", testProjectTaskBootstrapPrompt),
        ]
        do {
            for (name, test) in tests {
                try test()
                print("PASS \(name)")
            }
            print("PASS \(tests.count) project workbench tests")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
