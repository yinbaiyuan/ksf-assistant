import Foundation
import XCTest

final class FeishuTaskLinkRecoveryContractTests: XCTestCase {
    func testPendingTaskLinkIsVisibleAndExplainsSafeRecovery() throws {
        let repositoryRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let source = try String(
            contentsOf: repositoryRoot.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"),
            encoding: .utf8
        )

        XCTAssertTrue(source.contains("link.linkState == \"active\" || link.linkState == \"pending\""))
        XCTAssertTrue(source.contains("为避免重复发送，系统不会自动重试"))
    }
}
