import AppKit
import KSFAssistantCore
import SwiftUI

struct UsagePopoverView: View {
    @ObservedObject var viewModel: UsageViewModel
    var refreshOnAppear = true
    @State private var page: Page = .home
    @State private var showAllProjects = false
    @State private var showAllWorkspaces = false
    @State private var selectedTaskID: String?
    @State private var selectedLocalHistoryDate: String?
    @State private var editingPricingPlanID: String?
    @State private var pricingEditorVisible = false
    @State private var pricingProvider = ""
    @State private var pricingModel = ""
    @State private var pricingVariant = ""
    @State private var pricingRegularInput = ""
    @State private var pricingCachedInput = ""
    @State private var pricingOutput = ""
    @State private var pricingFormError: String?
    @State private var feishuAppID = ""
    @State private var feishuAppSecret = ""
    @State private var showExistingFeishuApp = false
    @State private var feishuExpandedSection: String?
    @State private var pendingFeishuAction: FeishuActionIntent?
    @State private var confirmToolchainInstall = false

    var body: some View {
        Group {
            switch page {
            case .home:
                homePage
            case .tokenHistory:
                tokenHistoryPage
            case .projectLibrary:
                projectLibraryPage
            case .workspaceLibrary:
                workspaceLibraryPage
            case .taskDetail:
                taskDetailPage
            case .settings:
                settingsPage
            case .pricing:
                pricingPage
            case .feishu:
                feishuPage
            }
        }
        .padding(12)
        .frame(width: 336)
        .fixedSize(horizontal: false, vertical: true)
        .onChange(of: viewModel.popoverPresentationID) { _ in
            page = .home
            showAllProjects = false
            showAllWorkspaces = false
            selectedTaskID = nil
            selectedLocalHistoryDate = nil
            pendingFeishuAction = nil
        }
        .onAppear {
            if refreshOnAppear { viewModel.popoverDidOpen() }
        }
        .onChange(of: viewModel.tokenHistoryComparison.days.map(\.startDate)) { dates in
            if selectedLocalHistoryDate == nil || !dates.contains(selectedLocalHistoryDate ?? "") {
                selectedLocalHistoryDate = dates.last
            }
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

            if viewModel.isOnboardingComplete {
                Divider()
                projectSectionHeader
                if let message = viewModel.projectDashboard.message {
                    compactStatus(message, color: .orange, symbol: "exclamationmark.triangle.fill")
                }
                projectWorksetList
                if let actionError = viewModel.projectActionError {
                    compactStatus(actionError, color: .red, symbol: "exclamationmark.circle.fill")
                }
            }

            if !viewModel.workspaceLibraryItems.isEmpty {
                Divider()
                workspaceSectionHeader
                if !viewModel.homeWorkspaceItems.isEmpty {
                    workspaceWorksetList
                }
            }
        }
    }

    private var homeHeader: some View {
        HStack(spacing: 8) {
            Text("Codex 用量")
                .font(.headline)
            if viewModel.isRefreshing || viewModel.status == .loading {
                ProgressView()
                    .controlSize(.mini)
                    .scaleEffect(0.72)
                    .frame(width: 11, height: 11)
                    .accessibilityLabel("正在读取 Codex 用量")
            }
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

    private var workspaceSectionHeader: some View {
        HStack(spacing: 6) {
            Text("Codex 工作区")
                .font(.subheadline.weight(.semibold))
            Text("\(viewModel.workspaceLibraryItems.count)")
                .font(.caption2)
                .monospacedDigit()
                .foregroundStyle(.tertiary)
            Spacer()
            headerIconButton(systemName: "square.grid.2x2", label: "Codex 工作区列表") {
                page = .workspaceLibrary
            }
        }
    }

    private var workspaceWorksetList: some View {
        let items = viewModel.homeWorkspaceItems
        let visibleItems = showAllWorkspaces ? items : Array(items.prefix(4))
        return VStack(spacing: 5) {
            ForEach(visibleItems) { item in
                workspaceContainer(item, showsHistoryNotice: false)
            }
            if items.count > 4 {
                if showAllWorkspaces {
                    projectListDisclosure(title: "收起", symbol: "chevron.up") {
                        showAllWorkspaces = false
                    }
                } else {
                    projectListDisclosure(title: "更多 \(items.count - 4) 个工作区", symbol: "chevron.down") {
                        showAllWorkspaces = true
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var projectWorksetList: some View {
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
            VStack(spacing: 5) {
                ForEach(visibleItems) { item in
                    projectContainer(item)
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
    private func projectContainer(_ item: ProjectDashboardItem) -> some View {
        if item.isUnassigned {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 7) {
                    Image(systemName: "folder.badge.questionmark")
                        .font(.system(size: 11, weight: .medium))
                        .foregroundStyle(.secondary)
                    Text("无项目")
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    projectTaskStatus(item)
                }

                VStack(spacing: 0) {
                    ForEach(Array(item.tasks.enumerated()), id: \.element.id) { index, task in
                        projectTaskRow(task)
                        if index < item.tasks.count - 1 {
                            Divider().opacity(0.45).padding(.leading, 16)
                        }
                    }
                }
                .padding(.vertical, 0)

                if let failure = viewModel.taskOpenFailure, failure.projectID == item.id {
                    Label(failure.message, systemImage: "exclamationmark.circle.fill")
                        .font(.caption2)
                        .foregroundStyle(.red)
                        .lineLimit(1)
                }
            }
            .padding(8)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            .accessibilityElement(children: .contain)
            .accessibilityLabel(projectAccessibilityLabel(item))
        } else if let project = item.project {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 7) {
                    Text(project.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    projectTaskStatus(item)
                    projectRowIconButton(
                        systemName: item.isPinned ? "pin.fill" : "pin",
                        label: item.isPinned ? "取消固定" : "固定项目",
                        tint: .secondary
                    ) {
                        viewModel.togglePinned(item.id)
                    }
                }

                if !item.tasks.isEmpty {
                    VStack(spacing: 0) {
                        ForEach(Array(item.tasks.enumerated()), id: \.element.id) { index, task in
                            projectTaskRow(task, showsKSFRoute: true)
                            if index < item.tasks.count - 1 {
                                Divider().opacity(0.45).padding(.leading, 16)
                            }
                        }
                    }
                    .padding(.vertical, 0)
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

                projectActionFooter(project: project, item: item)
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

    private func workspaceContainer(_ item: CodexWorkspaceItem, showsHistoryNotice: Bool) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 7) {
                Image(systemName: item.kind == "other" ? "tray.full" : "folder")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(.secondary)
                Text(item.name)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Spacer(minLength: 4)
                workspaceTaskStatus(item)
                projectRowIconButton(
                    systemName: item.isPinned ? "pin.fill" : "pin",
                    label: item.isPinned ? "取消固定" : "固定工作区",
                    tint: .secondary
                ) {
                    viewModel.toggleWorkspacePinned(item.id)
                }
            }
            if !item.path.isEmpty {
                Text(item.path)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            VStack(spacing: 0) {
                ForEach(Array(item.tasks.enumerated()), id: \.element.id) { index, task in
                    projectTaskRow(task, showsKSFRoute: false)
                    if index < item.tasks.count - 1 {
                        Divider().opacity(0.45).padding(.leading, 16)
                    }
                }
            }
            .padding(.vertical, 0)

            if showsHistoryNotice, item.hiddenTaskCount > 0 {
                Text("另有 \(item.hiddenTaskCount) 个较早任务未显示")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }

            if let failure = viewModel.taskOpenFailure, failure.projectID == item.id {
                Label(failure.message, systemImage: "exclamationmark.circle.fill")
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(1)
            }

            if let message = viewModel.workspaceTaskCreationErrors[item.id] {
                Label(message, systemImage: "exclamationmark.circle.fill")
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }

            if item.kind == "workspace", !item.path.isEmpty {
                workspaceActionFooter(item)
            }
        }
        .padding(8)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("\(item.name)，\(item.runningCount) 个任务运行中，\(item.waitingCount) 个任务等待处理")
    }

    private func projectTaskRow(_ task: ProjectTaskItem, showsKSFRoute: Bool = true) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .top, spacing: 5) {
                Button { viewModel.openTask(task) } label: {
                    VStack(alignment: .leading, spacing: 3) {
                        HStack(spacing: 5) {
                            Image(systemName: taskStatusSymbol(task))
                                .font(.system(size: 9, weight: .semibold))
                                .foregroundStyle(taskStatusColor(task))
                                .frame(width: 11)
                            Text(taskDisplayName(task))
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.primary)
                                .lineLimit(1)
                            Spacer(minLength: 5)
                            semanticBadge(
                                taskStatusText(task),
                                color: taskStatusColor(task)
                            )
                        }
                        .frame(height: 20, alignment: .center)
                        if showsKSFRoute {
                            if let route = task.route, task.taskRuntime == nil || task.taskRuntime?.routeFreshness == "current" {
                                taskRouteSummaryLine(route)
                                taskAbilitySummaryLine(route)
                            } else {
                                Label("未绑定 KSF 路由", systemImage: "link.badge.plus")
                                    .font(.caption2)
                                    .foregroundStyle(.tertiary)
                                    .lineLimit(1)
                                    .padding(.leading, 16)
                            }
                            Text(task.taskRuntime?.reportLabel ?? "暂无可用的 Agent 上报")
                                .font(.caption2).foregroundStyle(.secondary)
                                .lineLimit(1).padding(.leading, 16)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)

                projectRowIconButton(systemName: "info.circle", label: "查看任务详情", tint: .secondary) {
                    selectedTaskID = task.id
                    page = .taskDetail
                }
                if viewModel.feishuTaskLinkActions.contains(task.id) {
                    ProgressView().controlSize(.mini).frame(width: 20, height: 20)
                } else {
                    let link = viewModel.feishuTaskLink(for: task)
                    projectRowIconButton(
                        systemName: link == nil ? "paperplane" : feishuTaskLinkSymbol(link!.presentationState),
                        label: link == nil ? "连接此任务到飞书" : "解除飞书连接，当前状态：\(feishuTaskLinkText(link!.presentationState))",
                        tint: link.map { feishuTaskLinkColor($0.presentationState) } ?? .secondary
                    ) {
                        viewModel.toggleFeishuTaskLink(task)
                    }
                    .help(viewModel.feishuTaskLinkUnavailableReason ?? "连接或解除此任务的飞书卡片")
                }
            }
            if let error = viewModel.feishuTaskLinkError(for: task) {
                Label(error, systemImage: "exclamationmark.circle.fill")
                    .font(.caption2)
                    .foregroundStyle(.orange)
                    .lineLimit(2)
                    .padding(.leading, 16)
                    .accessibilityLabel("飞书连接失败：\(error)")
            }
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .contain)
        .accessibilityLabel(taskAccessibilityLabel(task))
    }

    private func taskRouteSummaryLine(_ route: KSFRouteSummary) -> some View {
        HStack(spacing: 5) {
            Label(route.category?.name ?? "未分类", systemImage: "square.grid.2x2")
            Text("·")
                .foregroundStyle(.tertiary)
            Label(route.mainJob?.name ?? "无主岗位", systemImage: "person.crop.circle")
        }
        .font(.caption2.weight(.medium))
        .foregroundStyle(.secondary)
        .lineLimit(1)
        .padding(.leading, 16)
    }

    private func taskAbilitySummaryLine(_ route: KSFRouteSummary) -> some View {
        HStack(spacing: 4) {
            Label(taskAbilityText(route), systemImage: "hammer")
                .lineLimit(1)
            Spacer(minLength: 4)
            Label("\(route.dispatchableSkills.count) Skill", systemImage: "sparkles")
                .lineLimit(1)
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
        .padding(.leading, 16)
    }

    private func feishuTaskLinkText(_ state: String) -> String {
        switch state {
        case "waiting_current_turn": return "等待当前轮"
        case "running": return "执行中"
        case "waiting_input": return "等待输入"
        case "desktop_action_required": return "需要桌面操作"
        case "queued": return "消息排队"
        case "completed": return "本轮完成"
        case "interrupted": return "本轮已停止"
        case "failed": return "执行失败"
        default: return "已连接"
        }
    }

    private func feishuTaskLinkSymbol(_ state: String) -> String {
        switch state {
        case "waiting_current_turn", "queued": return "clock.badge"
        case "running": return "paperplane.fill"
        case "waiting_input": return "questionmark.bubble"
        case "desktop_action_required": return "desktopcomputer"
        case "interrupted": return "stop.circle"
        case "failed": return "exclamationmark.triangle"
        default: return "paperplane.circle.fill"
        }
    }

    private func taskRouteDetails(_ route: KSFRouteSummary) -> some View {
        let presentation = KSFRoutePresentation(route: route)
        return VStack(alignment: .leading, spacing: 9) {
            routeDetailLine(
                label: "工作类别",
                value: presentation.category?.name ?? "未分类",
                status: presentation.category?.validationStatus,
                symbol: "square.grid.2x2",
                color: .indigo
            )
            ForEach(Array(presentation.jobGroups.enumerated()), id: \.offset) { _, group in
                VStack(alignment: .leading, spacing: 6) {
                    routeDetailLine(
                        label: group.job?.role == "main" ? "主岗位" : group.job == nil ? "其他基本功" : "协同岗位",
                        value: group.job?.name ?? "未关联岗位",
                        status: group.job?.validationStatus,
                        symbol: group.job?.role == "main" ? "person.crop.circle.fill" : "person.2.fill",
                        color: .purple
                    )
                    ForEach(Array(group.abilities.enumerated()), id: \.offset) { _, abilityGroup in
                        VStack(alignment: .leading, spacing: 2) {
                            routeDetailLine(
                                label: "基本功",
                                value: abilityGroup.ability.name ?? "未命名基本功",
                                status: abilityGroup.ability.validationStatus,
                                symbol: "hammer.fill",
                                color: .teal
                            )
                            ForEach(Array(abilityGroup.skills.enumerated()), id: \.offset) { _, skill in
                                skillDetailLine(skill)
                            }
                        }
                        .padding(.leading, 12)
                    }
                }
                .padding(.vertical, 2)
            }
            if !presentation.unassignedSkills.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Label("其他可调度 Skill", systemImage: "sparkles")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.secondary)
                    ForEach(Array(presentation.unassignedSkills.enumerated()), id: \.offset) { _, skill in
                        skillDetailLine(skill)
                    }
                }
            }
        }
    }

    private func routeDetailLine(
        label: String,
        value: String,
        status: String?,
        symbol: String,
        color: Color
    ) -> some View {
        HStack(alignment: .center, spacing: 6) {
            Image(systemName: symbol)
                .font(.system(size: 10, weight: .medium))
                .foregroundStyle(color)
                .frame(width: 13)
            Text(label)
                .foregroundStyle(.secondary)
                .frame(width: 48, alignment: .leading)
            Text(value)
                .fontWeight(.medium)
                .foregroundStyle(.primary)
                .lineLimit(1)
            Spacer(minLength: 4)
            if let status, !status.isEmpty {
                Text(status)
                    .foregroundStyle(validationStatusColor(status))
            }
        }
        .font(.caption2)
    }

    private func skillDetailLine(_ skill: KSFDispatchableSkill) -> some View {
        HStack(spacing: 5) {
            Image(systemName: "sparkles")
                .font(.system(size: 9, weight: .medium))
                .foregroundStyle(Color.blue)
                .frame(width: 12)
            Text(skill.skillID ?? "未命名 Skill")
                .foregroundStyle(.primary)
                .lineLimit(1)
            Spacer(minLength: 4)
            if let stage = skill.skillStage, !stage.isEmpty {
                Text(stage)
                    .foregroundStyle(skillStageColor(stage))
            }
        }
        .font(.caption2)
        .padding(.leading, 12)
    }

    private var taskDetailPage: some View {
        VStack(alignment: .leading, spacing: 10) {
            secondaryHeader(title: "任务详情", backLabel: "返回工作台") {
                page = .home
                selectedTaskID = nil
            }

            if let context = selectedTaskContext {
                VStack(alignment: .leading, spacing: 7) {
                    HStack(alignment: .firstTextBaseline, spacing: 7) {
                        Circle()
                            .fill(taskStatusColor(context.task))
                            .frame(width: 6, height: 6)
                        Text(taskDisplayName(context.task))
                            .font(.subheadline.weight(.semibold))
                            .lineLimit(2)
                        Spacer(minLength: 4)
                        Text(taskStatusText(context.task))
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(taskStatusColor(context.task))
                    }
                    Label(context.containerName, systemImage: "folder")
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    taskRuntimeDetails(context.task)
                }
                .padding(10)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))

                if context.showsKSFRoute {
                    VStack(alignment: .leading, spacing: 9) {
                        HStack(spacing: 5) {
                            Image(systemName: "point.3.connected.trianglepath.dotted")
                                .foregroundStyle(Color.indigo)
                            Text("KSF 路由")
                                .foregroundStyle(.primary)
                        }
                        .font(.caption.weight(.semibold))
                        Text(context.task.taskRuntime?.routeLabel ?? (context.task.route == nil ? "尚无 KSF 已验证来源" : "KSF 投影 · 时效未知"))
                            .font(.caption2).foregroundStyle(.secondary)
                        if let route = context.task.route, context.task.taskRuntime == nil || context.task.taskRuntime?.routeFreshness == "current" {
                            taskRouteDetails(route)
                        } else {
                            Text("该任务尚未绑定 KSF 路由。")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                                .frame(maxWidth: .infinity, minHeight: 34, alignment: .leading)
                        }
                    }
                    .padding(10)
                    .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
                }

                taskDetailActions(context.task)

                if let failure = viewModel.taskOpenFailure,
                   failure.projectID == context.task.projectID {
                    compactStatus(failure.message, color: .red, symbol: "exclamationmark.circle.fill")
                }
                if let feedback = viewModel.feishuFeedback {
                    compactStatus(
                        feedback,
                        color: feedback.contains("成功") || feedback.contains("已连接") ? .green : .orange,
                        symbol: feedback.contains("成功") || feedback.contains("已连接")
                            ? "checkmark.circle.fill" : "info.circle.fill"
                    )
                }
            } else {
                emptyState("任务详情暂不可用", detail: "任务可能已被归档、移除，或当前项目数据尚未完成同步。")
            }
        }
    }

    @ViewBuilder
    private func taskRuntimeDetails(_ task: ProjectTaskItem) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(task.taskRuntime?.reportLabel ?? "暂无可用的 Agent 上报").font(.caption2).foregroundStyle(.secondary)
            if task.taskRuntime == nil {
                Text("尚未上报或暂无法读取；实际状态以 Desktop 观测为准。")
                    .font(.caption2).foregroundStyle(.tertiary)
            }
            if let runtime = task.taskRuntime {
                Text("Agent 自述：\(runtime.reportedStatusLabel)（不替代实际任务状态）")
                    .font(.caption2).foregroundStyle(.secondary)
                if let reportedAt = runtime.reportedAt {
                    Text("上报于 \(reportedAt.formatted(date: .abbreviated, time: .shortened))")
                        .font(.caption2).foregroundStyle(.tertiary)
                }
                if let progress = runtime.progress {
                    if let summary = progress.summary, !summary.isEmpty {
                        Text("Agent 进度自述：\(summary)").font(.caption2).foregroundStyle(.secondary).lineLimit(3)
                    }
                    if let percent = progress.percent, (0...100).contains(percent) {
                        Text("Agent 自报进度 \(percent)%").font(.caption2).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    private func taskDetailActions(_ task: ProjectTaskItem) -> some View {
        let link = viewModel.feishuTaskLink(for: task)
        let isUpdatingLink = viewModel.feishuTaskLinkActions.contains(task.id)
        return VStack(alignment: .leading, spacing: 6) {
            Divider()

            HStack(spacing: 8) {
                Button { viewModel.openTask(task) } label: {
                    Label("打开 Codex", systemImage: "arrow.up.forward.app")
                        .frame(maxWidth: .infinity)
                }
                .controlSize(.small)
                .buttonStyle(.borderedProminent)

                Button { viewModel.toggleFeishuTaskLink(task) } label: {
                    HStack(spacing: 5) {
                        if isUpdatingLink {
                            ProgressView()
                                .controlSize(.mini)
                        } else {
                            Image(systemName: link == nil ? "paperplane" : "paperplane.slash")
                        }
                        Text(isUpdatingLink ? "正在更新" : link == nil ? "连接飞书" : "解除飞书")
                    }
                    .frame(maxWidth: .infinity)
                }
                .controlSize(.small)
                .buttonStyle(.bordered)
                .disabled(isUpdatingLink)
                .help(viewModel.feishuTaskLinkUnavailableReason ?? "连接或解除此任务的飞书卡片")
            }

            if let link {
                VStack(alignment: .leading, spacing: 3) {
                    Label(
                        "\(link.targetAlias) · 全权限 · 24 小时",
                        systemImage: feishuTaskLinkSymbol(link.presentationState)
                    )
                    .foregroundStyle(feishuTaskLinkColor(link.presentationState))
                    HStack(spacing: 4) {
                        Text(feishuTaskLinkText(link.presentationState))
                        Text("·")
                        Text("当前控制：\(feishuTurnOwnerText(link.turnOwner))")
                    }
                    Text("剩余 \(feishuRemainingTime(link.remainingSeconds)) · 最后同步 \(feishuTimestamp(link.updatedAt))")
                        .foregroundStyle(.tertiary)
                    if !link.phase.isEmpty {
                        Text("\(link.phase)\(link.detailSummary.isEmpty ? "" : " · \(link.detailSummary)")")
                            .lineLimit(2)
                            .foregroundStyle(.secondary)
                    }
                }
                .font(.caption2)
            }
        }
        .padding(.top, 1)
    }

    private var selectedTaskContext: TaskDetailContext? {
        guard let selectedTaskID else { return nil }
        for item in viewModel.projectDashboard.projects {
            if let task = item.tasks.first(where: { $0.id == selectedTaskID }) {
                return TaskDetailContext(task: task, containerName: item.isUnassigned ? "无项目" : item.project?.name ?? "KSF 项目", showsKSFRoute: true)
            }
        }
        for item in viewModel.workspaceDashboard.workspaces {
            if let task = item.tasks.first(where: { $0.id == selectedTaskID }) {
                return TaskDetailContext(task: task, containerName: item.name, showsKSFRoute: false)
            }
        }
        return nil
    }

    private struct TaskDetailContext {
        let task: ProjectTaskItem
        let containerName: String
        let showsKSFRoute: Bool
    }

    private func projectInlineMetric(_ label: String, value: String) -> some View {
        HStack(spacing: 2) {
            Text(label)
                .foregroundStyle(.secondary)
            Text(value)
                .fontWeight(.semibold)
                .monospacedDigit()
                .foregroundStyle(.primary)
        }
        .font(.caption2)
        .lineLimit(1)
        .accessibilityLabel("项目\(label) Token，\(value)")
    }

    private func projectActionFooter(project: KSFProject, item: ProjectDashboardItem) -> some View {
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
            projectRowIconButton(systemName: "doc.text", label: "打开项目记忆") {
                viewModel.openProjectCard(project)
            }
        }
        .padding(.top, 6)
        .overlay(alignment: .top) { Divider().opacity(0.55) }
    }

    private func workspaceActionFooter(_ item: CodexWorkspaceItem) -> some View {
        HStack(spacing: 7) {
            projectInlineMetric("累计", value: projectTokenValue(item.usage, keyPath: \.cumulativeTokens))
            projectInlineMetric("今日", value: projectTokenValue(item.usage, keyPath: \.todayTokens))
            Spacer(minLength: 2)
            if viewModel.creatingWorkspaceTaskIDs.contains(item.id) {
                ProgressView()
                    .controlSize(.small)
                    .scaleEffect(0.7)
                    .frame(width: 20, height: 20)
                    .help("正在新建 Codex 任务")
                    .accessibilityLabel("正在新建 Codex 任务")
            } else {
                projectRowIconButton(systemName: "plus.bubble", label: "在工作区中新建 Codex 任务") {
                    viewModel.createTask(for: item)
                }
            }
            projectRowIconButton(systemName: "folder", label: "打开工作区文件夹") {
                viewModel.openWorkspaceDirectory(item)
            }
        }
        .padding(.top, 6)
        .overlay(alignment: .top) { Divider().opacity(0.55) }
    }

    private func projectRowIconButton(
        systemName: String,
        label: String,
        disabled: Bool = false,
        tint: Color = .secondary,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(tint)
                .frame(width: 20, height: 20)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(disabled)
        .opacity(disabled ? 0.45 : 1)
        .help(label)
        .accessibilityLabel(label)
    }

    @ViewBuilder
    private func projectNewTaskIconControl(project: KSFProject) -> some View {
        let isCreating = viewModel.creatingProjectTaskIDs.contains(project.id)
        let isArchiving = viewModel.archivingProjectTaskIDs.contains(project.id)
        if isCreating && !isArchiving {
            ProgressView()
                .controlSize(.small)
                .scaleEffect(0.7)
                .frame(width: 20, height: 20)
                .help("正在新建 Codex 任务")
                .accessibilityLabel("正在新建 Codex 任务")
        } else {
            projectRowIconButton(
                systemName: "plus.bubble",
                label: "在 KSF 项目中新建 Codex 任务",
                disabled: isCreating
            ) {
                viewModel.createTask(for: project)
            }
        }
    }

    @ViewBuilder
    private func projectArchiveTaskIconControl(project: KSFProject) -> some View {
        if viewModel.archivingProjectTaskIDs.contains(project.id) {
            ProgressView()
                .controlSize(.small)
                .scaleEffect(0.7)
                .frame(width: 22, height: 22)
                .help("正在发起项目归档任务")
                .accessibilityLabel("正在发起项目归档任务")
        } else {
            headerIconButton(
                systemName: "archivebox",
                label: "发起项目归档任务",
                disabled: viewModel.creatingProjectTaskIDs.contains(project.id)
            ) {
                viewModel.createArchiveTask(for: project)
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
        if let action = item.launchAction {
            projectRowIconButton(systemName: action.symbol, label: action.title) {
                viewModel.launchAction(action)
            }
        } else {
            projectRowIconButton(
                systemName: "play.slash",
                label: "项目目录中没有可执行的 start.sh",
                disabled: true
            ) {}
        }
    }

    private func projectTaskStatus(_ item: ProjectDashboardItem) -> some View {
        HStack(spacing: 5) {
            switch viewModel.taskActivity.availability {
            case .loading:
                projectTaskCount("…", systemImage: "play.fill", color: .secondary)
            case .unsupportedProtocol, .offline:
                projectTaskCount("—", systemImage: "play.fill", color: .secondary)
                projectTaskCount("—", systemImage: "person.fill.questionmark", color: .secondary)
            case .available, .desktopNotRunning:
                projectTaskCount(
                    "\(item.runningCount)",
                    systemImage: "play.fill",
                    color: item.runningCount > 0 ? .blue : .secondary
                )
                if item.waitingCount > 0 {
                    projectTaskCount("\(item.waitingCount)", systemImage: "person.fill.questionmark", color: .orange)
                }
            }
        }
        .monospacedDigit()
    }

    private func workspaceTaskStatus(_ item: CodexWorkspaceItem) -> some View {
        HStack(spacing: 5) {
            projectTaskCount(
                "\(item.runningCount)",
                systemImage: "play.fill",
                color: item.runningCount > 0 ? .blue : .secondary
            )
            if item.waitingCount > 0 {
                projectTaskCount("\(item.waitingCount)", systemImage: "person.fill.questionmark", color: .orange)
            }
        }
        .monospacedDigit()
    }

    private func projectTaskCount(_ text: String, systemImage: String, color: Color) -> some View {
        Label(text, systemImage: systemImage)
            .font(.caption2.weight(.semibold))
            .foregroundStyle(color)
            .lineLimit(1)
            .accessibilityElement(children: .combine)
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
            HStack(spacing: 6) {
                Text("Token 活动")
                    .font(.caption.weight(.semibold))
                Spacer()
                headerIconButton(
                    systemName: "chart.bar.xaxis",
                    label: "查看每日 Token 历史"
                ) {
                    selectedLocalHistoryDate = nil
                    page = .tokenHistory
                    viewModel.refreshLocalTokenHistory()
                }
            }
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
                    Divider()
                    HStack(spacing: 8) {
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Text("API 估算")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                                .fixedSize(horizontal: true, vertical: false)
                            pricingPlanMenu
                        }
                        Spacer(minLength: 8)
                        Text("今日 \(formattedCost(viewModel.localTodayCost))")
                            .font(.system(.callout, design: .rounded, weight: .semibold))
                            .monospacedDigit()
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
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

    private var tokenHistoryPage: some View {
        VStack(alignment: .leading, spacing: 9) {
            secondaryHeader(title: "每日 Token", backLabel: "返回主页") { page = .home }

            HStack(spacing: 5) {
                Text("最近 30 个自然日")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Spacer(minLength: 4)
                tokenHistoryLegend(color: .purple, label: "服务器")
                tokenHistoryLegend(color: .accentColor, label: "本机")
                if viewModel.isRefreshingLocalTokenHistory {
                    ProgressView()
                        .controlSize(.mini)
                        .scaleEffect(0.68)
                        .frame(width: 10, height: 10)
                        .accessibilityLabel("正在刷新每日 Token 历史")
                }
            }

            if viewModel.tokenHistoryComparison.days.isEmpty {
                if viewModel.isRefreshingLocalTokenHistory {
                    HStack(spacing: 6) {
                        ProgressView().controlSize(.small)
                        Text("正在读取每日 Token 历史…")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                } else {
                    emptyState(
                        "没有每日 Token 历史",
                        detail: viewModel.localTokenHistoryError ?? "本机 Codex 会话中尚未发现可统计的 Token 记录。"
                    )
                }
            } else {
                localTokenHistoryDashboard(
                    TokenHistoryComparisonSeries(days: viewModel.tokenHistoryComparison.days)
                )
            }

            if let error = viewModel.localTokenHistoryError,
               !viewModel.tokenHistoryComparison.days.isEmpty {
                compactStatus(error, color: .orange, symbol: "exclamationmark.triangle.fill")
            }
            if let error = viewModel.tokenHistoryComparison.serverError,
               !viewModel.tokenHistoryComparison.days.isEmpty {
                compactStatus(error, color: .orange, symbol: "exclamationmark.triangle.fill")
            }
        }
    }

    private func tokenHistoryLegend(color: Color, label: String) -> some View {
        HStack(spacing: 3) {
            Circle()
                .fill(color)
                .frame(width: 5, height: 5)
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }

    private func localTokenHistoryDashboard(_ series: TokenHistoryComparisonSeries) -> some View {
        let selectedUsage = selectedLocalHistoryUsage(in: series)
        return VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 0) {
                localTokenHistoryMetric("本机 30 日", value: formatTokens(series.localTotalTokens))
                Divider().frame(height: 28)
                localTokenHistoryMetric("本机日均", value: formatTokens(series.localAverageTokens))
                Divider().frame(height: 28)
                localTokenHistoryMetric("本机活跃", value: "\(series.localActiveDayCount) 天")
            }

            HStack(spacing: 6) {
                Text("30 日 API 估算")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .fixedSize()
                pricingPlanMenu
                Spacer(minLength: 4)
                if let count = viewModel.tokenHistoryComparison.localCostSummary?.incompleteDayCount,
                   count > 0 {
                    Text("缺 \(count) 天")
                        .font(.caption2)
                        .foregroundStyle(.orange)
                }
                Text(formattedCost(viewModel.tokenHistoryComparison.localCostSummary))
                    .font(.system(.callout, design: .rounded, weight: .semibold))
                    .monospacedDigit()
                    .fixedSize()
            }
            .padding(.horizontal, 7)

            Divider()

            HStack(alignment: .lastTextBaseline, spacing: 8) {
                Text(selectedUsage.map { localTokenHistoryDateLabel($0.startDate) } ?? "未选择日期")
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Spacer(minLength: 8)
                Text(selectedUsage.map { "本机 \(formatTokens($0.localTokens)) · \(formattedCost($0.localCost))" } ?? "—")
                    .font(.system(.title3, design: .rounded, weight: .semibold))
                    .monospacedDigit()
                    .lineLimit(1)
            }

            localTokenHistoryChart(series)

            HStack {
                Text(series.days.first.map { localTokenHistoryShortDateLabel($0.startDate) } ?? "")
                Spacer()
                if series.days.count > 2 {
                    Text(localTokenHistoryShortDateLabel(series.days[series.days.count / 2].startDate))
                    Spacer()
                }
                Text(series.latestDay.map { localTokenHistoryAxisEndLabel($0.startDate) } ?? "")
            }
            .font(.caption2)
            .foregroundStyle(.tertiary)

            Divider()

            HStack(spacing: 0) {
                localTokenHistoryMetric(
                    "服务器当日",
                    value: selectedUsage?.serverTokens.map { formatTokens($0) } ?? "未同步",
                    color: .purple
                )
                Divider().frame(height: 28)
                localTokenHistoryMetric(
                    "本机当日",
                    value: selectedUsage.map { formatTokens($0.localTokens) } ?? "—",
                    color: .accentColor
                )
                Divider().frame(height: 28)
                localTokenHistoryMetric(
                    "本机占比",
                    value: selectedUsage.map { formatTokenShare(series.localShare(on: $0.startDate)) } ?? "—"
                )
            }

            Divider()

            if let breakdown = selectedUsage?.localBreakdown {
                HStack(spacing: 0) {
                    localTokenHistoryCostMetric(
                        "普通输入",
                        tokens: breakdown.regularInputTokens,
                        microUSD: selectedUsage?.localCost?.regularInputMicroUsd
                    )
                    Divider().frame(height: 28)
                    localTokenHistoryCostMetric(
                        "缓存输入",
                        tokens: breakdown.cachedInputTokens,
                        microUSD: selectedUsage?.localCost?.cachedInputMicroUsd
                    )
                    Divider().frame(height: 28)
                    localTokenHistoryCostMetric(
                        "输出",
                        tokens: breakdown.outputTokens,
                        microUSD: selectedUsage?.localCost?.outputMicroUsd
                    )
                }
            } else {
                Text("该日 Token 构成不可用")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: 28, alignment: .leading)
            }

            Text("按当前所选 API 价格估算，历史金额会随方案或价格变化，不代表实际账单。")
                .font(.caption2)
                .foregroundStyle(.tertiary)
        }
        .padding(10)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    private func localTokenHistoryMetric(_ label: String, value: String, color: Color? = nil) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 3) {
                if let color {
                    Circle()
                        .fill(color)
                        .frame(width: 5, height: 5)
                }
                Text(label)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Text(value)
                .font(.system(.callout, design: .rounded, weight: .semibold))
                .monospacedDigit()
                .lineLimit(1)
        }
        .padding(.horizontal, 7)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func localTokenHistoryCostMetric(_ label: String, tokens: Int64, microUSD: Int64?) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text(formatTokens(tokens))
                .font(.system(.callout, design: .rounded, weight: .semibold))
                .monospacedDigit()
            Text(microUSD.map(TokenCostFormatter.usd) ?? "—")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 7)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func localTokenHistoryChart(_ series: TokenHistoryComparisonSeries) -> some View {
        GeometryReader { geometry in
            let maximum = max(1, series.maximumTokens)
            let plotHeight = max(1, geometry.size.height - 7)
            let averageFraction = CGFloat(Double(series.localAverageTokens) / Double(maximum))
            let averageY = geometry.size.height - max(2, plotHeight * averageFraction)

            ZStack(alignment: .bottom) {
                Path { path in
                    path.move(to: CGPoint(x: 0, y: averageY))
                    path.addLine(to: CGPoint(x: geometry.size.width, y: averageY))
                }
                .stroke(
                    Color.secondary.opacity(0.24),
                    style: StrokeStyle(lineWidth: 1, dash: [2, 3])
                )

                HStack(alignment: .bottom, spacing: 3) {
                    ForEach(series.days) { usage in
                        let localFraction = CGFloat(Double(max(0, usage.localTokens)) / Double(maximum))
                        let serverHeight = usage.serverTokens.map {
                            max(2, plotHeight * CGFloat(Double(max(0, $0)) / Double(maximum)))
                        } ?? 0
                        let isSelected = selectedLocalHistoryUsage(in: series)?.startDate == usage.startDate
                        VStack(spacing: 3) {
                            Circle()
                                .fill(Color.accentColor)
                                .frame(width: 4, height: 4)
                                .opacity(isSelected ? 1 : 0)
                            ZStack(alignment: .bottom) {
                                RoundedRectangle(cornerRadius: 2)
                                    .fill(Color.purple.opacity(isSelected ? 0.76 : 0.26))
                                    .frame(maxWidth: .infinity)
                                    .frame(height: serverHeight)
                                RoundedRectangle(cornerRadius: 1.5)
                                    .fill(isSelected ? Color.accentColor : Color.accentColor.opacity(0.58))
                                    .frame(width: 3, height: max(2, plotHeight * localFraction))
                            }
                        }
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottom)
                    }
                }

                Rectangle()
                    .fill(Color.clear)
                    .contentShape(Rectangle())
                    .gesture(
                        DragGesture(minimumDistance: 0)
                            .onChanged { value in
                                selectLocalHistoryDay(
                                    at: value.location.x,
                                    width: geometry.size.width,
                                    days: series.days
                                )
                            }
                    )
            }
        }
        .frame(height: 108)
        .animation(.easeOut(duration: 0.16), value: selectedLocalHistoryDate)
        .help("点击或拖动查看每天的服务器与本机 Token 用量")
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("最近 30 日服务器与本机 Token 趋势")
        .accessibilityValue(localTokenHistoryChartAccessibilityValue(series))
        .accessibilityAdjustableAction { direction in
            adjustLocalHistorySelection(direction, in: series)
        }
    }

    private func selectedLocalHistoryUsage(in series: TokenHistoryComparisonSeries) -> TokenHistoryComparisonDay? {
        if let selectedLocalHistoryDate,
           let selected = series.usage(on: selectedLocalHistoryDate) {
            return selected
        }
        return series.latestDay
    }

    private func selectLocalHistoryDay(
        at xPosition: CGFloat,
        width: CGFloat,
        days: [TokenHistoryComparisonDay]
    ) {
        guard width > 0, !days.isEmpty else { return }
        let boundedX = min(max(0, xPosition), max(0, width - 0.001))
        let index = min(Int((boundedX / width) * CGFloat(days.count)), days.count - 1)
        selectedLocalHistoryDate = days[index].startDate
    }

    private func adjustLocalHistorySelection(
        _ direction: AccessibilityAdjustmentDirection,
        in series: TokenHistoryComparisonSeries
    ) {
        guard !series.days.isEmpty else { return }
        let currentDate = selectedLocalHistoryUsage(in: series)?.startDate
        let currentIndex = series.days.firstIndex { $0.startDate == currentDate }
            ?? (series.days.count - 1)
        let nextIndex: Int
        switch direction {
        case .increment:
            nextIndex = min(currentIndex + 1, series.days.count - 1)
        case .decrement:
            nextIndex = max(currentIndex - 1, 0)
        @unknown default:
            return
        }
        selectedLocalHistoryDate = series.days[nextIndex].startDate
    }

    private func localTokenHistoryChartAccessibilityValue(_ series: TokenHistoryComparisonSeries) -> String {
        guard let usage = selectedLocalHistoryUsage(in: series) else { return "没有数据" }
        let server = usage.serverTokens.map { formatTokens($0) } ?? "未同步"
        let share = formatTokenShare(series.localShare(on: usage.startDate))
        return "\(localTokenHistoryDateLabel(usage.startDate))，服务器 \(server)，本机 \(formatTokens(usage.localTokens))，本机占比 \(share)"
    }

    private func formatTokenShare(_ value: Double?) -> String {
        guard let value, value.isFinite else { return "—" }
        return String(format: "%.1f%%", value * 100)
    }

    private var projectLibraryPage: some View {
        VStack(alignment: .leading, spacing: 9) {
            secondaryHeader(title: "项目列表", backLabel: "返回主页") { page = .home }
            if viewModel.projectLibraryItems.isEmpty {
                emptyState("没有可用项目", detail: viewModel.projectDashboard.message ?? "KSF 尚未导出项目目录。")
            } else {
                VStack(spacing: 0) {
                    ForEach(viewModel.projectLibraryItems) { item in
                        projectLibraryRow(item)
                        if item.id != viewModel.projectLibraryItems.last?.id { Divider() }
                    }
                }
                .frame(maxWidth: .infinity)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            }

            if let actionError = viewModel.projectActionError {
                compactStatus(actionError, color: .red, symbol: "exclamationmark.circle.fill")
            }
        }
    }

    private var workspaceLibraryPage: some View {
        VStack(alignment: .leading, spacing: 9) {
            secondaryHeader(title: "Codex 工作区", backLabel: "返回主页") { page = .home }
            if viewModel.workspaceLibraryItems.isEmpty {
                emptyState("没有可用工作区", detail: "当前没有可展示的普通 Codex 工作区。")
            } else {
                VStack(spacing: 6) {
                    ForEach(viewModel.workspaceLibraryItems) { item in
                        workspaceContainer(item, showsHistoryNotice: true)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func projectLibraryRow(_ item: ProjectDashboardItem) -> some View {
        if let project = item.project {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 7) {
                    Image(systemName: "folder")
                        .foregroundStyle(.secondary)
                        .frame(width: 14)
                    Text(project.name)
                        .font(.caption.weight(.medium))
                        .lineLimit(1)
                    Spacer()
                    projectArchiveTaskIconControl(project: project)
                    headerIconButton(
                        systemName: item.isPinned ? "pin.fill" : "pin",
                        label: item.isPinned ? "取消固定" : "固定项目"
                    ) {
                        viewModel.togglePinned(item.id)
                    }
                }

                projectActionFooter(project: project, item: item)

                if let message = viewModel.projectTaskCreationErrors[item.id] {
                    Label(message, systemImage: "exclamationmark.circle.fill")
                        .font(.caption2)
                        .foregroundStyle(.red)
                        .lineLimit(2)
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
        } else {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 7) {
                    Image(systemName: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                        .frame(width: 14)
                    Text(unavailableProjectName(item.id))
                        .font(.caption.weight(.medium))
                        .lineLimit(1)
                    Spacer()
                    headerIconButton(systemName: "pin.slash", label: "取消固定") {
                        viewModel.togglePinned(item.id)
                    }
                }
                Text("项目不可用")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .padding(.leading, 21)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 6)
        }
    }

    private var settingsPage: some View {
        VStack(alignment: .leading, spacing: 10) {
            secondaryHeader(title: "设置", backLabel: "返回工作台") { page = .home }

            VStack(spacing: 0) {
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 1) {
                        Text("KSF 知识库（可选）")
                            .font(.caption)
                        Text(viewModel.isOnboardingComplete ? ksfDirectoryStatus : "KSF 是可选增强能力；不影响额度、Token、任务状态和飞书。")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Spacer()
                    if viewModel.isOnboardingComplete {
                        Button("取消", role: .destructive) { viewModel.cancelKSFRoot() }
                            .controlSize(.small)
                            .disabled(viewModel.onboardingInProgress)
                    } else {
                        Button("选择目录") { viewModel.chooseKSFRoot() }
                            .controlSize(.small)
                            .disabled(viewModel.onboardingInProgress)
                    }
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
                    page = .pricing
                } label: {
                    HStack(spacing: 8) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("API 估算价格").font(.caption)
                            Text(pricingPlanCompactName)
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                                .lineLimit(1)
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
                Divider()
                Button {
                    page = .feishu
                } label: {
                    HStack(spacing: 8) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("飞书服务").font(.caption)
                            Text(viewModel.feishuConfiguration.summaryTitle)
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
                .accessibilityLabel("飞书服务，\(viewModel.feishuConfiguration.summaryTitle)")
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
            .accessibilityLabel("退出 KSFAssistant")
        }
    }

    private var pricingPage: some View {
        VStack(alignment: .leading, spacing: 9) {
            secondaryHeader(title: "API 估算价格", backLabel: "返回设置") { page = .settings }

            ScrollView {
                VStack(alignment: .leading, spacing: 9) {
                    Text("内置方案 · 美元 / 百万 Token")
                        .font(.caption.weight(.semibold))
                    VStack(spacing: 0) {
                        ForEach(viewModel.pricingCatalog.plans.filter(\.builtIn)) { plan in
                            pricingPlanRow(plan, editable: false)
                            if plan.id != viewModel.pricingCatalog.plans.filter(\.builtIn).last?.id {
                                Divider()
                            }
                        }
                    }
                    .padding(.horizontal, 9)
                    .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))

                    HStack {
                        Text("自定义方案")
                            .font(.caption.weight(.semibold))
                        Spacer()
                        Button("添加") { beginPricingDraft(nil) }
                            .controlSize(.small)
                            .disabled(viewModel.customPricingPlans.count >= 20)
                    }

                    if viewModel.customPricingPlans.isEmpty && !pricingEditorVisible {
                        Text("最多保存 20 个设备本地方案。")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    } else {
                        VStack(spacing: 0) {
                            ForEach(viewModel.customPricingPlans) { plan in
                                pricingPlanRow(plan, editable: true)
                                if plan.id != viewModel.customPricingPlans.last?.id { Divider() }
                            }
                        }
                        .padding(.horizontal, 9)
                        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
                    }

                    if pricingEditorVisible {
                        pricingEditor
                    }

                    if let feedback = pricingFormError ?? viewModel.pricingFeedback {
                        compactStatus(feedback, color: viewModel.feishuConfiguration.outcome == "completed" ? .green : .orange, symbol: viewModel.feishuConfiguration.outcome == "completed" ? "checkmark.circle" : "exclamationmark.triangle.fill")
                    }

                    HStack(spacing: 10) {
                        Link("OpenAI 官方价格", destination: URL(string: "https://developers.openai.com/api/docs/models/compare")!)
                        Link("DeepSeek 官方价格", destination: URL(string: "https://api-docs.deepseek.com/quick_start/pricing/")!)
                    }
                    .font(.caption2)

                    Text("按当前所选 API 价格估算，历史金额会随方案或价格变化，不代表实际账单。")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }
                .padding(.trailing, 2)
            }
            .frame(maxHeight: 610)
        }
    }

    private func pricingPlanRow(_ plan: PricingPlan, editable: Bool) -> some View {
        HStack(alignment: .center, spacing: 7) {
            VStack(alignment: .leading, spacing: 2) {
                Text(plan.displayName)
                    .font(.caption.weight(.medium))
                    .lineLimit(1)
                Text(pricingRateSummary(plan))
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 4)
            if editable {
                Button("编辑") { beginPricingDraft(plan) }
                    .controlSize(.mini)
                Button(role: .destructive) { viewModel.deleteCustomPricingPlan(plan.id) } label: {
                    Image(systemName: "trash")
                }
                .controlSize(.mini)
                .accessibilityLabel("删除 \(plan.displayName)")
            }
        }
        .padding(.vertical, 6)
    }

    private var pricingEditor: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(editingPricingPlanID == nil ? "新建价格方案" : "编辑价格方案")
                .font(.caption.weight(.semibold))
            pricingTextField("厂商", text: $pricingProvider)
            pricingTextField("模型", text: $pricingModel)
            pricingTextField("方案名称（可选）", text: $pricingVariant)
            HStack(spacing: 6) {
                pricingTextField("普通输入", text: $pricingRegularInput)
                pricingTextField("缓存输入", text: $pricingCachedInput)
                pricingTextField("输出", text: $pricingOutput)
            }
            HStack {
                Button("取消") { pricingEditorVisible = false }
                    .controlSize(.small)
                Spacer()
                Button("保存") { savePricingDraft() }
                    .controlSize(.small)
                    .buttonStyle(.borderedProminent)
            }
        }
        .padding(9)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    private func pricingTextField(_ title: String, text: Binding<String>) -> some View {
        TextField(title, text: text)
            .textFieldStyle(.roundedBorder)
            .font(.caption)
            .accessibilityLabel(title)
    }

    private func beginPricingDraft(_ plan: PricingPlan?) {
        editingPricingPlanID = plan?.id
        pricingProvider = plan?.provider ?? ""
        pricingModel = plan?.model ?? ""
        pricingVariant = plan?.variant ?? ""
        pricingRegularInput = plan.map { UsageViewModel.priceRateText($0.regularInputMicroUsdPerMillion) } ?? ""
        pricingCachedInput = plan.map { UsageViewModel.priceRateText($0.cachedInputMicroUsdPerMillion) } ?? ""
        pricingOutput = plan.map { UsageViewModel.priceRateText($0.outputMicroUsdPerMillion) } ?? ""
        pricingFormError = nil
        pricingEditorVisible = true
    }

    private func savePricingDraft() {
        guard let regular = UsageViewModel.microUSDPerMillion(from: pricingRegularInput),
              let cached = UsageViewModel.microUSDPerMillion(from: pricingCachedInput),
              let output = UsageViewModel.microUSDPerMillion(from: pricingOutput) else {
            pricingFormError = "价格需为 0–1000 美元，最多六位小数。"
            return
        }
        Task {
            let saved = await viewModel.saveCustomPricingPlan(
                id: editingPricingPlanID,
                provider: pricingProvider,
                model: pricingModel,
                variant: pricingVariant,
                regularInputMicroUSDPerMillion: regular,
                cachedInputMicroUSDPerMillion: cached,
                outputMicroUSDPerMillion: output
            )
            if saved { pricingEditorVisible = false }
        }
    }

    private func pricingRateSummary(_ plan: PricingPlan) -> String {
        "入 \(UsageViewModel.priceRateText(plan.regularInputMicroUsdPerMillion)) · 缓 \(UsageViewModel.priceRateText(plan.cachedInputMicroUsdPerMillion)) · 出 \(UsageViewModel.priceRateText(plan.outputMicroUsdPerMillion))"
    }

    private var pricingPlanMenu: some View {
        Menu {
            Picker("模型", selection: Binding(
                get: { viewModel.selectedPricingPlanID },
                set: { viewModel.selectPricingPlan($0) }
            )) {
                ForEach(viewModel.pricingCatalog.plans) { plan in
                    Text(plan.displayName).tag(plan.id)
                }
            }
            .pickerStyle(.inline)
        } label: {
            // One text label prevents AppKit from promoting a separate Image
            // to its leading menu icon and overriding its requested size.
            (Text(pricingPlanCompactName) + Text(" ") +
                Text(Image(systemName: "arrowtriangle.down.fill"))
                    .font(.system(size: 7))
                    .foregroundColor(.secondary)
                    .baselineOffset(1))
            .font(.caption.weight(.medium))
            .lineLimit(1)
            .padding(.vertical, 2)
            .contentShape(Rectangle())
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize(horizontal: false, vertical: true)
        .accessibilityLabel("API 估算模型")
        .accessibilityValue(pricingPlanCompactName)
        .help(viewModel.selectedPricingPlan?.displayName ?? pricingPlanCompactName)
    }

    private var pricingPlanCompactName: String {
        guard let plan = viewModel.selectedPricingPlan else { return "GPT-5.6 Sol" }
        return [plan.model, plan.variant].compactMap { value in
            guard let value, !value.isEmpty else { return nil }
            return value
        }.joined(separator: " · ")
    }

    private func formattedCost(_ estimate: TokenCostEstimate?) -> String {
        guard let estimate, estimate.status != .unavailable else { return "—" }
        return TokenCostFormatter.usd(microUSD: estimate.totalMicroUsd)
    }

    #if FEISHU_LAYOUT_PREVIEW
    func feishuFocusPreview() -> some View {
        var preview = self
        preview._page = State(initialValue: .feishu)
        return preview
    }

    func feishuLayoutPreview(expandedSection: String? = nil, maximumHeight: CGFloat = 540) -> some View {
        var preview = self
        preview._feishuExpandedSection = State(initialValue: expandedSection)
        return preview.feishuPageLayout(maximumHeight: maximumHeight)
    }
    #endif

    private var feishuPage: some View {
        let availableHeight = max(320, min(720, (NSScreen.main?.visibleFrame.height ?? 800) - 96))
        return feishuPageLayout(maximumHeight: availableHeight)
            .onAppear {
                #if !FEISHU_LAYOUT_PREVIEW
                Task { await viewModel.refreshToolchainStatus() }
                #endif
            }
    }

    private func feishuPageLayout(maximumHeight: CGFloat) -> some View {
        ViewThatFits(in: .vertical) {
            feishuPageContent
            ScrollView { feishuPageContent }
                .frame(height: maximumHeight)
        }
        .frame(maxHeight: maximumHeight)
    }

    private var feishuPageContent: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                secondaryHeader(title: "飞书配置", backLabel: "返回设置") { page = .settings }
                ZStack {
                    headerIconButton(systemName: "arrow.clockwise", label: "刷新接入状态",
                        disabled: viewModel.feishuConfiguration.acting || viewModel.feishuConfiguration.reading) {
                        Task { await viewModel.refreshFeishuConfiguration(refresh: true); await viewModel.refreshToolchainStatus() }
                    }.opacity(viewModel.feishuConfiguration.reading ? 0 : 1)
                    if viewModel.feishuConfiguration.reading { ProgressView().controlSize(.small).accessibilityLabel("正在刷新接入状态") }
                }.frame(width: 28, height: 28)
            }
            feishuConfigurationOverview
            feishuSurface { toolchainSettings }
            feishuDiagnostics
            Text(viewModel.feishuConfiguration.snapshot?.diagnostics?.versionSummary ?? "飞书服务 未知 · lark-cli 未知")
                .font(.caption2).foregroundStyle(.secondary)
                .multilineTextAlignment(.center).frame(maxWidth: .infinity)
            if viewModel.feishuConfiguration.snapshot?.auth?.isAuthorized == true {
                HStack { Spacer(); feishuActionButton("logout"); Spacer() }.padding(.vertical, 8)
            }
        }
        .confirmationDialog(pendingFeishuAction?.title ?? "确认操作", isPresented: Binding(
            get: { pendingFeishuAction != nil },
            set: { if !$0 { pendingFeishuAction = nil } }
        ), titleVisibility: .visible) {
            if let intent = pendingFeishuAction {
                Button("确认", role: .destructive) {
                    pendingFeishuAction = nil
                    submitFeishuAction(intent, confirm: true)
                }
            }
            Button("取消", role: .cancel) { pendingFeishuAction = nil }
        } message: {
            Text(pendingFeishuAction?.confirmation ?? "")
        }
    }

    private var feishuConfigurationOverview: some View {
        feishuSurface {
            let configuration = viewModel.feishuConfiguration
            Text("飞书接入状态").font(.system(size: 13, weight: .semibold))
            if let snapshot = configuration.snapshot {
                Divider().padding(.vertical, 2)
                ForEach(["robot", "authorizedUser", "taskConnection"].compactMap { snapshot.fact($0) }) { fact in
                    feishuFactRow(fact)
                }
            }
            if feishuHasSetupContent || (configuration.acting && configuration.lastAction != "test_message") {
                Divider().padding(.vertical, 4)
                feishuSetupContent.frame(maxWidth: .infinity)
            }
            if let error = configuration.refreshError {
                Text(error).font(.caption2).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
            } else if configuration.lastAction != "test_message", ["failed", "unknown"].contains(configuration.outcome ?? ""), let message = configuration.message {
                Text(message).font(.caption2).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
            } else if let issue = configuration.snapshot?.issues?.first(where: { !($0.component == "operation" && $0.code == "test_message") }) {
                Text(issue.message).font(.caption2).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func feishuFactRow(_ fact: FeishuConfigurationSnapshot.Fact) -> some View {
        feishuSummaryRow(fact.title, value: fact.value).help(fact.evidenceHelp)
    }

    private func feishuSummaryRow(_ title: String, value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(title).foregroundStyle(.secondary)
            Spacer(minLength: 0)
            Text(value).multilineTextAlignment(.trailing)
        }
        .font(.caption)
        .accessibilityElement(children: .combine)
    }

    private func feishuSurface<Content: View>(@ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 8, content: content)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    private var feishuDiagnostics: some View {
        feishuSurface {
            DisclosureGroup(isExpanded: feishuDisclosure("diagnostics")) {
                VStack(alignment: .leading, spacing: 8) {
                    if let snapshot = viewModel.feishuConfiguration.snapshot {
                        ForEach(["application"].compactMap { snapshot.fact($0) }) { fact in feishuFactRow(fact) }
                        if let diagnostics = snapshot.diagnostics {
                            ForEach(diagnostics.missingApplicationScopes ?? [], id: \.self) { scope in
                                Text("应用权限缺项：" + scope).font(.caption2).textSelection(.enabled)
                            }
                            ForEach(diagnostics.missingUserScopes ?? [], id: \.self) { scope in
                                Text("用户授权缺项：" + scope).font(.caption2).textSelection(.enabled)
                            }
                            ForEach(diagnostics.recentOperations ?? []) { operation in
                                Text("\(operation.title ?? operation.action) · \(operation.statusText ?? operation.outcome)\n\(operation.stageText ?? operation.stage) · \(operation.code ?? "") · \(operation.updatedAt)\n\(operation.message)").font(.caption2)
                            }

                        }
                        if snapshot.action("restart")?.enabled == true { feishuActionButton("restart") }
                    }

                    if let target = viewModel.feishuConfiguration.snapshot?.diagnostics?.selfTarget, !target.isEmpty {
                        Divider()
                        VStack(spacing: 6) {
                            if viewModel.feishuConfiguration.acting && viewModel.feishuConfiguration.lastAction == "test_message" {
                                ProgressView("正在发送测试消息…").controlSize(.small).font(.caption)
                            } else { feishuActionButton("test_message") }
                            if viewModel.feishuConfiguration.lastAction == "test_message", let message = viewModel.feishuConfiguration.message {
                                Text(message).font(.caption2).foregroundStyle(.secondary).multilineTextAlignment(.center)
                            }
                        }.frame(maxWidth: .infinity)
                    }
                }.padding(.top, 8)
            } label: {
                Text("诊断详情").font(.caption.weight(.semibold))
            }
        }
    }

    private var toolchainSettings: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Codex 飞书技能").font(.system(size: 13, weight: .semibold))
            Text("让 Codex 使用当前飞书接入的消息、文档、日历等能力。")
                .font(.caption2).foregroundStyle(.secondary)
            Text(viewModel.toolchainStatus?.installationTitle ?? (viewModel.toolchainFeedback == nil ? "正在检查安装状态…" : "检查失败"))
                .font(.caption).foregroundStyle(.secondary)
            if let status = viewModel.toolchainStatus, status.isHealthy {
                Text("\(status.skills.count) 项 · ksf-lark-*").font(.caption2).foregroundStyle(.secondary)
            }
            HStack {
                Spacer()
                if let action = viewModel.toolchainStatus?.installationAction, !action.isEmpty {
                    Button(action) { confirmToolchainInstall = true }
                } else if viewModel.toolchainStatus == nil {
                    Button("检查安装") { Task { await viewModel.refreshToolchainStatus() } }
                }
                if viewModel.toolchainActionInProgress { ProgressView().controlSize(.small) }
                Spacer()
            }.controlSize(.small).disabled(viewModel.toolchainActionInProgress)
            if let details = viewModel.toolchainStatus?.installationDetails, !details.isEmpty {
                DisclosureGroup("查看具体文件") {
                    ForEach(details, id: \.self) { Text($0).font(.caption2).textSelection(.enabled) }
                }
            }
            if let feedback = viewModel.toolchainFeedback { Text(feedback).font(.caption2).foregroundStyle(.secondary) }
        }
        .confirmationDialog("安装 Codex 飞书技能？", isPresented: $confirmToolchainInstall, titleVisibility: .visible) {
            Button("确认安装") { viewModel.installToolchain() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("安装当前应用内置的 ksf-lark-* 技能及必要执行入口；迁移本应用管理且未修改的旧版。不会覆盖自行修改的文件，也不会自动授权或发送消息。")
        }
    }

    @ViewBuilder
    private var feishuSetupContent: some View {
        let configuration = viewModel.feishuConfiguration
        if let flow = configuration.snapshot?.flow, ["expired", "failed"].contains(flow.state) {
            Text(flow.state == "expired" ? "登录二维码已过期，请重新登录。" : "本次登录未完成，请重新登录。")
                .font(.caption2).foregroundStyle(.orange).multilineTextAlignment(.center)
        }
        if (configuration.acting || (configuration.lastAction == "start_auth" && configuration.outcome == "pending" && configuration.snapshot?.flow == nil)) && configuration.actionFlow == nil {
            ProgressView(configuration.lastAction == "start_auth" ? "正在准备登录…" : configuration.lastAction == "logout" ? "正在注销…" : "正在处理…")
                .controlSize(.small).font(.caption).frame(maxWidth: .infinity, minHeight: 36)
        } else if configuration.phase == .loading {
            ProgressView("正在读取配置…").controlSize(.small)
        } else if configuration.phase == .unknown {
            Text("当前结果尚未确认，不会自动重试配置操作。").font(.caption2).foregroundStyle(.secondary)
        } else if let flow = configuration.actionFlow {
            feishuPendingStep(flow)
        } else if configuration.snapshot?.action("create_app")?.enabled == true
                    || configuration.snapshot?.action("connect_app")?.enabled == true {
            Text("应用接入").font(.caption.weight(.semibold))
            Text("应用凭据由官方 CLI 保存；创建应用与用户授权是独立操作。")
                .font(.caption2).foregroundStyle(.secondary)
            if showExistingFeishuApp && configuration.snapshot?.action("connect_app")?.enabled == true {
                TextField("App ID", text: $feishuAppID).textFieldStyle(.roundedBorder)
                    .accessibilityLabel("已有飞书应用 App ID")
                SecureField("App Secret", text: $feishuAppSecret).textFieldStyle(.roundedBorder)
                    .accessibilityLabel("已有飞书应用 App Secret")
                feishuActionButton("connect_app", primary: true)
                    .disabled(feishuAppID.isEmpty || feishuAppSecret.isEmpty)
                Button("返回接入方式") { showExistingFeishuApp = false }.controlSize(.small)
            } else {
                if configuration.snapshot?.action("create_app")?.enabled == true {
                    feishuActionButton("create_app", primary: true)
                }
                if let action = configuration.snapshot?.action("connect_app"), action.enabled {
                    Button(action.title) { showExistingFeishuApp = true }
                        .controlSize(.small).disabled(!configuration.allows(action.id))
                }
            }
        } else if let action = configuration.priorityAction {
            HStack { Spacer(); feishuActionButton(action, primary: true); Spacer() }.padding(.vertical, 4)
            ForEach(configuration.pendingSetupActions) { action in feishuActionButton(action.id) }
        } else {
            ForEach(configuration.pendingSetupActions) { action in feishuActionButton(action.id) }
        }
    }

    private var feishuHasSetupContent: Bool {
        let configuration = viewModel.feishuConfiguration
        return configuration.phase != .current || configuration.snapshot?.flow != nil
            || (configuration.lastAction == "start_auth" && configuration.outcome == "pending" && configuration.snapshot?.flow == nil)
            || configuration.priorityAction != nil || !configuration.pendingSetupActions.isEmpty
            || configuration.snapshot?.action("connect_app")?.enabled == true
            || configuration.snapshot?.action("create_app")?.enabled == true
    }

    private func feishuPendingStep(_ flow: FeishuConfigurationSnapshot.Flow) -> some View {
        VStack(spacing: 8) {
            if let dataURL = flow.qrDataURL, let image = feishuQRCode(dataURL) {
                Image(nsImage: image).interpolation(.none).resizable().frame(width: 180, height: 180)
                    .accessibilityLabel("飞书当前流程二维码")
            } else if flow.state == "completed" {
                Color.clear.frame(width: 180, height: 180).accessibilityHidden(true)
            }
            if let code = flow.userCode { Text("验证码 \(code)").font(.caption2) }
            HStack(spacing: 6) {
                if viewModel.feishuConfiguration.acting || flow.state == "completed" {
                    ProgressView().controlSize(.small)
                    Text(viewModel.feishuConfiguration.lastAction == "cancel_flow" ? "正在取消登录…" : "正在确认登录…")
                } else { Text(flow.kind == "user" ? "请用飞书扫码授权" : "请用飞书扫码创建应用") }
            }.font(.caption2).foregroundStyle(.secondary).frame(height: 20)
            HStack(spacing: 12) {
                if flow.verificationURL != nil {
                    Button("在浏览器中授权") { viewModel.openFeishuFlowURL(flowID: flow.id) }.buttonStyle(.link)
                }
                if viewModel.feishuConfiguration.snapshot?.action("cancel_flow")?.enabled == true {
                    Button(flow.kind == "user" ? "取消登录" : "取消创建") { requestFeishuAction("cancel_flow", flowID: flow.id) }.buttonStyle(.link)
                }
            }.controlSize(.small)
            if flow.kind == "app", viewModel.feishuConfiguration.snapshot?.action("finish_app")?.enabled == true {
                feishuActionButton("finish_app", flowID: flow.id)
            }
            Text(flow.kind == "user" ? "取消本次登录，不撤销已有授权" : "取消本次等待，不删除已创建的应用")
                .font(.caption2).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity).padding(.vertical, 6)
        .id(flow.id)
        .disabled(viewModel.feishuConfiguration.acting)
    }

    private func feishuDisclosure(_ section: String) -> Binding<Bool> {
        Binding(
            get: { feishuExpandedSection == section },
            set: { feishuExpandedSection = $0 ? section : nil }
        )
    }

    @ViewBuilder
    private func feishuActionButton(_ id: String, primary: Bool = false, flowID: String? = nil) -> some View {
        if let action = viewModel.feishuConfiguration.snapshot?.action(id) {
            VStack(alignment: .leading, spacing: 3) {
                if primary {
                    Button(action.title) { requestFeishuAction(id, flowID: flowID) }.buttonStyle(.borderedProminent)
                } else if id == "logout" {
                    Button(action.title, role: .destructive) { requestFeishuAction(id, flowID: flowID) }
                        .buttonStyle(.plain).foregroundStyle(.red).font(.caption)
                } else {
                    Button(action.title) { requestFeishuAction(id, flowID: flowID) }.controlSize(.small)
                }
            }
            .disabled(!viewModel.feishuConfiguration.allows(id))
            .help(action.reason ?? action.title)
        }
    }

    private struct FeishuActionIntent {
        let id: String
        let title: String
        let confirmation: String
        let context: FeishuConfigurationContext
        let flowID: String?
        let feature: String?
        let mode: String?
        let targetAlias: String?
        let appID: String?
    }

    private func requestFeishuAction(_ id: String, feature: String? = nil, mode: String? = nil, flowID: String? = nil) {
        guard viewModel.feishuConfiguration.allows(id),
              let snapshot = viewModel.feishuConfiguration.snapshot,
              let action = snapshot.action(id) else { return }
        let appID = id == "connect_app" ? feishuAppID.trimmingCharacters(in: .whitespacesAndNewlines) : nil
        let featureTitle = snapshot.overview?.features.first { $0.id == feature }?.title
        let selfTarget = snapshot.diagnostics?.selfTarget ?? viewModel.selectedFeishuTargetAlias
        let confirmation = action.confirmationText(targetAlias: selfTarget, mode: mode, featureTitle: featureTitle, appID: appID)
        guard !action.requiresConfirmation || confirmation != nil else {
            viewModel.reportFeishuConfigurationIssue("缺少有效的操作确认内容，请检查配置后重试。")
            return
        }
        let intent = FeishuActionIntent(id: id, title: action.title, confirmation: confirmation ?? "",
            context: snapshot.context,
            flowID: flowID, feature: feature, mode: mode,
            targetAlias: id == "test_message" ? selfTarget : nil, appID: appID)
        if confirmation == nil { submitFeishuAction(intent, confirm: false) }
        else { pendingFeishuAction = intent }
    }

    private func submitFeishuAction(_ intent: FeishuActionIntent, confirm: Bool) {
        viewModel.performFeishuConfigurationAction(intent.id, confirm: confirm,
            appID: intent.appID,
            appSecret: intent.id == "connect_app" ? feishuAppSecret : nil,
            targetAlias: intent.targetAlias, feature: intent.feature, mode: intent.mode, flowID: intent.flowID,
            expectedContext: intent.context)
        if intent.id == "connect_app" { feishuAppSecret = "" }
    }

    private var feishuActivationTargetPicker: some View {
        let targets = viewModel.feishuConfiguration.snapshot?.overview?.targets
            ?? viewModel.feishuConfiguration.snapshot?.connection.targetAliases ?? []
        return Picker("测试目标", selection: Binding(
            get: { viewModel.selectedFeishuTargetAlias },
            set: { viewModel.setFeishuTargetAlias($0) }
        )) {
            Text("请选择").tag("")
            ForEach(targets, id: \.self) { Text($0).tag($0) }
        }
        .disabled(targets.isEmpty || viewModel.feishuActionInProgress)
    }

    private func feishuQRCode(_ dataURL: String) -> NSImage? {
        guard let separator = dataURL.firstIndex(of: ","),
              let data = Data(base64Encoded: String(dataURL[dataURL.index(after: separator)...])) else { return nil }
        return NSImage(data: data)
    }

    private func componentStatusRow(name: String, status: String, color: Color) -> some View {
        HStack(spacing: 7) {
            Circle()
                .fill(color)
                .frame(width: 7, height: 7)
            Text(name)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            Text(status)
                .font(.caption.weight(.semibold))
        }
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
        disabled: Bool = false,
        action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: 12, weight: .semibold))
                .frame(width: 22, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(disabled)
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

    private func semanticBadge(
        _ text: String,
        systemImage: String? = nil,
        color: Color
    ) -> some View {
        HStack(spacing: 3) {
            if let systemImage {
                Image(systemName: systemImage)
                    .font(.system(size: 8, weight: .semibold))
            }
            Text(text)
                .lineLimit(1)
        }
        .font(.system(size: 9, weight: .semibold, design: .rounded))
        .foregroundStyle(color)
        .padding(.horizontal, 6)
        .padding(.vertical, 2)
        .background(color.opacity(0.11), in: Capsule())
        .accessibilityElement(children: .combine)
    }

    private func taskStatusColor(_ task: ProjectTaskItem) -> Color {
        switch task.classification {
        case .running: return .blue
        case .waiting: return .orange
        case .completed: return .green
        case .ignored: return .secondary
        }
    }

    private func validationStatusColor(_ status: String) -> Color {
        switch status {
        case "高可用": return .green
        case "可用": return .blue
        case "待验证": return .orange
        default: return .secondary
        }
    }

    private func skillStageColor(_ stage: String) -> Color {
        switch stage.lowercased() {
        case "active": return .green
        case "trial": return .orange
        default: return .secondary
        }
    }

    private func feishuTaskLinkColor(_ state: String) -> Color {
        switch state {
        case "waiting_current_turn", "waiting_input", "queued", "desktop_action_required": return .orange
        case "running", "connected": return .blue
        case "completed": return .green
        case "failed", "expired": return .red
        default: return .secondary
        }
    }

    private func feishuTurnOwnerText(_ owner: String) -> String {
        switch owner {
        case "desktop": return "Codex Desktop"
        case "bridge": return "飞书服务"
        default: return "无运行轮次"
        }
    }

    private func feishuRemainingTime(_ seconds: Int) -> String {
        guard seconds > 0 else { return "即将失效" }
        let hours = seconds / 3600
        let minutes = (seconds % 3600) / 60
        return hours > 0 ? "\(hours)小时\(minutes)分" : "\(max(1, minutes))分钟"
    }

    private func feishuTimestamp(_ value: String) -> String {
        let formatter = ISO8601DateFormatter()
        guard let date = formatter.date(from: value) else { return "刚刚" }
        let display = DateFormatter()
        display.locale = Locale(identifier: "zh_CN")
        display.dateFormat = Calendar.current.isDateInToday(date) ? "HH:mm:ss" : "M月d日 HH:mm"
        return display.string(from: date)
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

    private func emptyState(_ title: String, detail: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.caption.weight(.semibold))
            Text(detail).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    private func localTokenHistoryDateLabel(_ value: String) -> String {
        let calendar = Calendar.current
        let today = LocalTokenUsageReader.dateString(for: Date(), calendar: calendar)
        if value == today,
           let date = Self.localHistoryInputDateFormatter.date(from: value) {
            return "今天 · \(Self.localHistoryDisplayDateFormatter.string(from: date))"
        }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: Date()),
           value == LocalTokenUsageReader.dateString(for: yesterday, calendar: calendar),
           let date = Self.localHistoryInputDateFormatter.date(from: value) {
            return "昨天 · \(Self.localHistoryDisplayDateFormatter.string(from: date))"
        }
        guard let date = Self.localHistoryInputDateFormatter.date(from: value) else { return value }
        return "\(Self.localHistoryDisplayDateFormatter.string(from: date)) · \(Self.localHistoryWeekdayFormatter.string(from: date))"
    }

    private func localTokenHistoryShortDateLabel(_ value: String) -> String {
        guard let date = Self.localHistoryInputDateFormatter.date(from: value) else { return value }
        return Self.localHistoryDisplayDateFormatter.string(from: date)
    }

    private func localTokenHistoryAxisEndLabel(_ value: String) -> String {
        let today = LocalTokenUsageReader.dateString(for: Date(), calendar: .current)
        return value == today ? "今天" : localTokenHistoryShortDateLabel(value)
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

    private func projectAccessibilityLabel(_ item: ProjectDashboardItem) -> String {
        let name = item.isUnassigned ? "无项目" : item.project?.name ?? unavailableProjectName(item.id)
        return "\(name)，\(item.runningCount) 个任务运行中，\(item.waitingCount) 个任务等待处理"
    }

    private func taskDisplayName(_ task: ProjectTaskItem) -> String {
        guard let name = task.name, !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return "未命名任务"
        }
        return name
    }

    private func taskStatusSymbol(_ task: ProjectTaskItem) -> String {
        switch task.classification {
        case .waiting: return "person.fill.questionmark"
        case .running: return "play.fill"
        case .completed: return "checkmark.circle"
        case .ignored: return "circle"
        }
    }

    private func taskStatusText(_ task: ProjectTaskItem) -> String {
        if task.classification == .completed { return "已完成" }
        guard task.classification == .waiting else { return "运行中" }
        switch task.waitingReason {
        case .approval: return "待批准"
        case .planConfirmation: return "待确认计划"
        case .userInput: return "待回复"
        case .actionRequired, .none: return "待处理"
        }
    }

    private func taskCategoryAndJobText(_ route: KSFRouteSummary?) -> String {
        guard let route else { return "未绑定 KSF 路由" }
        return [
            route.category?.name ?? "未分类",
            route.mainJob?.name ?? "无主岗位",
        ].joined(separator: " · ")
    }

    private func taskAbilityText(_ route: KSFRouteSummary) -> String {
        let names = route.abilities.compactMap(\.name).filter { !$0.isEmpty }
        return names.isEmpty ? "无基本功" : names.joined(separator: "、")
    }

    private func taskAccessibilityLabel(_ task: ProjectTaskItem) -> String {
        guard let route = task.route else {
            return "\(taskDisplayName(task))，\(taskStatusText(task))，未绑定 KSF 路由"
        }
        return "\(taskDisplayName(task))，\(taskStatusText(task))，\(taskCategoryAndJobText(route))，基本功：\(taskAbilityText(route))，\(route.dispatchableSkills.count) 个可调度 Skill"
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

    private static let localHistoryInputDateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.timeZone = .current
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()

    private static let localHistoryDisplayDateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "zh_Hans_CN")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.timeZone = .current
        formatter.dateFormat = "M月d日"
        return formatter
    }()

    private static let localHistoryWeekdayFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "zh_Hans_CN")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.timeZone = .current
        formatter.dateFormat = "EEE"
        return formatter
    }()

    private enum Page {
        case home
        case tokenHistory
        case projectLibrary
        case workspaceLibrary
        case taskDetail
        case settings
        case pricing
        case feishu
    }
}
