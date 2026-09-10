import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private func expect(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    guard condition() else { throw TestFailure(description: message) }
}

@main
private enum UILayoutTestRunner {
    static func main() {
        do {
            guard CommandLine.arguments.count == 2 else {
                throw TestFailure(description: "expected UsagePopoverView source path")
            }
            let source = try String(
                contentsOfFile: CommandLine.arguments[1],
                encoding: .utf8
            )
            guard
                let start = source.range(of: "private var projectLibraryPage"),
                let end = source.range(
                    of: "private var settingsPage",
                    range: start.upperBound..<source.endIndex
                )
            else {
                throw TestFailure(description: "project-library source section is missing")
            }
            let section = source[start.lowerBound..<end.lowerBound]

            try expect(section.contains("VStack(spacing: 0)"), "project catalog is not one continuous stack")
            try expect(!section.contains("ScrollView"), "project catalog still has an internal ScrollView")
            try expect(!section.contains("LazyVStack"), "project catalog still defers off-screen rows")
            try expect(!section.contains(".frame(height:"), "project catalog still has a height cap")
            try expect(!section.contains("OverlayScrollerConfigurator"), "project catalog still reserves a scroller gutter")
            try expect(!source.contains("projectLibraryViewportHeight"), "project catalog still computes a viewport height")
            try expect(!section.contains("projectLibraryStatus(item)"), "project catalog still shows a status subtitle")
            try expect(
                section.contains("projectActionFooter(project: project, item: item)"),
                "project catalog does not show the shared project action footer"
            )
            let sharedFooterCallCount = source.components(
                separatedBy: "projectActionFooter(project: project, item: item)"
            ).count - 1
            try expect(sharedFooterCallCount == 2, "home and catalog do not share one project action footer")
            guard
                let archiveButton = section.range(of: "projectArchiveTaskIconControl(project: project)"),
                let pinButton = section.range(of: "systemName: item.isPinned ? \"pin.fill\" : \"pin\"")
            else {
                throw TestFailure(description: "project catalog archive or pin control is missing")
            }
            try expect(archiveButton.lowerBound < pinButton.lowerBound, "archive control is not before the pin control")
            let archiveButtonCallCount = source.components(
                separatedBy: "projectArchiveTaskIconControl(project: project)"
            ).count - 1
            try expect(archiveButtonCallCount == 1, "archive control is not scoped to the project catalog")
            try expect(source.contains("private var feishuPage"), "KSFAssistant Feishu settings page is missing")
            try expect(source.contains("扫码连接飞书"), "single-scan Feishu setup path is missing")
            try expect(!source.contains("接入已有应用"), "retired local existing-app setup path is still visible")
            try expect(source.contains("feishuSetupContent"), "resumable Feishu setup state is missing")
            try expect(source.contains("确认启用并发送测试消息"), "Feishu setup has no explicit activation confirmation")
            try expect(source.contains("activateFeishuSetup"), "Feishu setup activation is not wired to the KSFAssistant Core")
            try expect(!source.contains("Button(\"启动\")"), "Feishu exposes a lifecycle-conflicting start button")
            try expect(source.contains("KSF 是可选增强能力"), "KSF is not described as optional")
            try expect(!source.contains("if !viewModel.isOnboardingComplete"), "KSF still blocks the whole application")
            try expect(source.contains("viewModel.feishuService.targetAliases"), "target picker is not alias-only")
            try expect(!source.contains("private var weChatPage"), "legacy WeChat page is still present")
            try expect(source.contains("case .taskDetail:"), "task detail page is not routed")
            try expect(source.contains("private var taskDetailPage"), "task detail page is missing")
            try expect(source.contains("systemName: \"info.circle\", label: \"查看任务详情\""), "task detail control is missing")
            try expect(source.contains("Button { viewModel.openTask(task) }"), "task body does not open the Codex task")
            try expect(!source.contains("expandedTaskID"), "legacy inline task expansion state remains")
            try expect(!source.contains("toggleTaskExpansion"), "legacy inline task expansion action remains")
            try expect(!source.contains("收起任务路由"), "legacy task collapse control remains")
            try expect(source.contains("private func taskStatusColor"), "central task status colors are missing")
            try expect(source.contains("private func projectTaskCount"), "quiet project task count is missing")
            try expect(source.contains("private func taskRouteSummaryLine"), "neutral home route summary is missing")
            try expect(source.contains("private func taskAbilitySummaryLine"), "neutral home ability summary is missing")
            try expect(source.contains(".frame(height: 20, alignment: .center)"), "task status and trailing controls do not share a center line")
            try expect(source.contains("ProgressView().controlSize(.mini).frame(width: 20, height: 20)"), "task-link progress does not preserve the action alignment grid")
            try expect(source.contains("private func feishuTaskLinkColor"), "central Feishu task-link colors are missing")
            try expect(source.contains("连接 24 小时"), "task detail does not distinguish the connection lease from Codex permissions")
            try expect(!source.contains("全权限 · 24 小时"), "task detail still presents the connection lease as a permission grant")
            try expect(source.contains("feishuTaskLinkError(for: task)"), "home task row does not expose task-link failures locally")
            try expect(source.contains("飞书连接失败"), "task-link failure does not have an accessible local label")
            try expect(source.contains("当前控制："), "task detail does not show the current turn owner")
            try expect(source.contains("feishuRemainingTime"), "task detail does not show remaining lease time")
            try expect(source.contains("private func semanticBadge"), "semantic badge component is missing")
            try expect(source.contains("private func validationStatusColor"), "task-detail validation color is missing")
            try expect(source.contains("private func skillStageColor"), "task-detail Skill stage color is missing")
            try expect(source.contains("private func taskDetailActions(_ task: ProjectTaskItem)"), "balanced task-detail action strip is missing")
            try expect(source.contains("Label(\"打开 Codex\", systemImage: \"arrow.up.forward.app\")"), "compact Codex action is missing")
            try expect(source.contains("link == nil ? \"连接飞书\" : \"解除飞书\""), "compact Feishu action is missing")
            try expect(source.contains(".buttonStyle(.borderedProminent)"), "compact Codex action is not prioritized")
            try expect(!source.contains("Label(\"在 Codex 中打开任务\""), "legacy full-width Codex action remains")
            try expect(!source.contains("Button(link == nil ? \"连接到飞书\""), "legacy uneven Feishu action remains")
            try expect(source.contains("color: .indigo"), "route category semantic color is missing")
            try expect(source.contains("color: .purple"), "route job semantic color is missing")
            try expect(source.contains("color: .teal"), "route ability semantic color is missing")

            print("PASS project catalog, task-detail navigation, and alias-only KSFAssistant Feishu settings")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
