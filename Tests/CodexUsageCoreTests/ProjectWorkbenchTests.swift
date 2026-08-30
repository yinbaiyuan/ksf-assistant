import XCTest
@testable import CodexUsageCore

final class ProjectWorkbenchTests: XCTestCase {
    func testWaitingRunningPinnedAndExactCWD() {
        let alpha = project("alpha", root: "/git/alpha")
        let beta = project("beta", root: "/git/beta")
        let route = KSFRouteSummary(
            category: KSFRouteCategory(categoryID: "product", name: "产品研发"),
            jobs: [KSFRouteJob(jobID: "engineer", name: "高级软件工程师", role: "main")],
            abilities: [KSFRouteAbility(abilityID: "debug", name: "小步实现与调试")],
            dispatchableSkills: [KSFDispatchableSkill(skillID: "test", skillStage: "trial")]
        )
        let projection = KSFTaskProjection(
            threadKey: "opaque",
            bindings: [KSFTaskBinding(projectCard: "beta", boundAt: "2026-08-30T00:00:00Z", route: route)],
            updatedAt: "2026-08-30T00:00:00Z"
        )
        let tasks = [
            CodexTaskObservation(id: "run", hostID: "local", runtimeStatus: .active),
            CodexTaskObservation(id: "run", hostID: "local", runtimeStatus: .active),
            CodexTaskObservation(
                id: "wait",
                hostID: "local",
                runtimeStatus: .active,
                activeFlags: [.waitingOnApproval]
            ),
            CodexTaskObservation(id: "child", hostID: "local", agentNickname: "helper", runtimeStatus: .active),
        ]
        let threads = [
            CodexThreadMetadata(id: "run", name: "实现列表", cwd: "/git/alpha", createdAt: 1, updatedAt: 10),
            CodexThreadMetadata(id: "wait", name: "确认权限", cwd: "/unrelated", createdAt: 2, updatedAt: 20),
            CodexThreadMetadata(id: "child", cwd: "/git/alpha", createdAt: 1, updatedAt: 30),
        ]

        let items = KSFProjectDashboardBuilder.build(
            catalog: [alpha, beta],
            activeTasks: tasks,
            threads: threads,
            projections: ["wait": projection],
            pinnedProjectIDs: ["missing"],
            selectedProjectID: "alpha",
            usage: [:],
            actions: [:]
        )
        XCTAssertEqual(items.map(\.id), ["missing", "alpha", "beta"])
        XCTAssertFalse(items[0].isAvailable)
        XCTAssertEqual(items[1].runningCount, 1)
        XCTAssertEqual(items[1].tasks.map(\.name), ["实现列表"])
        XCTAssertEqual(items[1].preferredEngineeringID, "git")
        XCTAssertEqual(items[2].waitingCount, 1)
        XCTAssertEqual(items[2].runningCount, 0)
        XCTAssertEqual(items[2].tasks.first?.waitingReason, .approval)
        XCTAssertEqual(items[2].tasks.first?.route?.mainJob?.name, "高级软件工程师")
        XCTAssertEqual(items[2].primaryRoute?.mainJob?.name, "高级软件工程师")

        let idle = ProjectDashboardItem(id: "idle", project: project("idle", root: "/git/idle"), isPinned: false)
        XCTAssertEqual(
            KSFProjectWorkset.select(from: items + [idle]).map(\.id),
            ["missing", "alpha", "beta"]
        )

        let nested = KSFProjectDashboardBuilder.build(
            catalog: [alpha],
            activeTasks: [CodexTaskObservation(id: "nested", hostID: "local", runtimeStatus: .active)],
            threads: [CodexThreadMetadata(id: "nested", cwd: "/git/alpha/subdir", createdAt: 1, updatedAt: 1)],
            projections: [:],
            pinnedProjectIDs: [],
            usage: [:],
            actions: [:]
        )
        XCTAssertTrue(nested.isEmpty)
    }

    func testProjectListKeepsStableOrderAcrossActivityUpdates() {
        let stableOrder = KSFProjectListOrdering.reconcile(
            previous: ["alpha", "pinned", "beta"],
            candidates: ["gamma", "alpha"]
        )
        XCTAssertEqual(stableOrder, ["alpha", "pinned", "beta", "gamma"])

        let initial = [
            ProjectDashboardItem(id: "beta", project: nil, isPinned: false, tasks: [task("beta", .running)], latestActivity: Date(timeIntervalSince1970: 30)),
            ProjectDashboardItem(id: "pinned", project: nil, isPinned: true),
            ProjectDashboardItem(id: "alpha", project: nil, isPinned: false, tasks: [task("alpha", .waiting)], latestActivity: Date(timeIntervalSince1970: 10)),
            ProjectDashboardItem(id: "gamma", project: nil, isPinned: false),
        ]
        XCTAssertEqual(
            KSFProjectListOrdering.sort(initial, stableOrder: stableOrder).map(\.id),
            ["pinned", "alpha", "beta", "gamma"]
        )

        let activityChanged = [
            ProjectDashboardItem(id: "gamma", project: nil, isPinned: false, tasks: [task("gamma", .running)], latestActivity: Date(timeIntervalSince1970: 50)),
            ProjectDashboardItem(id: "alpha", project: nil, isPinned: false, tasks: [task("alpha", .running)], latestActivity: Date(timeIntervalSince1970: 60)),
            ProjectDashboardItem(id: "beta", project: nil, isPinned: false, tasks: [task("beta", .waiting)], latestActivity: Date(timeIntervalSince1970: 70)),
            ProjectDashboardItem(id: "pinned", project: nil, isPinned: true, tasks: [task("pinned", .running)]),
        ]
        XCTAssertEqual(
            KSFProjectListOrdering.sort(activityChanged, stableOrder: stableOrder).map(\.id),
            ["pinned", "alpha", "beta", "gamma"]
        )

        let pinnedOrder = KSFProjectListOrdering.moving(
            "beta",
            in: stableOrder,
            toPinned: true,
            pinnedProjectIDs: ["pinned", "beta"]
        )
        XCTAssertEqual(
            KSFProjectListOrdering.sort(activityChanged.map { item in
                ProjectDashboardItem(
                    id: item.id,
                    project: item.project,
                    isPinned: item.id == "pinned" || item.id == "beta",
                    tasks: item.tasks,
                    latestActivity: item.latestActivity
                )
            }, stableOrder: pinnedOrder).map(\.id),
            ["pinned", "beta", "alpha", "gamma"]
        )
    }

    func testLocalTaskCandidatesAreTopLevelLocalAndConservativelyAssigned() {
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

        XCTAssertEqual(
            KSFProjectDashboardBuilder.localTaskCandidateIDs(
                catalog: [alpha, beta],
                threads: threads,
                projections: ["projected": projection]
            ),
            ["projected", "mapped"]
        )
    }

    func testWaitingReasonPriorityTaskOrderingAndDeepLinkEncoding() throws {
        let mixed = CodexTaskObservation(
            id: "mixed",
            hostID: "local",
            runtimeStatus: .active,
            activeFlags: [.waitingOnApproval, .waitingOnUserInput],
            pendingRequestMethods: ["requestOptionPicker"],
            hasPendingPlanImplementation: true
        )
        XCTAssertEqual(TaskActivityClassifier.waitingReason(for: mixed), .approval)
        XCTAssertEqual(
            TaskActivityClassifier.waitingReason(for: CodexTaskObservation(
                id: "plan",
                hostID: "local",
                runtimeStatus: .idle,
                activeFlags: [.waitingOnUserInput],
                hasPendingPlanImplementation: true
            )),
            .planConfirmation
        )
        XCTAssertEqual(
            TaskActivityClassifier.waitingReason(for: CodexTaskObservation(
                id: "reply",
                hostID: "local",
                runtimeStatus: .idle,
                pendingRequestMethods: ["requestUserInfo"]
            )),
            .userInput
        )
        XCTAssertEqual(
            TaskActivityClassifier.waitingReason(for: CodexTaskObservation(
                id: "action",
                hostID: "local",
                runtimeStatus: .idle,
                pendingRequestMethods: ["customActionRequest"]
            )),
            .actionRequired
        )

        let initial = [
            task("later", .running, createdAt: 20),
            task("earlier", .waiting, createdAt: 10),
        ]
        let firstOrder = ProjectTaskListOrdering.reconcile(previous: [], candidates: initial)
        XCTAssertEqual(firstOrder, ["local:earlier", "local:later"])
        let changed = [
            task("later", .waiting, createdAt: 20),
            task("new", .running, createdAt: 5),
            task("earlier", .running, createdAt: 10),
        ]
        let nextOrder = ProjectTaskListOrdering.reconcile(previous: firstOrder, candidates: changed)
        XCTAssertEqual(nextOrder, ["local:earlier", "local:later", "local:new"])
        XCTAssertEqual(
            ProjectTaskListOrdering.sort(changed, stableOrder: nextOrder).map(\.threadID),
            ["earlier", "later", "new"]
        )

        XCTAssertEqual(
            try CodexTaskDeepLink.url(for: "task/with space").absoluteString,
            "codex://threads/task%2Fwith%20space"
        )
        XCTAssertThrowsError(try CodexTaskDeepLink.url(for: "bad\nidentifier"))
    }

    func testProjectTokenSwitchAndChildInheritance() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let parent = root.appendingPathComponent("parent.jsonl")
        let child = root.appendingPathComponent("child.jsonl")
        try writeSamples([
            ("2026-08-29T23:50:00Z", 100),
            ("2026-08-30T00:10:00Z", 150),
            ("2026-08-30T00:20:00Z", 210),
            ("2026-08-30T00:30:00Z", 260),
        ], to: parent)
        try writeSamples([
            ("2026-08-30T00:24:00Z", 40),
            ("2026-08-30T00:35:00Z", 70),
        ], to: child)

        let formatter = ISO8601DateFormatter()
        let alpha = formatter.date(from: "2026-08-30T00:00:00Z")!
        let beta = formatter.date(from: "2026-08-30T00:25:00Z")!
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(secondsFromGMT: 0)!
        let usage = ProjectTokenUsageReader().read(
            projectIDs: ["alpha", "beta"],
            threads: [
                CodexThreadMetadata(id: "parent", cwd: "/git", createdAt: 1_700_000_000, updatedAt: 1, path: parent.path),
                CodexThreadMetadata(id: "child", cwd: "/git", parentThreadId: "parent", createdAt: Int64(alpha.timeIntervalSince1970), updatedAt: 1, path: child.path),
            ],
            timelines: [ProjectTaskTimeline(threadID: "parent", transitions: [
                ProjectBindingTransition(projectID: "alpha", boundAt: alpha),
                ProjectBindingTransition(projectID: "beta", boundAt: beta),
            ])],
            trackingStartedAt: alpha,
            now: formatter.date(from: "2026-08-30T12:00:00Z")!,
            calendar: calendar
        )
        XCTAssertEqual(usage["alpha"]?.cumulativeTokens, 150)
        XCTAssertEqual(usage["beta"]?.cumulativeTokens, 80)
    }

    func testActionManifestRejectsTraversalAndUnsafePermissions() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let mapping = KSFEngineeringMapping(id: "git", role: "主工程", rootPath: root.path)
        let manifest = root.appendingPathComponent(ProjectActionManifestLoader.fileName)
        let valid = #"{"protocol":"codex-usage-bar-actions-v1","actions":[{"id":"dev","title":"启动","executable":"/usr/bin/env","arguments":["swift","run"],"workingDirectory":"."}]}"#
        try Data(valid.utf8).write(to: manifest)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: manifest.path)
        XCTAssertEqual(try ProjectActionManifestLoader().load(mapping: mapping).count, 1)

        let traversal = #"{"protocol":"codex-usage-bar-actions-v1","actions":[{"id":"bad","title":"越界","executable":"/bin/echo","workingDirectory":"../"}]}"#
        try Data(traversal.utf8).write(to: manifest)
        XCTAssertThrowsError(try ProjectActionManifestLoader().load(mapping: mapping))
        try Data(valid.utf8).write(to: manifest)
        try FileManager.default.setAttributes([.posixPermissions: 0o666], ofItemAtPath: manifest.path)
        XCTAssertThrowsError(try ProjectActionManifestLoader().load(mapping: mapping))
    }

    func testNewTaskBootstrapPromptLoadsContextAndStopsBeforeWork() throws {
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

        XCTAssertEqual(bootstrap.cwd, "/Users/test/KSF")
        XCTAssertEqual(bootstrap.name, "家庭网络 · 新任务")
        XCTAssertTrue(prompt.contains("KSF 项目「家庭网络」"))
        XCTAssertTrue(prompt.contains(project.cardPath))
        XCTAssertTrue(prompt.contains("按 KSF 规范加载"))
        XCTAssertTrue(prompt.contains("不要开始具体工作"))
        XCTAssertTrue(prompt.contains("不要修改文件"))
        XCTAssertTrue(prompt.contains("不要生成实施方案"))
        XCTAssertTrue(prompt.contains("等待用户下一步指令"))
        XCTAssertFalse(prompt.contains("home-network-server"))
        XCTAssertFalse(prompt.contains("home-network-app"))
        XCTAssertLessThan(prompt.count, 300)

        XCTAssertThrowsError(try ProjectTaskBootstrap.prepare(
            project: project,
            ksfRootPath: "relative/KSF"
        ))
        let invalidName = KSFProject(
            id: "invalid",
            name: "家庭\n网络",
            cardPath: project.cardPath,
            projectDirectory: project.projectDirectory
        )
        XCTAssertThrowsError(try ProjectTaskBootstrap.prepare(
            project: invalidName,
            ksfRootPath: "/Users/test/KSF"
        ))
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

    private func task(
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

    private func writeSamples(_ samples: [(String, Int)], to url: URL) throws {
        let lines = samples.map { timestamp, total in
            #"{"timestamp":"\#(timestamp)","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":\#(total)}}}}"#
        }
        try Data((lines.joined(separator: "\n") + "\n").utf8).write(to: url)
    }
}
