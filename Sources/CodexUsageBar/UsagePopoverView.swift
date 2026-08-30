import AppKit
import CodexUsageCore
import CoreImage
import CoreImage.CIFilterBuiltins
import SwiftUI

struct UsagePopoverView: View {
    @ObservedObject var viewModel: UsageViewModel
    var refreshOnAppear = true
    @State private var page: Page = .home
    @State private var showAllProjects = false
    @State private var detailReturnPage: Page = .home

    var body: some View {
        Group {
            switch page {
            case .home:
                homePage
            case .projectLibrary:
                projectLibraryPage
            case .projectDetail:
                projectDetailPage
            case .settings:
                settingsPage
            case .wechat:
                weChatPage
            }
        }
        .padding(12)
        .frame(width: 336)
        .fixedSize(horizontal: false, vertical: true)
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.didBecomeKeyNotification)) { _ in
            page = .home
            showAllProjects = false
            detailReturnPage = .home
            if refreshOnAppear { viewModel.popoverDidOpen() }
        }
    }

    private var homePage: some View {
        VStack(alignment: .leading, spacing: 8) {
            homeHeader
            if let message = viewModel.statusMessage {
                compactStatus(message, color: .orange, symbol: "exclamationmark.triangle.fill")
            }
            if let bucket = viewModel.snapshot?.generalBucket {
                mainQuotaCard(bucket)
            }
            tokenActivity

            Divider()
            projectSectionHeader

            if let message = viewModel.projectDashboard.message {
                compactStatus(message, color: .orange, symbol: "exclamationmark.triangle.fill")
            }
            projectDetailList
            if let actionError = viewModel.projectActionError {
                compactStatus(actionError, color: .red, symbol: "exclamationmark.circle.fill")
            }
        }
    }

    private var homeHeader: some View {
        HStack(spacing: 8) {
            Text("Codex 用量")
                .font(.headline)
            Spacer()
            headerIconButton(systemName: "gearshape", label: "设置") {
                page = .settings
            }
        }
    }

    private var projectSectionHeader: some View {
        HStack(spacing: 6) {
            Text("KSF 项目")
                .font(.subheadline.weight(.semibold))
            if viewModel.isRefreshingProjects && viewModel.projectDashboard.availability != .loading {
                ProgressView()
                    .controlSize(.mini)
                    .scaleEffect(0.72)
                    .frame(width: 11, height: 11)
                    .accessibilityLabel("正在同步项目")
            }
            if !viewModel.homeProjectItems.isEmpty {
                Text("\(viewModel.homeProjectItems.count)")
                    .font(.caption2)
                    .monospacedDigit()
                    .foregroundStyle(.tertiary)
            }
            Spacer()
            headerIconButton(systemName: "square.grid.2x2", label: "项目列表") {
                page = .projectLibrary
            }
        }
    }

    @ViewBuilder
    private var projectDetailList: some View {
        let items = viewModel.homeProjectItems
        if viewModel.projectDashboard.availability == .loading {
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text("正在载入 KSF 项目…")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, minHeight: 34, alignment: .leading)
        } else if items.isEmpty {
            emptyState("暂无当前项目", detail: "这里仅显示已固定项目，以及有运行中或等待任务的项目。")
        } else {
            let visibleItems = showAllProjects ? items : Array(items.prefix(4))
            VStack(spacing: 6) {
                if projectRegionNeedsScroll(visibleItems) {
                    ScrollView(.vertical, showsIndicators: true) {
                        LazyVStack(spacing: 6) {
                            ForEach(visibleItems) { item in
                                projectDetailRow(item)
                            }
                        }
                        .padding(.trailing, 5)
                        .background(OverlayScrollerConfigurator())
                    }
                    .frame(height: 360)
                } else {
                    VStack(spacing: 6) {
                        ForEach(visibleItems) { item in
                            projectDetailRow(item)
                        }
                    }
                    .padding(.trailing, 5)
                }
                if items.count > 4 {
                    if showAllProjects {
                        projectListDisclosure(title: "收起", symbol: "chevron.up") {
                            showAllProjects = false
                        }
                    } else {
                        projectListDisclosure(title: "更多 \(items.count - 4) 个项目", symbol: "chevron.down") {
                            showAllProjects = true
                        }
                    }
                }
            }
        }
    }

    private func projectListDisclosure(
        title: String,
        symbol: String,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Label(title, systemImage: symbol)
                .font(.caption.weight(.medium))
                .foregroundStyle(Color.accentColor)
                .frame(maxWidth: .infinity, minHeight: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    @ViewBuilder
    private func projectDetailRow(_ item: ProjectDashboardItem) -> some View {
        if let project = item.project {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 7) {
                    Text(project.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    projectTaskStatus(item)
                    projectRowIconButton(
                        systemName: item.isPinned ? "pin.fill" : "pin",
                        label: item.isPinned ? "取消固定" : "固定项目"
                    ) {
                        viewModel.togglePinned(item.id)
                    }
                }

                if !item.tasks.isEmpty {
                    VStack(spacing: 0) {
                        ForEach(Array(item.tasks.enumerated()), id: \.element.id) { index, task in
                            projectTaskRow(task)
                            if index < item.tasks.count - 1 {
                                Divider().padding(.leading, 16)
                            }
                        }
                    }
                    .padding(.vertical, 1)
                }

                if let failure = viewModel.taskOpenFailure, failure.projectID == item.id {
                    Label(failure.message, systemImage: "exclamationmark.circle.fill")
                        .font(.caption2)
                        .foregroundStyle(.red)
                        .lineLimit(1)
                }

                if let message = viewModel.projectTaskCreationErrors[item.id] {
                    Label(message, systemImage: "exclamationmark.circle.fill")
                        .font(.caption2)
                        .foregroundStyle(.red)
                        .lineLimit(2)
                }

                HStack(spacing: 7) {
                    projectInlineMetric("累计", value: projectTokenValue(item.usage, keyPath: \.cumulativeTokens))
                    projectInlineMetric("今日", value: projectTokenValue(item.usage, keyPath: \.todayTokens))
                    Spacer(minLength: 2)
                    projectNewTaskIconControl(project: project)
                    projectRowIconButton(systemName: "folder", label: "打开 KSF 文件夹") {
                        viewModel.openProjectDirectory(project)
                    }
                    projectEngineeringIconControl(project: project, item: item)
                    projectLaunchIconControl(item)
                    projectRowIconButton(systemName: "info.circle", label: "项目详情") {
                        viewModel.selectProject(item.id)
                        detailReturnPage = .home
                        page = .projectDetail
                    }
                }
            }
            .padding(8)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            .accessibilityElement(children: .contain)
            .accessibilityLabel(projectAccessibilityLabel(item))
        } else {
            VStack(alignment: .leading, spacing: 5) {
                HStack(spacing: 7) {
                    Label("项目不可用", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.orange)
                    Spacer()
                    projectRowIconButton(systemName: "pin.slash", label: "取消固定") {
                        viewModel.togglePinned(item.id)
                    }
                }
                Text(unavailableProjectName(item.id))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                projectRowIconButton(systemName: "folder", label: "定位 KSF") {
                    viewModel.openKSFFolder()
                }
            }
            .padding(8)
            .background(Color.orange.opacity(0.10), in: RoundedRectangle(cornerRadius: 10))
        }
    }

    private func projectTaskRow(_ task: ProjectTaskItem) -> some View {
        Button {
            viewModel.openTask(task)
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 5) {
                    Image(systemName: taskStatusSymbol(task))
                        .font(.system(size: 9, weight: .semibold))
                        .foregroundStyle(task.classification == .waiting ? Color.orange : .secondary)
                        .frame(width: 11)
                    Text(taskDisplayName(task))
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                    Spacer(minLength: 5)
                    Text(taskStatusText(task))
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(task.classification == .waiting ? Color.orange : .secondary)
                        .lineLimit(1)
                    Image(systemName: "chevron.right")
                        .font(.system(size: 8, weight: .semibold))
                        .foregroundStyle(.tertiary)
                }
                Text(taskRouteText(task.route))
                    .font(.caption2)
                    .foregroundStyle(task.route == nil ? .tertiary : .secondary)
                    .lineLimit(1)
                    .padding(.leading, 16)
            }
            .padding(.vertical, 3)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(taskAccessibilityLabel(task))
        .help("在 Codex 中打开任务")
    }

    private func projectInlineMetric(_ label: String, value: String) -> some View {
        HStack(spacing: 2) {
            Text(label)
                .foregroundStyle(.secondary)
            Text(value)
                .fontWeight(.semibold)
                .monospacedDigit()
        }
        .font(.caption2)
        .lineLimit(1)
        .accessibilityLabel("项目\(label) Token，\(value)")
    }

    private func projectRowIconButton(
        systemName: String,
        label: String,
        disabled: Bool = false,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: 11, weight: .medium))
                .frame(width: 20, height: 20)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(disabled)
        .help(label)
        .accessibilityLabel(label)
    }

    @ViewBuilder
    private func projectNewTaskIconControl(project: KSFProject) -> some View {
        if viewModel.creatingProjectTaskIDs.contains(project.id) {
            ProgressView()
                .controlSize(.small)
                .scaleEffect(0.7)
                .frame(width: 20, height: 20)
                .help("正在新建 Codex 任务")
                .accessibilityLabel("正在新建 Codex 任务")
        } else {
            projectRowIconButton(systemName: "plus.bubble", label: "在 KSF 项目中新建 Codex 任务") {
                viewModel.createTask(for: project)
            }
        }
    }

    @ViewBuilder
    private func projectEngineeringIconControl(project: KSFProject, item: ProjectDashboardItem) -> some View {
        let mappings = preferredMappings(project.engineeringMappings, preferredID: item.preferredEngineeringID)
        if mappings.isEmpty {
            projectRowIconButton(systemName: "folder.badge.questionmark", label: "没有 Git 工程", disabled: true) {}
        } else if let mapping = mappings.first, mappings.count == 1 {
            projectRowIconButton(systemName: "folder.badge.gearshape", label: "打开 Git 工程") {
                viewModel.openEngineering(mapping)
            }
        } else {
            Menu {
                ForEach(mappings) { mapping in
                    Button {
                        viewModel.openEngineering(mapping)
                    } label: {
                        Label(mapping.role, systemImage: mapping.id == item.preferredEngineeringID ? "checkmark" : "folder")
                    }
                }
            } label: {
                Image(systemName: "folder.badge.gearshape")
                    .font(.system(size: 11, weight: .medium))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("选择 Git 工程")
            .accessibilityLabel("选择 Git 工程")
        }
    }

    @ViewBuilder
    private func projectLaunchIconControl(_ item: ProjectDashboardItem) -> some View {
        if item.actions.isEmpty {
            projectRowIconButton(systemName: "play.slash", label: "没有启动动作", disabled: true) {}
        } else if let action = item.actions.first, item.actions.count == 1 {
            projectRowIconButton(systemName: action.symbol, label: action.title) {
                viewModel.launchAction(action)
            }
        } else if item.actions.count > 1 {
            Menu {
                ForEach(item.actions) { action in
                    Button {
                        viewModel.launchAction(action)
                    } label: {
                        Label(action.title, systemImage: action.symbol)
                    }
                }
            } label: {
                Image(systemName: "play.fill")
                    .font(.system(size: 11, weight: .medium))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("启动项目动作")
            .accessibilityLabel("启动项目动作")
        }
    }

    private func projectTaskStatus(_ item: ProjectDashboardItem) -> some View {
        HStack(spacing: 5) {
            switch viewModel.taskActivity.availability {
            case .loading:
                Label("…", systemImage: "play.fill")
            case .unsupportedProtocol, .offline:
                Label("—", systemImage: "play.fill")
                Label("—", systemImage: "person.fill.questionmark")
            case .available, .desktopNotRunning:
                Label("\(item.runningCount)", systemImage: "play.fill")
                if item.waitingCount > 0 {
                    Label("\(item.waitingCount)", systemImage: "person.fill.questionmark")
                }
            }
        }
        .font(.system(size: 10, weight: .medium, design: .rounded))
        .monospacedDigit()
        .foregroundStyle(item.waitingCount > 0 ? Color.orange : .secondary)
    }

    private func projectTokenRow(_ usage: ProjectUsageSummary?) -> some View {
        HStack(spacing: 0) {
            projectMetric("项目累计 Token", value: projectTokenValue(usage, keyPath: \.cumulativeTokens))
            Divider().frame(height: 25)
            projectMetric("项目今日 Token", value: projectTokenValue(usage, keyPath: \.todayTokens))
        }
    }

    private func projectMetric(_ label: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.system(.callout, design: .rounded, weight: .semibold))
                .monospacedDigit()
                .lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private func engineeringControl(project: KSFProject, item: ProjectDashboardItem) -> some View {
        let mappings = preferredMappings(project.engineeringMappings, preferredID: item.preferredEngineeringID)
        if mappings.isEmpty {
            compactAction(title: "无工程", symbol: "folder.badge.questionmark", disabled: true) {}
        } else if let mapping = mappings.first, mappings.count == 1 {
            compactAction(title: "Git 工程", symbol: "folder.badge.gearshape") {
                viewModel.openEngineering(mapping)
            }
        } else {
            Menu {
                ForEach(mappings) { mapping in
                    Button {
                        viewModel.openEngineering(mapping)
                    } label: {
                        Label(mapping.role, systemImage: mapping.id == item.preferredEngineeringID ? "checkmark" : "folder")
                    }
                }
            } label: {
                Label("Git 工程", systemImage: "folder.badge.gearshape")
                    .font(.caption)
                    .frame(maxWidth: .infinity)
            }
            .menuStyle(.borderlessButton)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity)
            .help("选择 Git 工程")
        }
    }

    private func compactAction(
        title: String,
        symbol: String,
        disabled: Bool = false,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Label(title, systemImage: symbol)
                .font(.caption)
                .lineLimit(1)
                .frame(maxWidth: .infinity)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .disabled(disabled)
    }

    private func mainQuotaCard(_ bucket: RateLimitBucket) -> some View {
        let remaining = bucket.headlineRemainingPercent ?? 0
        return VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .lastTextBaseline, spacing: 6) {
                Text("\(remaining)%")
                    .font(.system(size: 23, weight: .semibold, design: .rounded))
                    .monospacedDigit()
                Text("通用额度剩余")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                if let updated = viewModel.snapshot?.rateUpdatedAt {
                    Text(formatTime(updated))
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }
            }
            ProgressView(value: Double(remaining), total: 100)
                .controlSize(.small)
                .tint(progressColor(remaining))
            ForEach(Array(bucket.windows.prefix(2).enumerated()), id: \.offset) { _, window in
                windowRow(window)
            }
        }
        .padding(9)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    private var tokenActivity: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text("Token 活动")
                .font(.caption.weight(.semibold))
            if viewModel.snapshot?.tokenSummary != nil || viewModel.localTodayUsage != nil {
                let breakdown = viewModel.localTodayUsage?.breakdown
                VStack(spacing: 4) {
                    HStack(spacing: 0) {
                        metric("模型普通输入", value: formatTokens(breakdown?.regularInputTokens))
                        Divider().frame(height: 25)
                        metric("模型缓存输入", value: formatTokens(breakdown?.cachedInputTokens))
                        Divider().frame(height: 25)
                        metric("模型输出", value: formatTokens(breakdown?.outputTokens))
                    }
                    Divider()
                    HStack(spacing: 0) {
                        metric(
                            accountLatestLabel,
                            value: viewModel.accountLatestUsage.map { formatTokens($0.tokens) } ?? "未同步"
                        )
                        Divider().frame(height: 25)
                        metric(
                            "本机昨日",
                            value: viewModel.localYesterdayUsage.map { formatTokens($0.tokens) } ?? "不可用"
                        )
                        Divider().frame(height: 25)
                        metric(
                            "本机今日",
                            value: viewModel.localTodayUsage.map { formatTokens($0.tokens) } ?? "不可用",
                            isRefreshing: viewModel.isRefreshing
                        )
                    }
                }
                .padding(.vertical, 5)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            } else {
                Text(viewModel.tokenErrorMessage ?? "Token 活动尚未载入。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var projectLibraryPage: some View {
        VStack(alignment: .leading, spacing: 9) {
            secondaryHeader(title: "项目列表", backLabel: "返回主页") { page = .home }
            if viewModel.projectLibraryItems.isEmpty {
                emptyState("没有可用项目", detail: viewModel.projectDashboard.message ?? "KSF 尚未导出项目目录。")
            } else {
                ScrollView(.vertical, showsIndicators: true) {
                    LazyVStack(spacing: 0) {
                        ForEach(viewModel.projectLibraryItems) { item in
                            HStack(spacing: 7) {
                                Button {
                                    viewModel.selectProject(item.id)
                                    detailReturnPage = .projectLibrary
                                    page = .projectDetail
                                } label: {
                                    HStack(spacing: 7) {
                                        Image(systemName: item.isAvailable ? "folder" : "exclamationmark.triangle")
                                            .foregroundStyle(item.isAvailable ? Color.secondary : .orange)
                                            .frame(width: 14)
                                        VStack(alignment: .leading, spacing: 1) {
                                            Text(item.project?.name ?? unavailableProjectName(item.id))
                                                .font(.caption.weight(item.id == viewModel.selectedProjectID ? .semibold : .regular))
                                                .lineLimit(1)
                                            Text(projectLibraryStatus(item))
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                                .lineLimit(1)
                                        }
                                        Spacer()
                                        Image(systemName: "chevron.right")
                                            .font(.system(size: 9, weight: .semibold))
                                            .foregroundStyle(.tertiary)
                                    }
                                    .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)

                                headerIconButton(
                                    systemName: item.isPinned ? "pin.fill" : "pin",
                                    label: item.isPinned ? "取消固定" : "固定项目"
                                ) {
                                    viewModel.togglePinned(item.id)
                                }
                            }
                            .padding(.horizontal, 8)
                            .padding(.vertical, 6)
                            if item.id != viewModel.projectLibraryItems.last?.id { Divider() }
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
                    .padding(.trailing, 8)
                    .background(OverlayScrollerConfigurator())
                }
                .frame(height: projectLibraryViewportHeight)
            }
        }
    }

    private var projectDetailPage: some View {
        VStack(alignment: .leading, spacing: 9) {
            secondaryHeader(title: "项目详情", backLabel: "返回") { page = detailReturnPage }
            if let item = viewModel.selectedProject, let project = item.project {
                VStack(alignment: .leading, spacing: 7) {
                    HStack {
                        Text(project.name)
                            .font(.subheadline.weight(.semibold))
                            .lineLimit(1)
                        Spacer()
                        projectTaskStatus(item)
                    }
                    detailLine("阶段", value: project.phase ?? "未登记", lines: 2)
                    if let focus = project.focus { detailLine("重点", value: focus, lines: 2) }
                    Divider()
                    VStack(alignment: .leading, spacing: 5) {
                        detailLine("工作类别", value: item.primaryRoute?.category?.name ?? "尚无已验真路由")
                        detailLine("岗位", value: joined(item.primaryRoute?.jobs.compactMap(\.name)))
                        detailLine("基本功", value: joined(item.primaryRoute?.abilities.compactMap(\.name)), lines: 2)
                        detailLine("可调度 Skill", value: joined(item.primaryRoute?.dispatchableSkills.compactMap(\.skillID)), lines: 2)
                    }
                    Divider()
                    projectTokenRow(item.usage)
                    Text(projectUsageScope(item.usage))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                .padding(10)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))

                HStack(spacing: 5) {
                    compactAction(title: "项目记忆", symbol: "doc.text") { viewModel.openProjectCard(project) }
                    engineeringControl(project: project, item: item)
                    if item.actions.isEmpty {
                        compactAction(title: "无启动动作", symbol: "play.slash", disabled: true) {}
                    } else {
                        Menu {
                            ForEach(item.actions) { action in
                                Button {
                                    viewModel.launchAction(action)
                                } label: {
                                    Label(action.title, systemImage: action.symbol)
                                }
                            }
                        } label: {
                            Label("启动动作", systemImage: "play.fill")
                                .font(.caption)
                                .frame(maxWidth: .infinity)
                        }
                        .menuStyle(.borderlessButton)
                        .frame(maxWidth: .infinity)
                    }
                }
            } else {
                emptyState("项目不可用", detail: "项目卡已失效或归档，只能取消固定或重新选择。")
            }
        }
    }

    private var settingsPage: some View {
        VStack(alignment: .leading, spacing: 10) {
            secondaryHeader(title: "设置", backLabel: "返回工作台") { page = .home }

            VStack(spacing: 0) {
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 1) {
                        Text("KSF 目录")
                            .font(.caption)
                        Text(ksfDirectoryStatus)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Spacer()
                    Button("重新选择") { viewModel.chooseKSFRoot() }
                        .controlSize(.small)
                }
                .padding(.vertical, 7)
                Divider()
                settingRow(
                    title: "登录时启动",
                    status: viewModel.loginItemState.rawValue,
                    isOn: Binding(
                        get: { viewModel.launchAtLoginEnabled },
                        set: { viewModel.setLaunchAtLogin($0) }
                    )
                )
                Divider()
                settingRow(
                    title: "通用额度重置通知",
                    status: viewModel.notificationPermission.rawValue,
                    isOn: Binding(
                        get: { viewModel.resetNotificationsEnabled },
                        set: { viewModel.setResetNotifications($0) }
                    )
                )
                Divider()
                Button {
                    page = .wechat
                } label: {
                    HStack(spacing: 8) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("微信连接").font(.caption)
                            Text(viewModel.weChatStatusText)
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                        }
                        Spacer()
                        Image(systemName: "chevron.right")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.tertiary)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .padding(.vertical, 7)
                .accessibilityLabel("微信连接，\(viewModel.weChatStatusText)")
            }
            .padding(.horizontal, 10)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))

            Divider()
            Button(role: .destructive) { viewModel.quit() } label: {
                Label("退出", systemImage: "power")
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundStyle(.red)
            .padding(.horizontal, 10)
            .accessibilityLabel("退出 Codex Usage Bar")
        }
    }

    private var weChatPage: some View {
        VStack(alignment: .leading, spacing: 10) {
            secondaryHeader(title: "微信连接", backLabel: "返回设置") { page = .settings }

            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 7) {
                    Image(systemName: weChatStatusSymbol)
                        .foregroundStyle(weChatStatusColor)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(viewModel.weChatStatusText)
                            .font(.caption.weight(.semibold))
                        Text(weChatStatusDetail)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    if viewModel.weChatActionInProgress || viewModel.weChatState == .connecting {
                        ProgressView().controlSize(.small)
                    }
                }

                if let content = viewModel.weChatQRCodeContent,
                   let image = qrCodeImage(for: content) {
                    VStack(spacing: 6) {
                        Image(nsImage: image)
                            .interpolation(.none)
                            .resizable()
                            .frame(width: 152, height: 152)
                            .accessibilityLabel("微信连接二维码")
                        Text("使用手机微信扫码并确认连接")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 4)
                }

                if let feedback = viewModel.weChatFeedback {
                    Text(feedback)
                        .font(.caption2)
                        .foregroundStyle(feedback.contains("成功") || feedback.contains("已发送") ? .green : .secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }

                weChatActions
            }
            .padding(10)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))

            Text("仅接收扫码账号本人的文字单聊。收到的命令会加密保存在本机待处理队列；当前不会执行命令或连接 Codex 任务。")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder
    private var weChatActions: some View {
        switch viewModel.weChatState {
        case .connected:
            HStack(spacing: 8) {
                Button("发送测试消息") { viewModel.sendWeChatTestMessage() }
                    .controlSize(.small)
                    .disabled(viewModel.weChatActionInProgress)
                Spacer()
                Button("断开", role: .destructive) { viewModel.disconnectWeChat() }
                    .controlSize(.small)
                    .disabled(viewModel.weChatActionInProgress)
            }
        case .reconnecting:
            HStack(spacing: 8) {
                Text("网络恢复后会自动继续接收。")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Spacer()
                Button("断开", role: .destructive) { viewModel.disconnectWeChat() }
                    .controlSize(.small)
            }
        case .awaitingScan, .connecting:
            Button("刷新二维码") { viewModel.connectWeChat() }
                .controlSize(.small)
                .disabled(viewModel.weChatActionInProgress)
        case .disconnected, .credentialsExpired, .failed:
            Button(viewModel.weChatState == .disconnected ? "连接微信" : "重新连接") {
                viewModel.connectWeChat()
            }
            .controlSize(.small)
            .disabled(viewModel.weChatActionInProgress)
        }
    }

    private var weChatStatusSymbol: String {
        switch viewModel.weChatState {
        case .connected: return "checkmark.circle.fill"
        case .awaitingScan, .connecting: return "qrcode.viewfinder"
        case .reconnecting: return "arrow.triangle.2.circlepath"
        case .credentialsExpired, .failed: return "exclamationmark.triangle.fill"
        case .disconnected: return "link.badge.plus"
        }
    }

    private var weChatStatusColor: Color {
        switch viewModel.weChatState {
        case .connected: return .green
        case .credentialsExpired, .failed: return .orange
        default: return .secondary
        }
    }

    private var weChatStatusDetail: String {
        switch viewModel.weChatState {
        case .disconnected: return "扫码后自动保持连接"
        case .awaitingScan: return "二维码五分钟内有效"
        case .connecting: return "请在手机微信中确认"
        case .connected: return "正在接收消息"
        case .reconnecting: return "连接暂时中断"
        case .credentialsExpired: return "需要重新扫码授权"
        case let .failed(message): return message
        }
    }

    private func qrCodeImage(for content: String) -> NSImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(content.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)) else {
            return nil
        }
        let context = CIContext(options: [.useSoftwareRenderer: false])
        guard let cgImage = context.createCGImage(output, from: output.extent) else { return nil }
        return NSImage(cgImage: cgImage, size: NSSize(width: output.extent.width, height: output.extent.height))
    }

    private func secondaryHeader(title: String, backLabel: String, action: @escaping () -> Void) -> some View {
        HStack(spacing: 7) {
            headerIconButton(systemName: "chevron.left", label: backLabel, action: action)
            Text(title).font(.headline)
            Spacer()
        }
    }

    private func headerIconButton(
        systemName: String,
        label: String,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: 12, weight: .semibold))
                .frame(width: 22, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(label)
        .accessibilityLabel(label)
    }

    private func compactStatus(_ message: String, color: Color, symbol: String) -> some View {
        Label(message, systemImage: symbol)
            .font(.caption2)
            .foregroundStyle(color)
            .lineLimit(1)
            .padding(.horizontal, 7)
            .padding(.vertical, 4)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(color.opacity(0.09), in: RoundedRectangle(cornerRadius: 7))
    }

    private func settingRow(title: String, status: String, isOn: Binding<Bool>) -> some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.caption)
                Text(status).font(.caption2).foregroundStyle(.tertiary)
            }
            Spacer()
            Toggle("", isOn: isOn)
                .labelsHidden()
                .toggleStyle(.switch)
                .controlSize(.small)
        }
        .padding(.vertical, 7)
    }

    private func windowRow(_ window: RateLimitWindow) -> some View {
        HStack(spacing: 6) {
            Text(windowLabel(window.windowDurationMins)).foregroundStyle(.secondary)
            Spacer(minLength: 8)
            Text("\(window.remainingPercent)%").monospacedDigit()
            if let reset = window.resetDate {
                Text("· \(formatResetTime(reset)) 重置").foregroundStyle(.secondary)
            }
        }
        .font(.caption)
    }

    private func metric(_ label: String, value: String, isRefreshing: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 3) {
                Text(label)
                if isRefreshing {
                    ProgressView()
                        .controlSize(.mini)
                        .scaleEffect(0.65)
                        .frame(width: 9, height: 9)
                        .accessibilityHidden(true)
                }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .accessibilityElement(children: .combine)
            .accessibilityLabel(isRefreshing ? "\(label)，正在刷新" : label)
            Text(value)
                .font(.system(.body, design: .rounded, weight: .semibold))
                .monospacedDigit()
                .lineLimit(1)
        }
        .padding(.horizontal, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func detailLine(_ label: String, value: String, lines: Int = 1) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .frame(width: 58, alignment: .leading)
            Text(value)
                .font(.caption)
                .lineLimit(lines)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private func emptyState(_ title: String, detail: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.caption.weight(.semibold))
            Text(detail).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    private var projectLibraryViewportHeight: CGFloat {
        min(max(CGFloat(viewModel.projectLibraryItems.count) * 42, 42), 420)
    }

    private var ksfDirectoryStatus: String {
        switch viewModel.projectDashboard.availability {
        case .available:
            return "已连接 · \(viewModel.projectDashboard.catalog.count) 个活动项目"
        case .loading:
            return "正在检查"
        case .unavailable:
            return "目录不可用"
        case .unsupportedProtocol:
            return "桥接版本不兼容"
        }
    }

    private func projectLibraryStatus(_ item: ProjectDashboardItem) -> String {
        guard item.isAvailable else { return "不可用 · 仅可取消固定" }
        if viewModel.taskActivity.availability == .unsupportedProtocol
            || viewModel.taskActivity.availability == .offline
        {
            return "任务状态不可用"
        }
        var parts: [String] = []
        if item.waitingCount > 0 { parts.append("等待 \(item.waitingCount)") }
        if item.runningCount > 0 { parts.append("运行 \(item.runningCount)") }
        if parts.isEmpty { parts.append(item.isPinned ? "已固定" : "项目库") }
        return parts.joined(separator: " · ")
    }

    private func projectAccessibilityLabel(_ item: ProjectDashboardItem) -> String {
        let name = item.project?.name ?? unavailableProjectName(item.id)
        return "\(name)，\(item.runningCount) 个任务运行中，\(item.waitingCount) 个任务等待处理"
    }

    private func taskDisplayName(_ task: ProjectTaskItem) -> String {
        guard let name = task.name, !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return "未命名任务"
        }
        return name
    }

    private func taskStatusSymbol(_ task: ProjectTaskItem) -> String {
        task.classification == .waiting ? "person.fill.questionmark" : "play.fill"
    }

    private func taskStatusText(_ task: ProjectTaskItem) -> String {
        guard task.classification == .waiting else { return "运行中" }
        switch task.waitingReason {
        case .approval: return "待批准"
        case .planConfirmation: return "待确认计划"
        case .userInput: return "待回复"
        case .actionRequired, .none: return "待处理"
        }
    }

    private func taskRouteText(_ route: KSFRouteSummary?) -> String {
        guard let route else { return "未绑定 KSF 路由" }
        return [
            route.category?.name ?? "未分类",
            route.mainJob?.name ?? "无主岗位",
            "\(route.abilities.count) 基本功",
            "\(route.dispatchableSkills.count) Skill",
        ].joined(separator: " · ")
    }

    private func taskAccessibilityLabel(_ task: ProjectTaskItem) -> String {
        "\(taskDisplayName(task))，\(taskStatusText(task))，\(taskRouteText(task.route))，打开 Codex 任务"
    }

    private func projectRegionNeedsScroll(_ items: [ProjectDashboardItem]) -> Bool {
        let estimatedHeight = items.reduce(CGFloat.zero) { result, item in
            let base: CGFloat = item.isAvailable ? 58 : 74
            let tasks = CGFloat(item.tasks.count) * 34
            let openError: CGFloat = viewModel.taskOpenFailure?.projectID == item.id ? 18 : 0
            let creationError: CGFloat = viewModel.projectTaskCreationErrors[item.id] == nil ? 0 : 32
            return result + base + tasks + openError + creationError
        } + CGFloat(max(items.count - 1, 0)) * 6
        return estimatedHeight > 360
    }

    private func projectTokenValue(
        _ usage: ProjectUsageSummary?,
        keyPath: KeyPath<ProjectUsageSummary, Int64>
    ) -> String {
        guard let usage else { return "未统计" }
        let value = usage[keyPath: keyPath]
        if !usage.isComplete && value == 0 { return "不完整" }
        return formatTokens(value) + (usage.isComplete ? "" : "＋")
    }

    private func projectUsageScope(_ usage: ProjectUsageSummary?) -> String {
        guard let usage else { return "尚未建立本机项目 Token 统计。" }
        let since = Self.shortDateFormatter.string(from: usage.trackingStartedAt)
        if usage.isComplete { return "本机 JSONL 统计，自 \(since) 起。" }
        return "本机 JSONL 统计，自 \(since) 起；\(usage.uncountedThreadCount) 个远程或缺日志任务未计入。"
    }

    private func joined(_ values: [String]?) -> String {
        guard let values, !values.isEmpty else { return "—" }
        return values.joined(separator: "、")
    }

    private func preferredMappings(
        _ mappings: [KSFEngineeringMapping],
        preferredID: String?
    ) -> [KSFEngineeringMapping] {
        mappings.sorted { left, right in
            if left.id == preferredID { return true }
            if right.id == preferredID { return false }
            return left.role.localizedStandardCompare(right.role) == .orderedAscending
        }
    }

    private func unavailableProjectName(_ id: String) -> String {
        URL(fileURLWithPath: id).deletingLastPathComponent().lastPathComponent
    }

    private func progressColor(_ remaining: Int) -> Color {
        if remaining == 0 { return .red }
        if remaining < 20 { return .orange }
        return .accentColor
    }

    private func windowLabel(_ minutes: Int64?) -> String {
        guard let minutes else { return "额度窗口" }
        if minutes % 1_440 == 0 { return "\(minutes / 1_440) 天窗口" }
        if minutes % 60 == 0 { return "\(minutes / 60) 小时窗口" }
        return "\(minutes) 分钟窗口"
    }

    private func formatTime(_ date: Date) -> String { Self.timeFormatter.string(from: date) }
    private func formatResetTime(_ date: Date) -> String { Self.resetTimeFormatter.string(from: date) }

    private func formatTokens(_ value: Int64?) -> String {
        guard let value else { return "—" }
        return formatTokens(value)
    }

    private func formatTokens(_ value: Int64) -> String {
        let number = Double(value)
        if abs(number) >= 1_000_000_000 { return String(format: "%.2fB", number / 1_000_000_000) }
        if abs(number) >= 1_000_000 { return String(format: "%.1fM", number / 1_000_000) }
        if abs(number) >= 1_000 { return String(format: "%.1fK", number / 1_000) }
        return NumberFormatter.localizedString(from: NSNumber(value: value), number: .decimal)
    }

    private var accountLatestLabel: String {
        guard let startDate = viewModel.accountLatestUsage?.startDate else { return "账号最新" }
        let now = Date()
        let calendar = Calendar.current
        if startDate == LocalTokenUsageReader.dateString(for: now, calendar: calendar) {
            return "账号最新 · 今日"
        }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now),
           startDate == LocalTokenUsageReader.dateString(for: yesterday, calendar: calendar) {
            return "账号最新 · 昨日"
        }
        guard let displayDate = Self.accountUsageDateFormatter.date(from: startDate) else {
            return "账号最新"
        }
        return "账号最新 · \(Self.accountUsageDisplayFormatter.string(from: displayDate))"
    }

    private static let timeFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "zh_Hans_CN")
        formatter.dateFormat = "HH:mm"
        return formatter
    }()

    private static let resetTimeFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "zh_Hans_CN")
        formatter.dateFormat = "M月d日 HH:mm"
        return formatter
    }()

    private static let shortDateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "zh_Hans_CN")
        formatter.dateFormat = "M月d日"
        return formatter
    }()

    private static let accountUsageDateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()

    private static let accountUsageDisplayFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "zh_Hans_CN")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.dateFormat = "M月d日"
        return formatter
    }()

    private enum Page {
        case home
        case projectLibrary
        case projectDetail
        case settings
        case wechat
    }
}

private struct OverlayScrollerConfigurator: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView {
        let view = NSView(frame: .zero)
        configure(view)
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        configure(nsView)
    }

    private func configure(_ view: NSView) {
        DispatchQueue.main.async {
            guard let scrollView = view.enclosingScrollView else { return }
            scrollView.scrollerStyle = .overlay
            scrollView.autohidesScrollers = true
        }
    }
}
