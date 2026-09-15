import Foundation
import XCTest

final class CodexWorkspaceLayoutContractTests: XCTestCase {
    func testWorkspaceSectionIsConditionalAndOrdinaryTasksHideKSFRoute() throws {
        let repositoryRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let source = try String(
            contentsOf: repositoryRoot.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"),
            encoding: .utf8
        )
        let client = try String(
            contentsOf: repositoryRoot.appendingPathComponent("Sources/KSFAssistant/CoreServiceProcessClient.swift"),
            encoding: .utf8
        )
        let viewModel = try String(
            contentsOf: repositoryRoot.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"),
            encoding: .utf8
        )

        XCTAssertTrue(source.contains("if !viewModel.workspaceLibraryItems.isEmpty"))
        XCTAssertTrue(source.contains("if !viewModel.homeWorkspaceItems.isEmpty"))
        XCTAssertTrue(viewModel.contains("var homeWorkspaceItems: [CodexWorkspaceItem]"))
        XCTAssertTrue(viewModel.contains("CodexWorkspaceWorkset.select(from: workspaceDashboard.workspaces, connectedTaskKeys: connectedTaskKeys)"))
        XCTAssertTrue(viewModel.contains("func toggleWorkspacePinned"))
        XCTAssertTrue(source.contains("Text(\"Codex 工作区\")"))
        XCTAssertTrue(source.contains("projectTaskRow(task, showsKSFRoute: false)"))
        XCTAssertTrue(source.contains("if context.showsKSFRoute"))
        XCTAssertTrue(source.contains("workspaceLibraryPage"))
        XCTAssertFalse(workspaceContainer(in: source).contains("未绑定 KSF 路由"))
        XCTAssertFalse(workspaceContainer(in: source).contains("taskAbilitySummaryLine"))
        XCTAssertTrue(workspaceContainer(in: source).contains("VStack(alignment: .leading, spacing: 3)"))
        XCTAssertTrue(workspaceContainer(in: source).contains("viewModel.toggleWorkspacePinned(item.id)"))
        XCTAssertTrue(workspaceContainer(in: source).contains("workspaceActionFooter(item)"))
        XCTAssertTrue(source.contains("viewModel.createTask(for: item)"))
        XCTAssertTrue(source.contains("viewModel.openWorkspaceDirectory(item)"))
        XCTAssertTrue(client.contains("func createWorkspaceTask"))
        XCTAssertTrue(taskRowContainer(in: source).contains(".padding(.vertical, 4)"))
        XCTAssertTrue(client.contains("let workspaces: WorkspacesDTO?"))
        XCTAssertTrue(client.contains("workspaces?.snapshot ?? CodexWorkspaceSnapshot(availability: .available)"))
        XCTAssertTrue(client.contains("\"pinnedWorkspaceIds\""))
    }

    private func workspaceContainer(in source: String) -> Substring {
        let start = source.range(of: "private func workspaceContainer")!
        let end = source.range(
            of: "private func projectTaskRow",
            range: start.upperBound..<source.endIndex
        )!
        return source[start.lowerBound..<end.lowerBound]
    }

    private func taskRowContainer(in source: String) -> Substring {
        let start = source.range(of: "private func projectTaskRow")!
        let end = source.range(
            of: "private func taskRouteSummaryLine",
            range: start.upperBound..<source.endIndex
        )!
        return source[start.lowerBound..<end.lowerBound]
    }
}
