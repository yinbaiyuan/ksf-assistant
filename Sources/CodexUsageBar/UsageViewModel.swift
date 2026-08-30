import AppKit
import CodexUsageCore
import Foundation

enum UsageDisplayStatus: Equatable {
    case loading
    case available
    case stale
    case codexMissing
    case authenticationRequired
    case unsupportedProtocol
    case offline
}

@MainActor
final class UsageViewModel: ObservableObject {
    @Published private(set) var snapshot: UsageSnapshot?
    @Published private(set) var status: UsageDisplayStatus
    @Published private(set) var isRefreshing = false
    @Published private(set) var lastErrorMessage: String?
    @Published private(set) var tokenErrorMessage: String?
    @Published private(set) var taskActivity = TaskActivitySnapshot(availability: .loading)
    @Published private(set) var projectDashboard = ProjectDashboardSnapshot()
    @Published private(set) var selectedProjectID: String?
    @Published private(set) var pinnedProjectIDs: Set<String>
    @Published private(set) var isRefreshingProjects = false
    @Published private(set) var projectActionError: String?
    @Published private(set) var taskOpenFailure: ProjectTaskOpenFailure?
    @Published private(set) var creatingProjectTaskIDs: Set<String> = []
    @Published private(set) var projectTaskCreationErrors: [String: String] = [:]
    @Published private(set) var ksfRootPath: String
    @Published private(set) var notificationPermission: NotificationPermissionState = .unknown
    @Published private(set) var loginItemState: LoginItemState = .disabled
    @Published private(set) var weChatState: WeChatConnectionState = .disconnected
    @Published private(set) var weChatQRCodeContent: String?
    @Published private(set) var weChatActionInProgress = false
    @Published private(set) var weChatFeedback: String?
    @Published var launchAtLoginEnabled: Bool
    @Published var resetNotificationsEnabled: Bool

    private let store = SnapshotStore()
    private let defaults = UserDefaults.standard
    private let notifications = ResetNotificationController()
    private let loginItems = LoginItemController()
    private let taskActivityProvider: CodexTaskActivityProviding
    private let bridge = KSFBridgeClient()
    private let projectUsageStore = ProjectUsageStore()
    private let actionLauncher = TerminalActionLauncher()
    private let taskOpener: CodexTaskOpening = WorkspaceCodexTaskOpener()
    private let taskSubmissionClient: CodexDesktopTaskSubmissionClient
    private let weChatConnector = WeChatConnector()
    private var client: CodexAppServerClient?
    private var started = false
    private var refreshingRateLimits = false
    private var refreshingTokenUsage = false
    private var refreshActivityDepth = 0
    private var rateTimerTask: Task<Void, Never>?
    private var accountTokenTimerTask: Task<Void, Never>?
    private var activeLocalTokenTimerTask: Task<Void, Never>?
    private var localTokenReadTask: Task<DailyUsageBucket?, Never>?
    private var retryTask: Task<Void, Never>?
    private var taskActivityUpdateTask: Task<Void, Never>?
    private var projectRefreshTask: Task<Void, Never>?
    private var weChatStateTask: Task<Void, Never>?
    private var weChatCommandTask: Task<Void, Never>?
    private var refreshingProjects = false
    private var bridgeEnabledAt: Date?
    private var projectCatalog: [KSFProject] = []
    private var projectThreads: [CodexThreadMetadata] = []
    private var projectTaskObservations: [CodexTaskObservation] = []
    private var projectProjections: [String: KSFTaskProjection] = [:]
    private var projectUsage: [String: ProjectUsageSummary]
    private var projectActions: [String: [ProjectLaunchAction]] = [:]
    private var projectListOrder: [String]
    private var projectTaskOrder: [String: [String]] = [:]
    private var retryAttempt = 0
    private var wakeObserver: WorkspaceWakeObserver?

    init(autoStart: Bool = true) {
        let socketURL = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".codex/ipc/ipc.sock")
        taskActivityProvider = CodexDesktopTaskActivityClient(
            transportFactory: { UnixSocketDesktopIPCTransport(socketURL: socketURL) },
            desktopIsRunning: {
                !NSRunningApplication.runningApplications(
                    withBundleIdentifier: "com.openai.codex"
                ).isEmpty
            }
        )
        taskSubmissionClient = CodexDesktopTaskSubmissionClient(
            transportFactory: { UnixSocketDesktopIPCTransport(socketURL: socketURL) }
        )
        defaults.register(defaults: [
            "launchAtLoginEnabled": true,
            "resetNotificationsEnabled": true,
            "ksfRootPath": "/Users/lawis/Documents/KSF",
        ])
        let cached = store.load()
        snapshot = cached
        status = cached?.headlineRemainingPercent == nil ? .loading : .stale
        launchAtLoginEnabled = defaults.bool(forKey: "launchAtLoginEnabled")
        resetNotificationsEnabled = defaults.bool(forKey: "resetNotificationsEnabled")
        ksfRootPath = defaults.string(forKey: "ksfRootPath") ?? "/Users/lawis/Documents/KSF"
        selectedProjectID = defaults.string(forKey: "selectedProjectID")
        pinnedProjectIDs = Set(defaults.stringArray(forKey: "pinnedProjectIDs") ?? [])
        projectUsage = projectUsageStore.load()
        projectListOrder = KSFProjectListOrdering.reconcile(
            previous: defaults.stringArray(forKey: "projectListOrder") ?? [],
            candidates: []
        )

        if autoStart {
            Task { [weak self] in
                await self?.start()
            }
        }
    }

    deinit {
        rateTimerTask?.cancel()
        accountTokenTimerTask?.cancel()
        activeLocalTokenTimerTask?.cancel()
        localTokenReadTask?.cancel()
        retryTask?.cancel()
        taskActivityUpdateTask?.cancel()
        projectRefreshTask?.cancel()
        weChatStateTask?.cancel()
        weChatCommandTask?.cancel()
        if let wakeObserver {
            NSWorkspace.shared.notificationCenter.removeObserver(wakeObserver)
        }
    }

    var menuTitle: String {
        let quota: String
        if let remaining = snapshot?.headlineRemainingPercent {
            quota = "Codex 剩余额度 \(remaining)%"
        } else if status == .loading {
            quota = "Codex 剩余额度正在载入"
        } else {
            quota = "Codex 剩余额度不可用"
        }

        let localTokens: String
        if let usage = localTodayUsage {
            localTokens = "本机今日 Token \(TokenCountFormatter.millions(usage.tokens))"
        } else if localTokenUsageWasRefreshedToday {
            localTokens = "本机今日 Token 不可用"
        } else {
            localTokens = "本机今日 Token 正在载入"
        }

        let weChat = weChatState == .connected ? "，微信已连接" : ""
        let summary = "\(quota)，\(localTokens)\(weChat)"

        switch taskActivity.availability {
        case .loading:
            return "\(summary)，任务状态正在载入"
        case .available, .desktopNotRunning:
            return "\(summary)，\(taskActivity.runningCount) 个任务运行中，\(taskActivity.waitingCount) 个任务等待处理"
        case .unsupportedProtocol, .offline:
            return "\(summary)，任务状态不可用"
        }
    }

    var weChatStatusText: String {
        switch weChatState {
        case .disconnected: return "未连接"
        case .awaitingScan: return "等待扫码"
        case .connecting: return "正在确认"
        case .connected: return "已连接"
        case .reconnecting: return "正在重连"
        case .credentialsExpired: return "连接已失效"
        case .failed: return "连接异常"
        }
    }

    var menuPercentageText: String {
        if let remaining = snapshot?.headlineRemainingPercent {
            return "\(remaining)%"
        }
        switch status {
        case .loading: return "…"
        default: return "—"
        }
    }

    var menuLocalTodayTokenText: String {
        if let usage = localTodayUsage {
            return TokenCountFormatter.millions(usage.tokens)
        }
        return localTokenUsageWasRefreshedToday ? "—M" : "…M"
    }

    var runningTaskText: String {
        taskCountText(taskActivity.runningCount)
    }

    var waitingTaskText: String {
        taskCountText(taskActivity.waitingCount)
    }

    var accountLatestUsage: DailyUsageBucket? {
        snapshot?.latestDailyUsage
    }

    var localTodayUsage: DailyUsageBucket? {
        guard snapshot?.localDailyUsage?.startDate == todayDateString else { return nil }
        return snapshot?.localDailyUsage
    }

    var localYesterdayUsage: DailyUsageBucket? {
        guard snapshot?.localPreviousDailyUsage?.startDate == yesterdayDateString else { return nil }
        return snapshot?.localPreviousDailyUsage
    }

    var statusMessage: String? {
        switch status {
        case .loading: return "正在读取 Codex 用量…"
        case .available: return nil
        case .stale: return "刷新失败，正在显示上次成功数据。"
        case .codexMissing: return "未找到可用的 Codex。"
        case .authenticationRequired: return "Codex 尚未使用 ChatGPT 登录。"
        case .unsupportedProtocol: return "当前 Codex 版本不支持用量接口。"
        case .offline: return "暂时无法获取 Codex 用量。"
        }
    }

    func start() async {
        guard !started else { return }
        started = true
        configureClientIfNeeded()
        configureLoginItem()
        notificationPermission = await notifications.permissionState()
        let observer = WorkspaceWakeObserver { [weak self] in
            Task { await self?.refreshAll() }
        }
        NSWorkspace.shared.notificationCenter.addObserver(
            observer,
            selector: #selector(WorkspaceWakeObserver.didWake(_:)),
            name: NSWorkspace.didWakeNotification,
            object: nil
        )
        wakeObserver = observer

        let stateUpdates = weChatConnector.stateUpdates
        weChatStateTask = Task { [weak self] in
            for await state in stateUpdates {
                guard !Task.isCancelled, let self else { break }
                self.weChatState = state
                if case let .awaitingScan(qrContent) = state {
                    self.weChatQRCodeContent = qrContent
                } else if state == .connected || state == .disconnected {
                    self.weChatQRCodeContent = nil
                }
                if case let .failed(message) = state { self.weChatFeedback = message }
            }
        }
        let commandUpdates = weChatConnector.commandUpdates
        weChatCommandTask = Task {
            for await _ in commandUpdates {
                guard !Task.isCancelled else { break }
                // The command-processing module will consume this stream in a later release.
            }
        }
        await weChatConnector.start()

        taskActivityUpdateTask = Task { [weak self, taskActivityProvider] in
            for await update in taskActivityProvider.updates {
                guard !Task.isCancelled else { break }
                guard let self else { break }
                self.applyTaskActivityUpdate(update)
            }
        }
        await taskActivityProvider.start()
        await refreshUsageData()
        await refreshProjects()
        startTimers()
    }

    func applyTaskActivityUpdate(_ update: TaskActivitySnapshot) {
        taskActivity = update
        updateActiveLocalTokenTimer(for: update)
        switch update.availability {
        case .available:
            projectTaskObservations = update.observations
            if projectDashboard.observedAt != nil {
                rebuildProjectDashboard(message: projectDashboard.message)
            }
            scheduleProjectRefresh()
        case .desktopNotRunning, .unsupportedProtocol, .offline:
            projectTaskObservations = []
            rebuildProjectDashboard(message: projectDashboard.message)
        case .loading:
            break
        }
    }

    func refreshAll() async {
        beginRefreshActivity()
        defer { endRefreshActivity() }
        await taskActivityProvider.refresh()
        await refreshUsageData()
        await refreshProjects()
    }

    private func refreshUsageData() async {
        beginRefreshActivity()
        defer { endRefreshActivity() }
        await refreshRateLimits()
        await refreshTokenUsage()
    }

    func popoverDidOpen() {
        Task { [weak self] in await self?.refreshAll() }
    }

    var selectedProject: ProjectDashboardItem? {
        guard let selectedProjectID else { return nil }
        return projectDashboard.projects.first { $0.id == selectedProjectID }
    }

    var homeProjectItems: [ProjectDashboardItem] {
        KSFProjectWorkset.select(from: projectDashboard.projects)
    }

    var projectLibraryItems: [ProjectDashboardItem] {
        let current = Dictionary(uniqueKeysWithValues: projectDashboard.projects.map { ($0.id, $0) })
        var items = projectDashboard.catalog.map { project in
            current[project.id] ?? ProjectDashboardItem(
                id: project.id,
                project: project,
                isPinned: pinnedProjectIDs.contains(project.id),
                usage: projectUsage[project.id],
                actions: projectActions[project.id] ?? []
            )
        }
        let catalogIDs = Set(items.map(\.id))
        items.append(contentsOf: projectDashboard.projects.filter { !catalogIDs.contains($0.id) })
        return KSFProjectListOrdering.sort(items, stableOrder: projectListOrder)
    }

    func selectProject(_ id: String) {
        selectedProjectID = id
        defaults.set(id, forKey: "selectedProjectID")
        rebuildProjectDashboard()
    }

    func togglePinned(_ id: String) {
        if pinnedProjectIDs.contains(id) {
            pinnedProjectIDs.remove(id)
            projectListOrder = KSFProjectListOrdering.moving(
                id,
                in: projectListOrder,
                toPinned: false,
                pinnedProjectIDs: pinnedProjectIDs
            )
        } else {
            pinnedProjectIDs.insert(id)
            projectListOrder = KSFProjectListOrdering.moving(
                id,
                in: projectListOrder,
                toPinned: true,
                pinnedProjectIDs: pinnedProjectIDs
            )
        }
        defaults.set(Array(pinnedProjectIDs).sorted(), forKey: "pinnedProjectIDs")
        defaults.set(projectListOrder, forKey: "projectListOrder")
        rebuildProjectDashboard()
    }

    func openKSFFolder() {
        NSWorkspace.shared.open(URL(fileURLWithPath: ksfRootPath, isDirectory: true))
    }

    func openProjectCard(_ project: KSFProject) {
        NSWorkspace.shared.open(URL(fileURLWithPath: project.cardPath))
    }

    func openProjectDirectory(_ project: KSFProject) {
        NSWorkspace.shared.open(URL(fileURLWithPath: project.projectDirectory, isDirectory: true))
    }

    func openEngineering(_ mapping: KSFEngineeringMapping) {
        NSWorkspace.shared.open(URL(fileURLWithPath: mapping.rootPath, isDirectory: true))
    }

    func launchAction(_ action: ProjectLaunchAction) {
        do {
            try actionLauncher.launch(action)
            projectActionError = nil
        } catch {
            projectActionError = error.localizedDescription
        }
    }

    func openTask(_ task: ProjectTaskItem) {
        do {
            try taskOpener.openTask(id: task.threadID)
            taskOpenFailure = nil
        } catch {
            taskOpenFailure = ProjectTaskOpenFailure(
                projectID: task.projectID,
                message: error.localizedDescription
            )
        }
    }

    func createTask(for project: KSFProject) {
        guard !creatingProjectTaskIDs.contains(project.id) else { return }

        guard isDirectory(atPath: ksfRootPath) else {
            projectTaskCreationErrors[project.id] = "KSF 根目录不存在，无法在 KSF 项目中新建任务。"
            return
        }
        guard FileManager.default.fileExists(atPath: project.cardPath),
              FileManager.default.fileExists(atPath: URL(
                fileURLWithPath: ksfRootPath,
                isDirectory: true
              ).appendingPathComponent("AGENTS.md").path) else {
            projectTaskCreationErrors[project.id] = "项目基础上下文入口不存在，请先检查 KSF 目录。"
            return
        }

        let bootstrap: ProjectTaskBootstrap
        do {
            bootstrap = try ProjectTaskBootstrap.prepare(
                project: project,
                ksfRootPath: ksfRootPath
            )
        } catch {
            projectTaskCreationErrors[project.id] = error.localizedDescription
            return
        }

        configureClientIfNeeded()
        guard let client else {
            projectTaskCreationErrors[project.id] = "未找到 Codex，无法新建任务。"
            return
        }

        projectTaskCreationErrors[project.id] = nil
        creatingProjectTaskIDs.insert(project.id)
        Task { [weak self] in
            guard let self else { return }
            defer { self.creatingProjectTaskIDs.remove(project.id) }
            do {
                let threadID = try await client.createDraftThread(
                    cwd: bootstrap.cwd,
                    name: bootstrap.name
                )
                do {
                    try await self.taskSubmissionClient.submitInitialTurn(
                        threadID: threadID,
                        cwd: bootstrap.cwd,
                        prompt: bootstrap.prompt,
                        openTask: { @MainActor [weak self] in
                            guard let self else {
                                throw CodexTaskOpeningError.applicationRejectedURL
                            }
                            try self.taskOpener.openTask(id: threadID)
                        }
                    )
                    self.projectTaskCreationErrors[project.id] = nil
                } catch {
                    self.projectTaskCreationErrors[project.id] = self.taskCreationErrorMessage(for: error)
                }
                await self.taskActivityProvider.refresh()
                self.scheduleProjectRefresh()
            } catch {
                self.projectTaskCreationErrors[project.id] = self.taskCreationErrorMessage(for: error)
            }
        }
    }

    func chooseKSFRoot() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "选择"
        panel.message = "选择 KSF 知识库根目录"
        panel.directoryURL = URL(fileURLWithPath: ksfRootPath, isDirectory: true)
        guard panel.runModal() == .OK, let url = panel.url else { return }
        ksfRootPath = url.standardizedFileURL.path
        defaults.set(ksfRootPath, forKey: "ksfRootPath")
        bridgeEnabledAt = nil
        projectDashboard = ProjectDashboardSnapshot(availability: .loading)
        Task { [weak self] in await self?.refreshProjects() }
    }

    func setLaunchAtLogin(_ enabled: Bool) {
        launchAtLoginEnabled = enabled
        defaults.set(enabled, forKey: "launchAtLoginEnabled")
        loginItemState = loginItems.setEnabled(enabled)
    }

    func setResetNotifications(_ enabled: Bool) {
        resetNotificationsEnabled = enabled
        defaults.set(enabled, forKey: "resetNotificationsEnabled")
        guard enabled else { return }
        Task { [weak self] in
            guard let self else { return }
            self.notificationPermission = await self.notifications.requestPermission()
        }
    }

    func connectWeChat() {
        guard !weChatActionInProgress else { return }
        weChatActionInProgress = true
        weChatFeedback = nil
        Task { [weak self] in
            guard let self else { return }
            defer { self.weChatActionInProgress = false }
            do {
                _ = try await self.weChatConnector.beginLogin()
            } catch {
                self.weChatFeedback = error.localizedDescription
            }
        }
    }

    func sendWeChatTestMessage() {
        guard !weChatActionInProgress else { return }
        weChatActionInProgress = true
        weChatFeedback = nil
        Task { [weak self] in
            guard let self else { return }
            defer { self.weChatActionInProgress = false }
            do {
                try await self.weChatConnector.send(text: "Codex Usage Bar 微信连接测试成功")
                self.weChatFeedback = "测试消息已发送。"
            } catch {
                self.weChatFeedback = error.localizedDescription
            }
        }
    }

    func disconnectWeChat() {
        guard !weChatActionInProgress else { return }
        weChatActionInProgress = true
        weChatFeedback = nil
        Task { [weak self] in
            guard let self else { return }
            await self.weChatConnector.disconnect()
            self.weChatActionInProgress = false
            self.weChatFeedback = "已断开微信并清除本地连接数据。"
        }
    }

    func quit() {
        rateTimerTask?.cancel()
        accountTokenTimerTask?.cancel()
        activeLocalTokenTimerTask?.cancel()
        localTokenReadTask?.cancel()
        retryTask?.cancel()
        taskActivityUpdateTask?.cancel()
        projectRefreshTask?.cancel()
        weChatStateTask?.cancel()
        weChatCommandTask?.cancel()
        Task { [weak self] in
            await self?.client?.stop()
            await self?.taskActivityProvider.stop()
            await self?.weChatConnector.stop()
            await MainActor.run { NSApplication.shared.terminate(nil) }
        }
    }

    private func configureClientIfNeeded() {
        guard client == nil else { return }
        guard let executable = CodexLocator.locate() else {
            status = snapshot?.headlineRemainingPercent == nil ? .codexMissing : .stale
            lastErrorMessage = "已检查以下位置：\n\(CodexLocator.searchDescription)"
            return
        }
        client = CodexAppServerClient {
            ProcessAppServerTransport(executableURL: executable)
        }
    }

    private func configureLoginItem() {
        if launchAtLoginEnabled && !defaults.bool(forKey: "didConfigureLaunchAtLogin") {
            loginItemState = loginItems.setEnabled(true)
            let configured = loginItemState == .enabled || loginItemState == .requiresApproval
            defaults.set(configured, forKey: "didConfigureLaunchAtLogin")
        } else {
            loginItemState = loginItems.state()
        }
    }

    private func startTimers() {
        rateTimerTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 300_000_000_000)
                guard !Task.isCancelled else { break }
                await self?.refreshRateLimits()
                await self?.refreshProjects()
            }
        }
        accountTokenTimerTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_800_000_000_000)
                guard !Task.isCancelled else { break }
                await self?.refreshTokenUsage()
                await self?.refreshProjects()
            }
        }
    }

    private func updateActiveLocalTokenTimer(for activity: TaskActivitySnapshot) {
        guard LocalTokenRefreshPolicy.shouldPoll(for: activity) else {
            activeLocalTokenTimerTask?.cancel()
            activeLocalTokenTimerTask = nil
            return
        }
        guard activeLocalTokenTimerTask == nil else { return }

        activeLocalTokenTimerTask = Task { [weak self] in
            guard let self else { return }
            await self.refreshLocalTokenUsage()
            while !Task.isCancelled {
                do {
                    try await Task.sleep(
                        nanoseconds: LocalTokenRefreshPolicy.activeIntervalNanoseconds
                    )
                } catch {
                    break
                }
                guard !Task.isCancelled else { break }
                await self.refreshLocalTokenUsage()
            }
        }
    }

    private func scheduleProjectRefresh() {
        projectRefreshTask?.cancel()
        projectRefreshTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 350_000_000)
            guard !Task.isCancelled else { return }
            await self?.refreshProjects()
        }
    }

    private func refreshProjects() async {
        guard !refreshingProjects else { return }
        refreshingProjects = true
        isRefreshingProjects = true
        defer {
            refreshingProjects = false
            isRefreshingProjects = false
        }

        let rootURL = URL(fileURLWithPath: ksfRootPath, isDirectory: true).standardizedFileURL
        guard FileManager.default.fileExists(atPath: rootURL.path) else {
            await taskActivityProvider.reconcileLocalTaskCandidates([])
            projectDashboard = ProjectDashboardSnapshot(
                availability: .unavailable,
                projects: unavailablePinnedProjects(),
                observedAt: Date(),
                message: "KSF 目录不存在，请在设置中重新选择。"
            )
            return
        }

        do {
            let bridge = self.bridge
            let bridgeResult = try await Task.detached(priority: .utility) {
                let enabledAt = try bridge.enable(rootURL: rootURL)
                let catalog = try bridge.fetchCatalog(rootURL: rootURL)
                return (enabledAt, catalog)
            }.value
            bridgeEnabledAt = bridgeResult.0
            projectCatalog = bridgeResult.1.projects
        } catch {
            await taskActivityProvider.reconcileLocalTaskCandidates([])
            projectDashboard = ProjectDashboardSnapshot(
                availability: error is KSFBridgeError ? .unsupportedProtocol : .unavailable,
                projects: unavailablePinnedProjects(),
                observedAt: Date(),
                message: error.localizedDescription
            )
            return
        }

        var partialMessage: String?
        configureClientIfNeeded()
        if let client {
            do {
                projectThreads = try await client.fetchThreads()
                let threadIDs = projectThreads.map(\.id)
                if threadIDs.isEmpty {
                    projectProjections = [:]
                } else {
                    let bridge = self.bridge
                    projectProjections = try await Task.detached(priority: .utility) {
                        try bridge.resolve(rootURL: rootURL, threadIDs: threadIDs).byThreadID
                    }.value
                }
                await taskActivityProvider.reconcileLocalTaskCandidates(
                    KSFProjectDashboardBuilder.localTaskCandidateIDs(
                        catalog: projectCatalog,
                        threads: projectThreads,
                        projections: projectProjections
                    )
                )

                let timelines = projectProjections.map { threadID, projection in
                    ProjectTaskTimeline(
                        threadID: threadID,
                        transitions: projection.bindings.compactMap { binding in
                            guard let date = Self.parseISO8601(binding.boundAt) else { return nil }
                            return ProjectBindingTransition(projectID: binding.projectCard, boundAt: date)
                        }
                    )
                }
                let trackingStart = bridgeEnabledAt ?? Date()
                let catalog = projectCatalog
                let threads = projectThreads
                let usage = await Task.detached(priority: .utility) {
                    ProjectTokenUsageReader().read(
                        projectIDs: catalog.map(\.id),
                        threads: threads,
                        timelines: timelines,
                        trackingStartedAt: trackingStart
                    )
                }.value
                projectUsage = usage
                projectUsageStore.save(usage)
            } catch {
                partialMessage = "项目任务与 Token 暂不可用；目录信息仍可使用。"
                projectThreads = []
                projectProjections = [:]
                await taskActivityProvider.reconcileLocalTaskCandidates([])
            }
        } else {
            partialMessage = "未找到 Codex；项目目录仍可使用。"
            await taskActivityProvider.reconcileLocalTaskCandidates([])
        }

        let catalog = projectCatalog
        let loaderResult = await Task.detached(priority: .utility) {
            var actions: [String: [ProjectLaunchAction]] = [:]
            var errors = 0
            let loader = ProjectActionManifestLoader()
            for project in catalog {
                var projectActions: [ProjectLaunchAction] = []
                for mapping in project.engineeringMappings {
                    do {
                        projectActions.append(contentsOf: try loader.load(mapping: mapping))
                    } catch {
                        errors += 1
                    }
                }
                actions[project.id] = projectActions
            }
            return (actions, errors)
        }.value
        projectActions = loaderResult.0
        if loaderResult.1 > 0 {
            partialMessage = [partialMessage, "有 \(loaderResult.1) 个启动动作清单未通过安全校验。"]
                .compactMap { $0 }.joined(separator: " ")
        }

        rebuildProjectDashboard(message: partialMessage)
    }

    private func rebuildProjectDashboard(message: String? = nil) {
        let items = KSFProjectDashboardBuilder.build(
            catalog: projectCatalog,
            activeTasks: projectTaskObservations,
            threads: projectThreads,
            projections: projectProjections,
            pinnedProjectIDs: pinnedProjectIDs,
            selectedProjectID: selectedProjectID,
            usage: projectUsage,
            actions: projectActions
        )
        let reconciledOrder = KSFProjectListOrdering.reconcile(
            previous: projectListOrder,
            candidates: projectCatalog.map(\.id) + items.map(\.id)
        )
        if reconciledOrder != projectListOrder {
            projectListOrder = reconciledOrder
            defaults.set(projectListOrder, forKey: "projectListOrder")
        }
        let taskOrderedItems = items.map { item -> ProjectDashboardItem in
            let stableTaskOrder = ProjectTaskListOrdering.reconcile(
                previous: projectTaskOrder[item.id] ?? [],
                candidates: item.tasks
            )
            projectTaskOrder[item.id] = stableTaskOrder
            return item.replacingTasks(ProjectTaskListOrdering.sort(
                item.tasks,
                stableOrder: stableTaskOrder
            ))
        }
        let orderedItems = KSFProjectListOrdering.sort(taskOrderedItems, stableOrder: projectListOrder)
        if let failure = taskOpenFailure,
           !orderedItems.contains(where: { item in
               item.id == failure.projectID && !item.tasks.isEmpty
           }) {
            taskOpenFailure = nil
        }
        projectDashboard = ProjectDashboardSnapshot(
            availability: .available,
            projects: orderedItems,
            catalog: projectCatalog,
            observedAt: Date(),
            message: message
        )
        reconcileSelectedProject()
    }

    private func reconcileSelectedProject() {
        if let selectedProjectID,
           projectDashboard.projects.contains(where: { $0.id == selectedProjectID }) {
            return
        }
        selectedProjectID = projectDashboard.projects.first?.id
        if let selectedProjectID {
            defaults.set(selectedProjectID, forKey: "selectedProjectID")
        } else {
            defaults.removeObject(forKey: "selectedProjectID")
        }
    }

    private func unavailablePinnedProjects() -> [ProjectDashboardItem] {
        let items = pinnedProjectIDs.sorted().map {
            ProjectDashboardItem(id: $0, project: nil, isPinned: true, usage: projectUsage[$0])
        }
        return KSFProjectListOrdering.sort(items, stableOrder: projectListOrder)
    }

    private static func parseISO8601(_ value: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: value) ?? ISO8601DateFormatter().date(from: value)
    }

    private func refreshRateLimits() async {
        guard !refreshingRateLimits else { return }
        configureClientIfNeeded()
        guard let client else { return }
        refreshingRateLimits = true
        beginRefreshActivity()
        defer {
            refreshingRateLimits = false
            endRefreshActivity()
        }

        let previousSnapshot = snapshot
        do {
            let response = try await client.fetchRateLimits()
            let now = Date()
            var updated = snapshot ?? UsageSnapshot()
            updated.buckets = response.normalizedBuckets.map(\.cacheRepresentation)
            updated.rateUpdatedAt = now
            snapshot = updated
            store.save(updated)
            status = response.generalBucket?.headlineRemainingPercent == nil ? .unsupportedProtocol : .available
            lastErrorMessage = nil
            retryAttempt = 0
            retryTask?.cancel()
            await configureNotificationsAfterFirstSnapshot()
            processResetEvents(previous: previousSnapshot, current: updated, now: now)
        } catch {
            applyRateError(error)
            scheduleRetry()
        }
    }

    private func refreshTokenUsage() async {
        guard !refreshingTokenUsage else { return }
        configureClientIfNeeded()
        refreshingTokenUsage = true
        beginRefreshActivity()
        defer {
            refreshingTokenUsage = false
            endRefreshActivity()
        }

        let localUsageTask = Task { [weak self] () -> DailyUsageBucket? in
            guard let self else { return nil }
            return await self.readLocalTokenUsage()
        }
        let localPreviousUsageTask = Task { [weak self] () -> DailyUsageBucket? in
            guard let self else { return nil }
            return await self.readLocalPreviousTokenUsage()
        }

        guard let client else {
            persistLocalUsage(
                await localUsageTask.value,
                previousDayUsage: await localPreviousUsageTask.value
            )
            return
        }

        do {
            let response = try await client.fetchTokenUsage()
            let localUsage = await localUsageTask.value
            let localPreviousUsage = await localPreviousUsageTask.value
            let now = Date()
            var updated = snapshot ?? UsageSnapshot()
            updated.tokenSummary = response.summary
            updated.dailyUsageBuckets = response.normalizedDailyBuckets()
            mergeLocalUsage(
                localUsage,
                previousDayUsage: localPreviousUsage,
                into: &updated
            )
            updated.tokenUpdatedAt = now
            updated.localTokenUpdatedAt = now
            snapshot = updated
            store.save(updated)
            tokenErrorMessage = nil
        } catch {
            persistLocalUsage(
                await localUsageTask.value,
                previousDayUsage: await localPreviousUsageTask.value
            )
            tokenErrorMessage = tokenUsageErrorMessage(for: error)
        }
    }

    private var todayDateString: String {
        LocalTokenUsageReader.dateString(for: Date())
    }

    private var yesterdayDateString: String {
        let calendar = Calendar.current
        guard let yesterday = calendar.date(byAdding: .day, value: -1, to: Date()) else {
            return ""
        }
        return LocalTokenUsageReader.dateString(for: yesterday, calendar: calendar)
    }

    private var localTokenUsageWasRefreshedToday: Bool {
        guard let updatedAt = snapshot?.localTokenUpdatedAt else { return false }
        return Calendar.current.isDateInToday(updatedAt)
    }

    private func beginRefreshActivity() {
        refreshActivityDepth += 1
        if refreshActivityDepth == 1 {
            isRefreshing = true
        }
    }

    private func endRefreshActivity() {
        refreshActivityDepth = max(0, refreshActivityDepth - 1)
        if refreshActivityDepth == 0 {
            isRefreshing = false
        }
    }

    private func persistLocalUsage(
        _ usage: DailyUsageBucket?,
        previousDayUsage: DailyUsageBucket? = nil
    ) {
        var updated = snapshot ?? UsageSnapshot()
        mergeLocalUsage(usage, previousDayUsage: previousDayUsage, into: &updated)
        updated.localTokenUpdatedAt = Date()
        snapshot = updated
        store.save(updated)
    }

    private func mergeLocalUsage(
        _ usage: DailyUsageBucket?,
        previousDayUsage: DailyUsageBucket?,
        into snapshot: inout UsageSnapshot
    ) {
        let expectedPreviousDate = yesterdayDateString
        let rolledOverUsage = snapshot.localDailyUsage.flatMap { existing in
            existing.startDate == expectedPreviousDate ? existing : nil
        }
        let verifiedPreviousUsage = previousDayUsage.flatMap { previous in
            previous.startDate == expectedPreviousDate ? previous : nil
        }
        let retainedPreviousUsage = snapshot.localPreviousDailyUsage.flatMap { previous in
            previous.startDate == expectedPreviousDate ? previous : nil
        }
        snapshot.localPreviousDailyUsage = verifiedPreviousUsage
            ?? rolledOverUsage
            ?? retainedPreviousUsage
        snapshot.localDailyUsage = usage
    }

    private func refreshLocalTokenUsage() async {
        persistLocalUsage(await readLocalTokenUsage())
    }

    private func readLocalTokenUsage() async -> DailyUsageBucket? {
        if let localTokenReadTask {
            return await localTokenReadTask.value
        }

        let task = Task.detached(priority: .utility) {
            LocalTokenUsageReader().readToday()
        }
        localTokenReadTask = task
        let usage = await task.value
        localTokenReadTask = nil
        return usage
    }

    private func readLocalPreviousTokenUsage() async -> DailyUsageBucket? {
        let calendar = Calendar.current
        guard let previousDay = calendar.date(byAdding: .day, value: -1, to: Date()) else {
            return nil
        }
        return await Task.detached(priority: .utility) {
            LocalTokenUsageReader().read(on: previousDay, calendar: calendar)
        }.value
    }

    private func configureNotificationsAfterFirstSnapshot() async {
        guard resetNotificationsEnabled else { return }
        if !defaults.bool(forKey: "didRequestResetNotificationPermission") {
            defaults.set(true, forKey: "didRequestResetNotificationPermission")
            notificationPermission = await notifications.requestPermission()
        } else {
            notificationPermission = await notifications.permissionState()
        }
    }

    private func processResetEvents(previous: UsageSnapshot?, current: UsageSnapshot, now: Date) {
        guard resetNotificationsEnabled else { return }
        let notified = notifiedResetTimestamps()
        let events = ResetDetector.detect(
            previous: previous?.generalBucket,
            current: current.generalBucket,
            previousCapturedAt: previous?.rateUpdatedAt,
            currentCapturedAt: now,
            notifiedResetTimestamps: notified
        )
        guard !events.isEmpty else { return }
        events.forEach { notifications.send(event: $0) }
        saveNotifiedResetTimestamps(notified.union(events.map(\.resetTimestamp)))
    }

    private func notifiedResetTimestamps() -> Set<Int64> {
        let values = defaults.array(forKey: "notifiedResetTimestamps") as? [NSNumber] ?? []
        return Set(values.map(\.int64Value))
    }

    private func saveNotifiedResetTimestamps(_ values: Set<Int64>) {
        let recent = values.sorted().suffix(20).map(NSNumber.init(value:))
        defaults.set(recent, forKey: "notifiedResetTimestamps")
    }

    private func applyRateError(_ error: Error) {
        lastErrorMessage = recoveryMessage(for: error)
        let hasSnapshot = snapshot?.headlineRemainingPercent != nil
        if hasSnapshot {
            status = .stale
            return
        }
        guard let codexError = error as? CodexUsageError else {
            status = .offline
            return
        }
        switch codexError {
        case .codexMissing: status = .codexMissing
        case .authenticationRequired: status = .authenticationRequired
        case .unsupportedProtocol, .protocolFailure: status = .unsupportedProtocol
        case .offline: status = .offline
        }
    }

    private func recoveryMessage(for error: Error) -> String {
        guard let codexError = error as? CodexUsageError else {
            return "请检查网络，应用会自动重试。"
        }
        switch codexError {
        case .codexMissing:
            return "请安装 Codex，或确认它位于受支持的路径。"
        case .authenticationRequired:
            return "请先在 Codex 中使用 ChatGPT 登录，然后刷新。"
        case .unsupportedProtocol, .protocolFailure:
            return "请更新 Codex 后重试。"
        case .offline:
            return "请检查网络，应用会自动重试。"
        }
    }

    private func tokenUsageErrorMessage(for error: Error) -> String {
        if let codexError = error as? CodexUsageError, codexError == .authenticationRequired {
            return "Token 统计仅支持 ChatGPT 登录。"
        }
        return "Token 活动暂不可用，额度信息不受影响。"
    }

    private func taskCreationErrorMessage(for error: Error) -> String {
        if let submissionError = error as? CodexDesktopTaskSubmissionError {
            return "任务已创建，但首次说明提交失败：\(submissionError.localizedDescription)"
        }
        if error is CodexTaskOpeningError {
            return "任务已创建，但 Codex 未能打开它。"
        }
        guard let codexError = error as? CodexUsageError else {
            return "暂时无法连接 Codex，新任务未创建。"
        }
        switch codexError {
        case .codexMissing:
            return "未找到 Codex，无法新建任务。"
        case .authenticationRequired:
            return "Codex 尚未登录，无法新建任务。"
        case .unsupportedProtocol, .protocolFailure:
            return "当前 Codex 版本不支持新建任务，请更新后重试。"
        case .offline:
            return "暂时无法连接 Codex，新任务未创建。"
        }
    }

    private func isDirectory(atPath path: String) -> Bool {
        var isDirectory: ObjCBool = false
        return FileManager.default.fileExists(atPath: path, isDirectory: &isDirectory)
            && isDirectory.boolValue
    }

    private func scheduleRetry() {
        retryTask?.cancel()
        let delays: [UInt64] = [5, 30, 120, 300]
        let seconds = delays[min(retryAttempt, delays.count - 1)]
        retryAttempt += 1
        retryTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: seconds * 1_000_000_000)
            guard !Task.isCancelled else { return }
            await self?.refreshRateLimits()
        }
    }

    private func taskCountText(_ value: Int) -> String {
        switch taskActivity.availability {
        case .loading:
            return "…"
        case .available, .desktopNotRunning:
            return String(value)
        case .unsupportedProtocol, .offline:
            return "—"
        }
    }
}

private final class WorkspaceWakeObserver: NSObject {
    private let handler: () -> Void

    init(handler: @escaping () -> Void) {
        self.handler = handler
    }

    @objc func didWake(_ notification: Notification) {
        handler()
    }
}
