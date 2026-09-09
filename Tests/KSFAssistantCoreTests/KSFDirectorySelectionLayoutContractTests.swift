import Foundation
import XCTest

final class KSFDirectorySelectionLayoutContractTests: XCTestCase {
    func testConnectedLibraryUsesCancelAndHomeRemainsStateGated() throws {
        let repositoryRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let view = try String(
            contentsOf: repositoryRoot.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"),
            encoding: .utf8
        )
        let viewModel = try String(
            contentsOf: repositoryRoot.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"),
            encoding: .utf8
        )

        XCTAssertTrue(view.contains("if viewModel.isOnboardingComplete"))
        XCTAssertTrue(view.contains("Button(\"取消\", role: .destructive) { viewModel.cancelKSFRoot() }"))
        XCTAssertTrue(view.contains("Button(\"选择目录\") { viewModel.chooseKSFRoot() }"))
        XCTAssertFalse(view.contains("viewModel.isOnboardingComplete ? \"更换\" : \"选择目录\""))
        XCTAssertTrue(viewModel.contains("func cancelKSFRoot()"))
        XCTAssertTrue(viewModel.contains("updateIntegrationContext(ksfRoot: \"\")"))
        XCTAssertTrue(viewModel.contains("self.isOnboardingComplete = false"))
        XCTAssertTrue(viewModel.contains("self.projectDashboard = ProjectDashboardSnapshot(availability: .unavailable)"))
    }
}
