import XCTest
@testable import KSFAssistantCore

final class CodexWorkspaceModelsTests: XCTestCase {
    func testWorkspaceKeepsCompletedTasksAlongsideActiveTasks() {
        let workspace = CodexWorkspaceItem(
            id: "workspace",
            kind: "workspace",
            name: "Example",
            tasks: [task("running", .running), task("waiting", .waiting), task("done", .completed)],
            usage: ProjectUsageSummary(cumulativeTokens: 120, todayTokens: 20, trackingStartedAt: Date(timeIntervalSince1970: 1)),
            runningCount: 1,
            waitingCount: 1,
            totalTaskCount: 3
        )

        XCTAssertEqual(workspace.tasks.map(\.threadID), ["running", "waiting", "done"])
        XCTAssertEqual(workspace.activeTaskCount, 2)
        XCTAssertEqual(workspace.totalTaskCount, 3)
        XCTAssertEqual(workspace.usage?.cumulativeTokens, 120)
    }

    func testHomeWorksetIncludesPinnedOrActiveWorkspacesOnly() {
        let pinned = CodexWorkspaceItem(id: "pinned", kind: "workspace", name: "Pinned", isPinned: true)
        let active = CodexWorkspaceItem(id: "active", kind: "workspace", name: "Active", runningCount: 1)
        let inactive = CodexWorkspaceItem(id: "inactive", kind: "workspace", name: "Inactive")

        XCTAssertEqual(CodexWorkspaceWorkset.select(from: [pinned, active, inactive]).map(\.id), ["pinned", "active"])
    }

    private func task(_ id: String, _ classification: TaskActivityClassifier.Classification) -> ProjectTaskItem {
        ProjectTaskItem(
            threadID: id,
            hostID: "local",
            classification: classification,
            createdAt: Date(timeIntervalSince1970: 1),
            projectID: "workspace"
        )
    }
}
