import Foundation
import XCTest

final class FeishuSettingsLayoutContractTests: XCTestCase {
    func testPermissionOverviewAcceptsNullMissingForUpgradeCompatibility() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("Sources/CodexUsageBar/CoreServiceProcessClient.swift"))
        XCTAssertTrue(source.contains("decodeIfPresent([String].self, forKey: .missing) ?? []"))
    }

    func testReadySettingsExposePermissionFeatureAndDiagnosticsInline() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("Sources/CodexUsageBar/UsagePopoverView.swift"))
        XCTAssertFalse(source.contains("feishuPermissionsPage"))
        XCTAssertFalse(source.contains("feishuAdvancedPage"))
        XCTAssertFalse(source.contains("feishuDiagnosticsPage"))
        XCTAssertTrue(source.contains("允许真实执行？"))
        XCTAssertTrue(source.contains("基础单聊、卡片回调和 Codex 任务控制已授权"))
        let ready = try XCTUnwrap(source.range(of: "private var feishuReadySettings"))
        let next = try XCTUnwrap(source.range(of: "private func feishuFeatureControl"))
        let section = source[ready.lowerBound..<next.lowerBound]
        XCTAssertTrue(section.contains("Text(\"权限\")"))
        XCTAssertTrue(section.contains("Text(\"接收与高级功能\")"))
        XCTAssertTrue(section.contains("Text(\"诊断\")"))
        XCTAssertTrue(section.contains("Text(\"连接测试\")"))
        XCTAssertFalse(section.contains("Text(\"运行组件\")"))
        XCTAssertFalse(section.contains("feishuNavigationRow"))
    }
}
