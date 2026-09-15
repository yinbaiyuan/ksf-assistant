import XCTest
@testable import KSFAssistantCore

final class ProjectWorkbenchTests: XCTestCase {
    func testConnectedCompletedTaskKeepsUnpinnedContainersVisible() {
        let task = ProjectTaskItem(threadID: "linked", hostID: "local", name: "已完成", classification: .completed, route: nil, createdAt: Date(), projectID: "group")
        let keys: Set<String> = [TaskLinkIdentity.taskKey(for: "linked")]
        let group = ProjectDashboardItem(id: "group", project: nil, kind: .unassigned, isPinned: false, tasks: [task])
        XCTAssertEqual(KSFProjectWorkset.select(from: [group], connectedTaskKeys: keys).count, 1)
        XCTAssertTrue(KSFProjectWorkset.select(from: [group]).isEmpty)
        let workspace = CodexWorkspaceItem(id: "group", kind: "other", name: "其他任务", tasks: [task])
        XCTAssertEqual(CodexWorkspaceWorkset.select(from: [workspace], connectedTaskKeys: keys).count, 1)
        XCTAssertTrue(CodexWorkspaceWorkset.select(from: [workspace]).isEmpty)
    }
    func testPinnedUnassignedHasNoDuplicateUnavailableProject() {
        let id = KSFProjectDashboardBuilder.unassignedProjectID
        let items = KSFProjectDashboardBuilder.build(
            catalog: [], activeTasks: [], threads: [], projections: [:],
            pinnedProjectIDs: [id], usage: [:], launchActions: [:]
        )
        XCTAssertEqual(items.count, 1)
        XCTAssertEqual(items.first?.kind, .unassigned)
        XCTAssertEqual(items.first?.isPinned, true)
        XCTAssertEqual(items.first?.tasks.count, 0)
    }
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
            CodexTaskObservation(id: "loose", hostID: "local", runtimeStatus: .active),
            CodexTaskObservation(
                id: "orphaned-route",
                hostID: "local",
                runtimeStatus: .active,
                activeFlags: [.waitingOnUserInput]
            ),
            CodexTaskObservation(id: "child", hostID: "local", agentNickname: "helper", runtimeStatus: .active),
            CodexTaskObservation(id: "metadata-child", hostID: "local", runtimeStatus: .active),
        ]
        let threads = [
            CodexThreadMetadata(id: "run", name: "实现列表", cwd: "/git/alpha", createdAt: 1, updatedAt: 10),
            CodexThreadMetadata(id: "wait", name: "确认权限", cwd: "/unrelated", createdAt: 2, updatedAt: 20),
            CodexThreadMetadata(id: "loose", name: "临时任务", cwd: "/unrelated", createdAt: 3, updatedAt: 30),
            CodexThreadMetadata(id: "orphaned-route", name: "项目已移除", cwd: "/unrelated", createdAt: 4, updatedAt: 40),
            CodexThreadMetadata(id: "child", cwd: "/git/alpha", createdAt: 1, updatedAt: 30),
            CodexThreadMetadata(id: "metadata-child", cwd: "/git/alpha", parentThreadId: "run", createdAt: 5, updatedAt: 50),
        ]
        let orphanedProjection = KSFTaskProjection(
            threadKey: "opaque-orphan",
            bindings: [KSFTaskBinding(projectCard: "removed", boundAt: "2026-08-30T00:00:00Z", route: route)],
            updatedAt: "2026-08-30T00:00:00Z"
        )
        let alphaLaunch = ProjectLaunchAction(
            projectID: "alpha",
            scriptPath: "/ksf/alpha/start.sh",
            workingDirectory: "/ksf/alpha"
        )

        let items = KSFProjectDashboardBuilder.build(
            catalog: [alpha, beta],
            activeTasks: tasks,
            threads: threads,
            projections: ["wait": projection, "orphaned-route": orphanedProjection],
            pinnedProjectIDs: ["missing"],
            usage: [:],
            launchActions: ["alpha": alphaLaunch]
        )
        XCTAssertEqual(items.map(\.id), [KSFProjectDashboardBuilder.unassignedProjectID, "missing", "alpha", "beta"])
        XCTAssertEqual(items[0].kind, .unassigned)
        XCTAssertEqual(items[0].runningCount, 1)
        XCTAssertEqual(items[0].waitingCount, 1)
        XCTAssertEqual(items[0].tasks.map(\.name), ["临时任务", "项目已移除"])
        XCTAssertEqual(items[0].tasks.last?.route?.mainJob?.name, "高级软件工程师")
        XCTAssertFalse(items[1].isAvailable)
        XCTAssertEqual(items[1].kind, .unavailablePinned)
        XCTAssertEqual(items[2].runningCount, 1)
        XCTAssertEqual(items[2].tasks.map(\.name), ["实现列表"])
        XCTAssertEqual(items[2].preferredEngineeringID, "git")
        XCTAssertEqual(items[2].launchAction, alphaLaunch)
        XCTAssertEqual(items[3].waitingCount, 1)
        XCTAssertEqual(items[3].runningCount, 0)
        XCTAssertEqual(items[3].tasks.first?.waitingReason, .approval)
        XCTAssertEqual(items[3].tasks.first?.route?.mainJob?.name, "高级软件工程师")

        let idle = ProjectDashboardItem(id: "idle", project: project("idle", root: "/git/idle"), isPinned: false)
        XCTAssertEqual(
            KSFProjectWorkset.select(from: items + [idle]).map(\.id),
            [KSFProjectDashboardBuilder.unassignedProjectID, "missing", "alpha", "beta"]
        )

        let nested = KSFProjectDashboardBuilder.build(
            catalog: [alpha],
            activeTasks: [CodexTaskObservation(id: "nested", hostID: "local", runtimeStatus: .active)],
            threads: [CodexThreadMetadata(id: "nested", cwd: "/git/alpha/subdir", createdAt: 1, updatedAt: 1)],
            projections: [:],
            pinnedProjectIDs: [],
            usage: [:],
            launchActions: [:]
        )
        XCTAssertEqual(nested.map(\.kind), [.unassigned])
        XCTAssertEqual(nested.first?.tasks.map(\.threadID), ["nested"])

        let catalogOnly = KSFProjectDashboardBuilder.build(
            catalog: [alpha],
            activeTasks: [],
            threads: [],
            projections: [:],
            pinnedProjectIDs: [],
            usage: [:],
            launchActions: [:]
        )
        XCTAssertTrue(catalogOnly.isEmpty)
    }

    func testCompletedProjectTaskRemainsVisibleAndReopenable() {
        let alpha = project("alpha", root: "/git/alpha")
        let completed = CodexThreadMetadata(
            id: "completed",
            name: "已完成任务",
            cwd: "/git/alpha",
            createdAt: 1,
            updatedAt: 20,
            path: "/sessions/completed.jsonl"
        )

        let items = KSFProjectDashboardBuilder.build(
            catalog: [alpha],
            activeTasks: [],
            threads: [completed],
            projections: [:],
            pinnedProjectIDs: [],
            usage: [:],
            launchActions: [:]
        )

        XCTAssertEqual(items.map(\.id), ["alpha"])
        XCTAssertEqual(items.first?.tasks.map(\.threadID), ["completed"])
        XCTAssertEqual(items.first?.tasks.first?.classification, .completed)
        XCTAssertEqual(items.first?.activeTaskCount, 0)
        XCTAssertTrue(KSFProjectWorkset.select(from: items).isEmpty)

        let pinned = ProjectDashboardItem(
            id: "alpha",
            project: alpha,
            isPinned: true,
            tasks: items[0].tasks
        )
        XCTAssertEqual(KSFProjectWorkset.select(from: [pinned]).map(\.id), ["alpha"])
    }

    func testRoutePresentationGroupsAbilitiesAndSkillsWithoutDroppingOrphans() {
        let route = KSFRouteSummary(
            category: KSFRouteCategory(name: "产品研发", validationStatus: "草案"),
            jobs: [
                KSFRouteJob(jobID: "product", name: "高级产品经理", role: "main", validationStatus: "待验证"),
                KSFRouteJob(jobID: "interaction", name: "交互设计师", role: "collaborator", validationStatus: "草案"),
            ],
            abilities: [
                KSFRouteAbility(abilityID: "rules", name: "范围与规则", jobID: "product", validationStatus: "待验证"),
                KSFRouteAbility(abilityID: "flow", name: "主流程", jobID: "interaction", validationStatus: "草案"),
                KSFRouteAbility(abilityID: "orphan-ability", name: "待归属能力", jobID: "missing"),
            ],
            dispatchableSkills: [
                KSFDispatchableSkill(skillID: "rules-check", skillStage: "active", abilityID: "rules"),
                KSFDispatchableSkill(skillID: "flow-check", skillStage: "trial", abilityID: "flow"),
                KSFDispatchableSkill(skillID: "orphan-skill", skillStage: "trial", abilityID: "missing"),
            ]
        )

        let presentation = KSFRoutePresentation(route: route)

        XCTAssertEqual(presentation.category?.name, "产品研发")
        XCTAssertEqual(presentation.jobGroups.map { $0.job?.name }, ["高级产品经理", "交互设计师", nil])
        XCTAssertEqual(presentation.jobGroups[0].abilities[0].skills.map(\.skillID), ["rules-check"])
        XCTAssertEqual(presentation.jobGroups[1].abilities[0].skills.map(\.skillID), ["flow-check"])
        XCTAssertEqual(presentation.jobGroups[2].abilities.map { $0.ability.name }, ["待归属能力"])
        XCTAssertEqual(presentation.unassignedSkills.map(\.skillID), ["orphan-skill"])
    }

    func testProjectListKeepsStableOrderAcrossActivityUpdates() {
        let stableOrder = KSFProjectListOrdering.reconcile(
            previous: ["alpha", "pinned", "beta"],
            candidates: ["gamma", "alpha"]
        )
        XCTAssertEqual(stableOrder, ["alpha", "pinned", "beta", "gamma"])

        let initial = [
            ProjectDashboardItem(id: KSFProjectDashboardBuilder.unassignedProjectID, project: nil, kind: .unassigned, isPinned: false, tasks: [task("loose", .running)]),
            ProjectDashboardItem(id: "beta", project: nil, isPinned: false, tasks: [task("beta", .running)], latestActivity: Date(timeIntervalSince1970: 30)),
            ProjectDashboardItem(id: "pinned", project: nil, isPinned: true),
            ProjectDashboardItem(id: "alpha", project: nil, isPinned: false, tasks: [task("alpha", .waiting)], latestActivity: Date(timeIntervalSince1970: 10)),
            ProjectDashboardItem(id: "gamma", project: nil, isPinned: false),
        ]
        XCTAssertEqual(
            KSFProjectListOrdering.sort(initial, stableOrder: stableOrder).map(\.id),
            [KSFProjectDashboardBuilder.unassignedProjectID, "pinned", "alpha", "beta", "gamma"]
        )

        let activityChanged = [
            ProjectDashboardItem(id: KSFProjectDashboardBuilder.unassignedProjectID, project: nil, kind: .unassigned, isPinned: false, tasks: [task("loose", .waiting)]),
            ProjectDashboardItem(id: "gamma", project: nil, isPinned: false, tasks: [task("gamma", .running)], latestActivity: Date(timeIntervalSince1970: 50)),
            ProjectDashboardItem(id: "alpha", project: nil, isPinned: false, tasks: [task("alpha", .running)], latestActivity: Date(timeIntervalSince1970: 60)),
            ProjectDashboardItem(id: "beta", project: nil, isPinned: false, tasks: [task("beta", .waiting)], latestActivity: Date(timeIntervalSince1970: 70)),
            ProjectDashboardItem(id: "pinned", project: nil, isPinned: true, tasks: [task("pinned", .running)]),
        ]
        XCTAssertEqual(
            KSFProjectListOrdering.sort(activityChanged, stableOrder: stableOrder).map(\.id),
            [KSFProjectDashboardBuilder.unassignedProjectID, "pinned", "alpha", "beta", "gamma"]
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
                    kind: item.kind,
                    isPinned: item.id == "pinned" || item.id == "beta",
                    tasks: item.tasks,
                    latestActivity: item.latestActivity
                )
            }, stableOrder: pinnedOrder).map(\.id),
            [KSFProjectDashboardBuilder.unassignedProjectID, "pinned", "beta", "alpha", "gamma"]
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

    func testProjectStartScriptResolverRequiresSafeExecutableProjectScript() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let project = KSFProject(
            id: "project",
            name: "项目",
            cardPath: root.appendingPathComponent("项目记忆卡.md").path,
            projectDirectory: root.path
        )
        let resolver = ProjectStartScriptResolver()
        XCTAssertNil(try resolver.load(project: project))

        let script = root.appendingPathComponent(ProjectStartScriptResolver.fileName)
        try Data("#!/bin/zsh\nexit 0\n".utf8).write(to: script)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: script.path)
        let action = try XCTUnwrap(resolver.load(project: project))
        XCTAssertEqual(action.scriptPath, script.path)
        XCTAssertEqual(action.workingDirectory, root.path)
        XCTAssertNoThrow(try resolver.validate(action))

        try FileManager.default.setAttributes([.posixPermissions: 0o722], ofItemAtPath: script.path)
        XCTAssertThrowsError(try resolver.load(project: project)) { error in
            XCTAssertEqual(error as? ProjectStartScriptError, .invalidPermissions)
        }

        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: script.path)
        XCTAssertThrowsError(try resolver.load(project: project)) { error in
            XCTAssertEqual(error as? ProjectStartScriptError, .notExecutable)
        }

        try FileManager.default.removeItem(at: script)
        let target = root.appendingPathComponent("actual.sh")
        try Data("#!/bin/zsh\nexit 0\n".utf8).write(to: target)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: target.path)
        try FileManager.default.createSymbolicLink(at: script, withDestinationURL: target)
        XCTAssertThrowsError(try resolver.load(project: project)) { error in
            XCTAssertEqual(error as? ProjectStartScriptError, .invalidScript)
        }
        XCTAssertThrowsError(try resolver.validate(action)) { error in
            XCTAssertEqual(error as? ProjectStartScriptError, .invalidScript)
        }
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

    func testArchiveTaskBootstrapNamesAndScopesTheArchiveWork() throws {
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
            ]
        )

        let bootstrap = try ProjectTaskBootstrap.prepare(
            project: project,
            ksfRootPath: "/Users/test/KSF",
            purpose: .archiveProject
        )
        let prompt = bootstrap.prompt

        XCTAssertEqual(bootstrap.cwd, "/Users/test/KSF")
        XCTAssertEqual(bootstrap.name, "家庭网络 · 归档项目")
        XCTAssertTrue(prompt.contains("KSF 项目「家庭网络」的归档任务"))
        XCTAssertTrue(prompt.contains(project.cardPath))
        XCTAssertTrue(prompt.contains("按 KSF 规范"))
        XCTAssertTrue(prompt.contains("归档当前项目"))
        XCTAssertTrue(prompt.contains("归档前置条件"))
        XCTAssertTrue(prompt.contains("必须由用户确认"))
        XCTAssertTrue(prompt.contains("不要绕过 KSF"))
        XCTAssertFalse(prompt.contains("本轮只做上下文准备"))
        XCTAssertFalse(prompt.contains("home-network-server"))
        XCTAssertLessThan(prompt.count, 360)
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
