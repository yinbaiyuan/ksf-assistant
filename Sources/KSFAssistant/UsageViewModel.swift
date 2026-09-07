import AppKit
import KSFAssistantCore
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
    static let defaultPricingPlanID = "openai:gpt-5.6-sol"

    @Published private(set) var snapshot: UsageSnapshot?
    @Published private(set) var status: UsageDisplayStatus
    @Published private(set) var isRefreshing = false
    @Published private(set) var lastErrorMessage: String?
    @Published private(set) var tokenErrorMessage: String?
    @Published private(set) var localTokenHistory: [DailyUsageBucket] = []
    @Published private(set) var tokenHistoryComparison = TokenHistoryComparison()
    @Published private(set) var pricingCatalog = PricingCatalog(
        defaultPlanId: UsageViewModel.defaultPricingPlanID,
        plans: []
    )
    @Published private(set) var customPricingPlans: [PricingPlan] = []
    @Published private(set) var selectedPricingPlanID = UsageViewModel.defaultPricingPlanID
    @Published private(set) var pricingFeedback: String?
    @Published private(set) var isRefreshingLocalTokenHistory = false
    @Published private(set) var localTokenHistoryError: String?
    @Published private(set) var taskActivity = TaskActivitySnapshot(availability: .loading)
    @Published private(set) var projectDashboard = ProjectDashboardSnapshot()
    @Published private(set) var pinnedProjectIDs: Set<String>
    @Published private(set) var isRefreshingProjects = false
    @Published private(set) var projectActionError: String?
    @Published private(set) var taskOpenFailure: ProjectTaskOpenFailure?
    @Published private(set) var creatingProjectTaskIDs: Set<String> = []
    @Published private(set) var archivingProjectTaskIDs: Set<String> = []
    @Published private(set) var projectTaskCreationErrors: [String: String] = [:]
    @Published private(set) var ksfRootPath: String
    @Published private(set) var isOnboardingComplete: Bool
    @Published private(set) var onboardingInProgress = false
    @Published private(set) var onboardingError: String?
    @Published private(set) var notificationPermission: NotificationPermissionState = .unknown
    @Published private(set) var loginItemState: LoginItemState = .disabled
    @Published private(set) var feishuService = FeishuServiceSnapshot.notConfigured
    @Published private(set) var feishuConfiguration = FeishuConfigurationState()
    var feishuConnectionIndicatorVisible: Bool {
        feishuConfiguration.showsConnectionIndicator(transportReady: feishuService.availability == .ready, taskLinkReady: feishuService.taskLinkReady)
    }
    var feishuActionInProgress: Bool { feishuConfiguration.acting || feishuConfiguration.reading }
    @Published private(set) var feishuFeedback: String?
    var feishuAuthStatus: CoreServiceFeishuAuth? { feishuConfiguration.snapshot?.auth }
    var feishuSettingsOverview: FeishuSettingsOverview? { feishuConfiguration.snapshot?.overview }
    @Published private(set) var toolchainStatus: ToolchainStatus?
    @Published private(set) var toolchainActionInProgress = false
    @Published private(set) var toolchainFeedback: String?
    var feishuSetup: FeishuSetupState { feishuConfiguration.snapshot?.setup ?? .unknown }
    @Published private(set) var feishuTaskLinks: [String: FeishuTaskLinkSnapshot] = [:]
    @Published private(set) var feishuTaskLinkActions: Set<String> = []
    @Published private(set) var feishuTaskLinkErrors: [String: String] = [:]
    @Published var selectedFeishuTargetAlias: String
    @Published var launchAtLoginEnabled: Bool
    @Published var resetNotificationsEnabled: Bool

    private let store = SnapshotStore()
    private let defaults = UserDefaults.standard
    private let notifications = ResetNotificationController()
    private let loginItems = LoginItemController()
    private let projectUsageStore = ProjectUsageStore()
    private let actionLauncher = TerminalActionLauncher()
    private let taskOpener: CodexTaskOpening = WorkspaceCodexTaskOpener()
    private let coreService = CoreServiceProcessClient()
    private lazy var feishuConfigurationSession: FeishuConfigurationSession = {
        let client = coreService
        let session = FeishuConfigurationSession(canSubmit: { [weak self] in
            guard let self else { return false }
            return !self.quitRequested && !self.shutdownStarted && self.popoverIsOpen
                && self.userApprovalController.allowsConfigurationSubmission
        }) { method, payload in
            try await client.feishuConfigurationRequest(method: method, payload: payload)
        }
        session.onChange = { [weak self] state in self?.feishuConfiguration = state }
        return session
    }()
    private lazy var userApprovalController = UserApprovalController(core: coreService)
    private var coreServiceEnabled = false
    private var shutdownStarted = false
    private var quitRequested = false
    private var refreshingCoreService = false
    private var started = false
    private var refreshActivityDepth = 0
    private var rateTimerTask: Task<Void, Never>?
    private var localTokenHistoryTask: Task<Void, Never>?
    private var coreServicePollTask: Task<Void, Never>?
    @Published private(set) var popoverPresentationID: UInt64 = 0
    private var popoverIsOpen = false
    private var projectUsage: [String: ProjectUsageSummary]
    private var projectLaunchActions: [String: ProjectLaunchAction] = [:]
    private var projectListOrder: [String]
    private var projectTaskOrder: [String: [String]] = [:]
    private var wakeObserver: WorkspaceWakeObserver?

    init(autoStart: Bool = true, cleanupLegacyWeChatData: Bool = true) {
        defaults.register(defaults: [
            "launchAtLoginEnabled": false,
            "resetNotificationsEnabled": false,
            "onboardingComplete": false,
            "selectedPricingPlanID": Self.defaultPricingPlanID,
        ])
        defaults.removeObject(forKey: "selectedProjectID")
        let cached = store.load()
        snapshot = cached
        status = cached?.headlineRemainingPercent == nil ? .loading : .stale
        launchAtLoginEnabled = defaults.bool(forKey: "launchAtLoginEnabled")
        resetNotificationsEnabled = defaults.bool(forKey: "resetNotificationsEnabled")
        ksfRootPath = defaults.string(forKey: "ksfRootPath") ?? ""
        defaults.removeObject(forKey: "feishuServiceRootPath")
        selectedFeishuTargetAlias = defaults.string(forKey: "selectedFeishuTargetAlias") ?? ""
        selectedPricingPlanID = defaults.string(forKey: "selectedPricingPlanID") ?? Self.defaultPricingPlanID
        if let data = defaults.data(forKey: "customPricingPlansV1"),
           let plans = try? JSONDecoder().decode([PricingPlan].self, from: data) {
            customPricingPlans = Array(plans.prefix(20))
        }
        isOnboardingComplete = defaults.bool(forKey: "onboardingComplete")
        pinnedProjectIDs = Set(defaults.stringArray(forKey: "pinnedProjectIDs") ?? [])
        projectUsage = projectUsageStore.load()
        projectListOrder = KSFProjectListOrdering.reconcile(
            previous: defaults.stringArray(forKey: "projectListOrder") ?? [],
            candidates: []
        )
        if cleanupLegacyWeChatData && !defaults.bool(forKey: "didRemoveLegacyWeChatData") {
            do {
                try LegacyWeChatDataCleaner().clean()
                defaults.set(true, forKey: "didRemoveLegacyWeChatData")
            } catch {
                feishuFeedback = error.localizedDescription
            }
        }

        if autoStart {
            Task { [weak self] in
                await self?.start()
            }
        }
    }

    deinit {
        rateTimerTask?.cancel()
        localTokenHistoryTask?.cancel()
        coreServicePollTask?.cancel()
        if let wakeObserver {
            NSWorkspace.shared.notificationCenter.removeObserver(wakeObserver)
        }
    }

    #if FEISHU_LAYOUT_PREVIEW
    func loadFeishuLayoutPreview(snapshot: FeishuConfigurationSnapshot?, toolchain: ToolchainStatus, failed: Bool = false) throws {
        precondition(!started && !coreServiceEnabled)
        if let snapshot { try feishuConfigurationSession.restore(snapshot) }
        if failed { feishuConfiguration.phase = .unknown }
        toolchainStatus = toolchain
    }
    #endif

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

        let feishu = feishuConnectionIndicatorVisible ? "，飞书服务已连接" : ""
        let summary = "\(quota)，\(localTokens)\(feishu)"

        switch taskActivity.availability {
        case .loading:
            return "\(summary)，任务状态正在载入"
        case .available, .desktopNotRunning:
            return "\(summary)，\(taskActivity.runningCount) 个任务运行中，\(taskActivity.waitingCount) 个任务等待处理"
        case .unsupportedProtocol, .offline:
            return "\(summary)，任务状态不可用"
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

        do {
            let activeKSFRoot = isOnboardingComplete ? ksfRootPath : ""
            try await coreService.start(ksfRoot: activeKSFRoot)
            coreServiceEnabled = true
            userApprovalController.start()
            await refreshPricingCatalog()
            await refreshSharedDashboard()
            await refreshFeishuConfiguration()
            feishuConfigurationSession.startPolling()
            startCoreServiceTimers()
            return
        } catch {
            coreServiceEnabled = false
            status = snapshot?.headlineRemainingPercent == nil ? .offline : .stale
            lastErrorMessage = "核心服务不可用；正在显示缓存数据。"
            taskActivity = TaskActivitySnapshot(availability: .offline)
            projectDashboard = ProjectDashboardSnapshot(
                availability: .unavailable,
                projects: unavailablePinnedProjects(),
                observedAt: Date(),
                message: "核心服务不可用。"
            )
            feishuService = FeishuServiceSnapshot(
                availability: .unavailable("核心服务不可用。"),
                targetAliases: [],
                taskLinkProtocolVersion: 0,
                taskLinkReady: false,
                readinessBlockers: ["核心服务不可用"]
            )
            await refreshFeishuConfiguration()
            return
        }
    }

    func refreshAll() async {
        if coreServiceEnabled {
            await refreshSharedDashboard()
        }
    }

    func popoverDidOpen() {
        guard !popoverIsOpen else { return }
        popoverPresentationID &+= 1
        popoverIsOpen = true
        if coreServiceEnabled {
            startCoreServicePolling()
            Task { [weak self] in await self?.refreshSharedDashboard() }
            return
        }
        Task { [weak self] in await self?.refreshAll() }
    }

    func popoverDidClose() {
        popoverIsOpen = false
        coreServicePollTask?.cancel()
        coreServicePollTask = nil
    }

    func completeOnboarding() {
        guard !onboardingInProgress else { return }
        let path = ksfRootPath.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !path.isEmpty else {
            onboardingError = "请先选择 KSF 根目录。"
            return
        }
        onboardingInProgress = true
        onboardingError = nil
        let rootURL = URL(fileURLWithPath: path, isDirectory: true).standardizedFileURL
        Task { [weak self] in
            guard let self else { return }
            do {
                guard self.coreServiceEnabled else {
                    throw CoreServiceError.processStopped
                }
                try await self.coreService.updateIntegrationContext(ksfRoot: rootURL.path)
                self.ksfRootPath = rootURL.path
                self.defaults.set(rootURL.path, forKey: "ksfRootPath")
                self.defaults.set(true, forKey: "onboardingComplete")
                self.isOnboardingComplete = true
                self.onboardingError = nil
                await self.refreshSharedDashboard()
                self.refreshLocalTokenHistory()
            } catch {
                self.onboardingError = error.localizedDescription
            }
            self.onboardingInProgress = false
        }
    }

    func refreshLocalTokenHistory() {
        guard localTokenHistoryTask == nil else { return }
        isRefreshingLocalTokenHistory = true
        localTokenHistoryError = nil

        if coreServiceEnabled {
            localTokenHistoryTask = Task { [weak self] in
                guard let self else { return }
                do {
                    let comparison = try await self.coreService.tokenHistoryComparison(
                        dayCount: 30,
                        pricingSelection: self.pricingSelection
                    )
                    self.applyTokenHistoryComparison(comparison)
                    self.localTokenHistoryError = nil
                } catch {
                    self.localTokenHistoryError = error.localizedDescription
                }
                self.isRefreshingLocalTokenHistory = false
                self.localTokenHistoryTask = nil
            }
            return
        }
        localTokenHistoryError = "核心服务不可用。"
        isRefreshingLocalTokenHistory = false
    }

    var selectedPricingPlan: PricingPlan? {
        pricingCatalog.plans.first { $0.id == selectedPricingPlanID }
    }

    var pricingSelection: PricingSelection {
        PricingSelection(planId: selectedPricingPlanID, customPlans: customPricingPlans)
    }

    func selectPricingPlan(_ planID: String) {
        guard pricingCatalog.plans.contains(where: { $0.id == planID }) else { return }
        selectedPricingPlanID = planID
        defaults.set(planID, forKey: "selectedPricingPlanID")
        Task { [weak self] in await self?.repriceLoadedTokenHistory() }
    }

    func saveCustomPricingPlan(
        id: String?,
        provider: String,
        model: String,
        variant: String,
        regularInputMicroUSDPerMillion: Int64,
        cachedInputMicroUSDPerMillion: Int64,
        outputMicroUSDPerMillion: Int64
    ) async -> Bool {
        guard coreServiceEnabled else {
            pricingFeedback = "核心服务不可用，暂不能保存价格方案。"
            return false
        }
        let planID = id ?? "custom:\(UUID().uuidString.lowercased())"
        let candidate = PricingPlan(
            id: planID,
            provider: provider,
            model: model,
            variant: variant.isEmpty ? nil : variant,
            regularInputMicroUsdPerMillion: regularInputMicroUSDPerMillion,
            cachedInputMicroUsdPerMillion: cachedInputMicroUSDPerMillion,
            outputMicroUsdPerMillion: outputMicroUSDPerMillion
        )
        var proposed = customPricingPlans.filter { $0.id != planID }
        proposed.append(candidate)
        do {
            let catalog = try await coreService.pricingCatalog(customPlans: proposed)
            guard catalog.plans.contains(where: { $0.id == planID }),
                  !(catalog.rejectedCustomPlanIds ?? []).contains(planID) else {
                pricingFeedback = "价格方案无效：请检查名称、重复项和 0–1000 美元的六位小数价格。"
                return false
            }
            customPricingPlans = catalog.plans.filter { !$0.builtIn }
            pricingCatalog = catalog
            persistPricingSettings()
            pricingFeedback = nil
            await repriceLoadedTokenHistory()
            return true
        } catch {
            pricingFeedback = error.localizedDescription
            return false
        }
    }

    func deleteCustomPricingPlan(_ planID: String) {
        customPricingPlans.removeAll { $0.id == planID }
        if selectedPricingPlanID == planID {
            selectedPricingPlanID = Self.defaultPricingPlanID
        }
        persistPricingSettings()
        Task { [weak self] in
            guard let self else { return }
            await self.refreshPricingCatalog()
            await self.repriceLoadedTokenHistory()
        }
    }

    static func microUSDPerMillion(from value: String) -> Int64? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty,
              trimmed.range(of: #"^\d{1,4}(?:\.\d{1,6})?$"#, options: .regularExpression) != nil,
              let decimal = Decimal(string: trimmed, locale: Locale(identifier: "en_US_POSIX")),
              decimal >= 0, decimal <= 1000 else { return nil }
        var scaled = decimal * Decimal(1_000_000)
        var rounded = Decimal()
        NSDecimalRound(&rounded, &scaled, 0, .plain)
        return NSDecimalNumber(decimal: rounded).int64Value
    }

    static func priceRateText(_ microUSDPerMillion: Int64) -> String {
        var value = String(format: "%.6f", Double(microUSDPerMillion) / 1_000_000)
        while value.contains(".") && value.last == "0" { value.removeLast() }
        if value.last == "." { value.removeLast() }
        return value
    }

    static func localHistoryRefreshDayCount(
        cachedAt: Date,
        hasCachedDays: Bool,
        now: Date = Date(),
        calendar: Calendar = .current
    ) -> Int {
        guard hasCachedDays, cachedAt != .distantPast else { return 30 }
        let cachedDay = calendar.startOfDay(for: cachedAt)
        let today = calendar.startOfDay(for: now)
        let elapsed = calendar.dateComponents([.day], from: cachedDay, to: today).day ?? 0
        return min(30, max(1, elapsed + 1))
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
                launchAction: projectLaunchActions[project.id]
            )
        }
        let catalogIDs = Set(items.map(\.id))
        items.append(contentsOf: projectDashboard.projects.filter {
            !$0.isUnassigned && !catalogIDs.contains($0.id)
        })
        return KSFProjectListOrdering.sort(items, stableOrder: projectListOrder)
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
        if coreServiceEnabled {
            Task { [weak self] in await self?.refreshSharedDashboard() }
        }
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
        createTask(for: project, purpose: .contextPreparation)
    }

    func createArchiveTask(for project: KSFProject) {
        createTask(for: project, purpose: .archiveProject)
    }

    private func createTask(for project: KSFProject, purpose: ProjectTaskPurpose) {
        guard !creatingProjectTaskIDs.contains(project.id) else { return }

        if coreServiceEnabled {
            createTaskUsingCoreService(for: project, purpose: purpose)
            return
        }
        projectTaskCreationErrors[project.id] = "核心服务不可用，无法新建任务。"
    }

    private func createTaskUsingCoreService(for project: KSFProject, purpose: ProjectTaskPurpose) {
        projectTaskCreationErrors[project.id] = nil
        creatingProjectTaskIDs.insert(project.id)
        if purpose == .archiveProject {
            archivingProjectTaskIDs.insert(project.id)
        }
        let purposeValue = purpose == .archiveProject ? "archiveProject" : "contextPreparation"
        Task { [weak self] in
            guard let self else { return }
            defer {
                self.creatingProjectTaskIDs.remove(project.id)
                self.archivingProjectTaskIDs.remove(project.id)
            }
            do {
                let created = try await self.coreService.createTask(
                    projectID: project.id,
                    ksfRoot: self.ksfRootPath,
                    purpose: purposeValue
                )
                try self.taskOpener.openTask(id: created.threadId)
                do {
                    try await Task.sleep(nanoseconds: 700_000_000)
                    try await self.coreService.submitTask(
                        threadID: created.threadId,
                        cwd: self.ksfRootPath,
                        prompt: created.prompt
                    )
                    self.projectTaskCreationErrors[project.id] = nil
                } catch {
                    self.projectTaskCreationErrors[project.id] = self.taskCreationErrorMessage(for: error)
                }
                await self.refreshSharedDashboard()
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
        panel.directoryURL = ksfRootPath.isEmpty
            ? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Documents", isDirectory: true)
            : URL(fileURLWithPath: ksfRootPath, isDirectory: true)
        guard panel.runModal() == .OK, let url = panel.url else { return }
        ksfRootPath = url.standardizedFileURL.path
        defaults.set(ksfRootPath, forKey: "ksfRootPath")
        projectDashboard = ProjectDashboardSnapshot(availability: .loading)
        localTokenHistoryTask?.cancel()
        localTokenHistoryTask = nil
        localTokenHistory = []
        tokenHistoryComparison = TokenHistoryComparison()
        localTokenHistoryError = nil
        onboardingError = nil
        completeOnboarding()
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

    func setFeishuTargetAlias(_ alias: String) {
        selectedFeishuTargetAlias = alias
        defaults.set(alias, forKey: "selectedFeishuTargetAlias")
    }

    func refreshFeishuConfiguration(refresh: Bool = false) async {
        await feishuConfigurationSession.read(refresh: refresh)
    }

    func reportFeishuConfigurationIssue(_ message: String) { feishuConfiguration.message = message }

    func performFeishuConfigurationAction(
        _ action: String, confirm: Bool = false, appID: String? = nil, appSecret: String? = nil,
        targetAlias: String? = nil, feature: String? = nil, mode: String? = nil, flowID: String? = nil,
        expectedContext: FeishuConfigurationContext
    ) {
        guard !feishuConfiguration.acting else { return }
        Task { [weak self] in
            await self?.feishuConfigurationSession.perform(
                action, confirm: confirm, appID: appID, appSecret: appSecret, targetAlias: targetAlias,
                feature: feature, mode: mode, flowID: flowID, expectedContext: expectedContext
            )
        }
    }

    func openFeishuFlowURL(flowID: String) {
        guard let flow = feishuConfiguration.flow, flow.id == flowID,
              let value = flow.verificationURL, let url = URL(string: value),
              url.scheme?.lowercased() == "https", let host = url.host?.lowercased(),
              ["feishu.cn", "larksuite.com", "larkoffice.com"].contains(where: { host == $0 || host.hasSuffix(".\($0)") })
        else { feishuFeedback = "飞书官方链接已失效，请重新检查配置。"; return }
        NSWorkspace.shared.open(url)
    }

    func refreshToolchainStatus() async {
        guard coreServiceEnabled, !toolchainActionInProgress else { return }
        toolchainActionInProgress = true
        defer { toolchainActionInProgress = false }
        do {
            toolchainStatus = try await coreService.toolchainStatus()
            toolchainFeedback = nil
        } catch {
            toolchainStatus = nil
            toolchainFeedback = "无法检查官方工具链，请重试。"
        }
    }

    func installToolchain() {
        guard coreServiceEnabled, !toolchainActionInProgress else { return }
        toolchainActionInProgress = true
        toolchainFeedback = nil
        Task { [weak self] in
            guard let self else { return }
            defer { self.toolchainActionInProgress = false }
            do {
                self.toolchainStatus = try await self.coreService.installToolchain()
                self.toolchainFeedback = self.toolchainStatus?.healthy == true
                    ? "技能已安装并校验完成；Codex 中的可用状态需在下一轮确认。"
                    : "安装后校验未通过，请重新检查。"
            } catch {
                self.toolchainStatus = try? await self.coreService.toolchainStatus()
                self.toolchainFeedback = "安装未完成，请查看具体文件或重新检查。"
            }
        }
    }



    func feishuTaskLink(for task: ProjectTaskItem) -> FeishuTaskLinkSnapshot? {
        guard let link = feishuTaskLinks[FeishuTaskLinkSnapshot.taskKey(for: task.threadID)],
              link.linkState == "active" else { return nil }
        return link
    }

    func feishuTaskLinkError(for task: ProjectTaskItem) -> String? {
        feishuTaskLinkErrors[task.id]
    }

    var feishuTaskLinkUnavailableReason: String? {
        if !coreServiceEnabled { return "本机服务未就绪，请打开设置检查运行组件。" }
        if feishuService.readinessBlockers.contains("taskCardWriteDisabled") {
            return "飞书写操作已关闭，无法发送或更新任务卡片。请在飞书设置中检查写操作。"
        }
        if feishuService.readinessBlockers.contains("taskCardWriteDryRun") {
            return "飞书写操作处于演练模式，不会实际发送任务卡片。"
        }
        if feishuService.availability != .ready {
            return "飞书消息或卡片服务未就绪，请在设置中检查飞书连接。"
        }
        if !feishuService.taskLinkReady { return "任务连接服务暂不可用，请在设置中查看诊断。" }
        if selectedFeishuTargetAlias.isEmpty { return "请先在飞书设置中选择接收人。" }
        return nil
    }

    func toggleFeishuTaskLink(_ task: ProjectTaskItem) {
        guard !feishuActionInProgress,
              !feishuTaskLinkActions.contains(task.id)
        else { return }
        let existing = feishuTaskLink(for: task)
        if existing == nil, let reason = feishuTaskLinkUnavailableReason {
            feishuTaskLinkErrors[task.id] = reason
            feishuFeedback = reason
            return
        }
        feishuTaskLinkActions.insert(task.id)
        feishuTaskLinkErrors.removeValue(forKey: task.id)
        feishuFeedback = nil
        let projectName = projectDashboard.projects.first(where: { $0.id == task.projectID })?.project?.name ?? "未分配项目"
        let title = task.name?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty == false ? task.name! : "未命名任务"
        let target = selectedFeishuTargetAlias
        Task { [weak self] in
            guard let self else { return }
            defer { self.feishuTaskLinkActions.remove(task.id) }
            do {
                let link: FeishuTaskLinkSnapshot
                if existing != nil {
                    link = try await self.coreService.releaseTaskLink(
                        threadID: task.threadID
                    )
                } else {
                    do {
                        link = try await self.coreService.createTaskLink(threadID: task.threadID, title: title, projectName: projectName, targetAlias: target)
                    } catch {
                        guard let authorization = FeishuTaskCardAuthorization.from(error) else { throw error }
                        guard self.userApprovalController.allowsConfigurationSubmission else {
                            throw CoreServiceError.remote("请在桌面完成当前对话框后，再次点击连接飞书。")
                        }
                        let alert = NSAlert()
                        alert.messageText = "发送任务卡片到飞书"
                        alert.informativeText = "接收人：\(target)\n任务：\(title)\n项目：\(projectName)\n\n以机器人身份发送包含任务状态和交互按钮的卡片。确认仅限本次发送。"
                        alert.addButton(withTitle: "取消")
                        alert.addButton(withTitle: "确认发送")
                        guard alert.runModal() == .alertSecondButtonReturn else {
                            try await self.coreService.cancelTaskCard(authorization, threadID: task.threadID)
                            throw CoreServiceError.remote("已取消发送任务卡片。")
                        }
                        guard self.userApprovalController.allowsConfigurationSubmission else {
                            throw CoreServiceError.remote("桌面暂不可交互，请重新检查本次发送。")
                        }
                        try await self.coreService.confirmTaskCard(authorization)
                        link = try await self.coreService.createTaskLink(threadID: task.threadID, title: title, projectName: projectName, targetAlias: target)
                    }
                }
                self.feishuTaskLinks[link.taskKey] = link
                self.feishuTaskLinkErrors.removeValue(forKey: task.id)
                self.feishuFeedback = existing == nil ? "“\(title)”已连接到飞书。" : "“\(title)”的飞书连接已解除。"
            } catch {
                let message = error.localizedDescription
                self.feishuTaskLinkErrors[task.id] = message
                self.feishuFeedback = message
            }
        }
    }

    var coreServiceStatusText: String {
        coreServiceEnabled ? "运行中" : "不可用"
    }

    func shutdown() async {
        guard !shutdownStarted else { return }
        shutdownStarted = true
        feishuConfigurationSession.shutdown()
        rateTimerTask?.cancel()
        coreServicePollTask?.cancel()
        await userApprovalController.stop()
        await coreService.stop()
    }

    func quit() {
        guard !quitRequested, !shutdownStarted else { return }
        quitRequested = true
        // AppDelegate owns cleanup for every exit path. Enter AppKit termination
        // from the run loop, not a Swift task/main-queue block: terminateLater
        // runs a nested modal loop and would block its own MainActor cleanup.
        NSApplication.shared.perform(
            #selector(NSApplication.terminate(_:)), with: nil, afterDelay: 0,
            inModes: [.common]
        )
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

    private func refreshSharedDashboard() async {
        guard coreServiceEnabled, !refreshingCoreService else { return }
        refreshingCoreService = true
        beginRefreshActivity()
        defer {
            refreshingCoreService = false
            endRefreshActivity()
        }
        do {
            let activeKSFRoot = isOnboardingComplete ? ksfRootPath : ""
            let dashboard = try await coreService.dashboard(
                ksfRoot: activeKSFRoot,
                pinnedProjectIDs: pinnedProjectIDs,
                pricingSelection: pricingSelection
            )
            snapshot = dashboard.usage
            store.save(dashboard.usage)
            switch dashboard.usageStatus {
            case "available": status = .available
            case "stale": status = .stale
            case "codexMissing": status = .codexMissing
            case "unsupportedProtocol": status = .unsupportedProtocol
            default: status = .offline
            }
            lastErrorMessage = dashboard.rateError
            tokenErrorMessage = dashboard.tokenError
            taskActivity = dashboard.activity

            projectUsage = Dictionary(uniqueKeysWithValues: dashboard.projects.projects.compactMap { item in
                item.usage.map { (item.id, $0) }
            })
            projectUsageStore.save(projectUsage)
            projectLaunchActions = Dictionary(uniqueKeysWithValues: dashboard.projects.projects.compactMap { item in
                item.launchAction.map { (item.id, $0) }
            })
            let reconciledOrder = KSFProjectListOrdering.reconcile(
                previous: projectListOrder,
                candidates: dashboard.projects.catalog.map(\.id)
                    + dashboard.projects.projects.filter { !$0.isUnassigned }.map(\.id)
            )
            if reconciledOrder != projectListOrder {
                projectListOrder = reconciledOrder
                defaults.set(projectListOrder, forKey: "projectListOrder")
            }
            let taskOrdered = dashboard.projects.projects.map { item -> ProjectDashboardItem in
                let order = ProjectTaskListOrdering.reconcile(
                    previous: projectTaskOrder[item.id] ?? [],
                    candidates: item.tasks
                )
                projectTaskOrder[item.id] = order
                return item.replacingTasks(ProjectTaskListOrdering.sort(item.tasks, stableOrder: order))
            }
            projectDashboard = ProjectDashboardSnapshot(
                availability: dashboard.projects.availability,
                projects: KSFProjectListOrdering.sort(taskOrdered, stableOrder: projectListOrder),
                catalog: dashboard.projects.catalog,
                observedAt: dashboard.projects.observedAt,
                message: dashboard.projects.message
            )

            feishuService = dashboard.feishu
            feishuTaskLinks = Dictionary(uniqueKeysWithValues: dashboard.feishuLinks.map { ($0.taskKey, $0) })
            if !dashboard.feishu.targetAliases.contains(selectedFeishuTargetAlias) {
                setFeishuTargetAlias(dashboard.feishu.targetAliases.count == 1 ? dashboard.feishu.targetAliases[0] : "")
            }
            feishuFeedback = nil
        } catch {
            status = snapshot?.headlineRemainingPercent == nil ? .offline : .stale
            lastErrorMessage = error.localizedDescription
            if taskActivity.availability == .loading {
                taskActivity = TaskActivitySnapshot(
                    runningCount: 0,
                    waitingCount: 0,
                    observedAt: Date(),
                    availability: .offline
                )
            }
            if projectDashboard.availability == .loading {
                projectDashboard = ProjectDashboardSnapshot(
                    availability: .unavailable,
                    projects: unavailablePinnedProjects(),
                    observedAt: Date(),
                    message: error.localizedDescription
                )
            }
        }
    }

    private func refreshPricingCatalog() async {
        guard coreServiceEnabled else { return }
        do {
            let catalog = try await coreService.pricingCatalog(customPlans: customPricingPlans)
            pricingCatalog = catalog
            customPricingPlans = catalog.plans.filter { !$0.builtIn }
            if !catalog.plans.contains(where: { $0.id == selectedPricingPlanID }) {
                selectedPricingPlanID = catalog.defaultPlanId
                pricingFeedback = "原价格方案已不可用，已切换为默认方案。"
            }
            persistPricingSettings()
        } catch {
            pricingFeedback = error.localizedDescription
        }
    }

    private func repriceLoadedTokenHistory() async {
        guard coreServiceEnabled else { return }
        guard !tokenHistoryComparison.days.isEmpty else {
            await refreshSharedDashboard()
            return
        }
        do {
            let comparison = try await coreService.tokenHistoryComparison(
                dayCount: 30,
                pricingSelection: pricingSelection,
                repriceOnly: true
            )
            applyTokenHistoryComparison(comparison)
            if let todayCost = comparison.days.last(where: { $0.startDate == todayDateString })?.localCost {
                var updated = snapshot ?? UsageSnapshot()
                updated.localDailyCost = todayCost
                snapshot = updated
                store.save(updated)
            }
        } catch {
            pricingFeedback = error.localizedDescription
        }
    }

    private func applyTokenHistoryComparison(_ comparison: TokenHistoryComparison) {
        tokenHistoryComparison = comparison
        localTokenHistory = comparison.days.map {
            DailyUsageBucket(
                startDate: $0.startDate,
                tokens: $0.localTokens,
                breakdown: $0.localBreakdown
            )
        }
        if comparison.pricingFallback, let plan = comparison.selectedPlan {
            selectedPricingPlanID = plan.id
            defaults.set(plan.id, forKey: "selectedPricingPlanID")
        }
    }

    private func persistPricingSettings() {
        defaults.set(selectedPricingPlanID, forKey: "selectedPricingPlanID")
        if let data = try? JSONEncoder().encode(customPricingPlans) {
            defaults.set(data, forKey: "customPricingPlansV1")
        }
    }

    private func startCoreServiceTimers() {
        rateTimerTask?.cancel()
        rateTimerTask = Task { [weak self] in
            while !Task.isCancelled {
                do { try await Task.sleep(nanoseconds: 300_000_000_000) } catch { break }
                guard !Task.isCancelled else { break }
                await self?.refreshSharedDashboard()
            }
        }
    }

    private func startCoreServicePolling() {
        guard popoverIsOpen, coreServicePollTask == nil else { return }
        coreServicePollTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self, self.popoverIsOpen else { break }
                let hasLiveState = self.taskActivity.runningCount > 0
                    || self.taskActivity.waitingCount > 0
                    || self.feishuTaskLinks.values.contains { $0.linkState == "active" }
                let interval: UInt64 = hasLiveState ? 3_000_000_000 : 15_000_000_000
                do { try await Task.sleep(nanoseconds: interval) } catch { break }
                guard !Task.isCancelled else { break }
                await self.refreshSharedDashboard()
            }
            self?.coreServicePollTask = nil
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
