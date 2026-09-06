import Foundation
import XCTest

final class FeishuSettingsLayoutContractTests: XCTestCase {
    func testAuthAndToolchainControlsStayExplicitAndUseSanitizedCoreContract() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let view = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"))
        let client = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/CoreServiceProcessClient.swift"))
        let model = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"))
        for label in ["CLI 用户授权", "补充授权", "退出当前飞书用户授权？", "我已授权，检查", "官方工具链与 Skills", "安装官方工具链？"] {
            XCTAssertTrue(view.contains(label), label)
        }
        for method in ["feishu/auth/status", "feishu/auth/start", "feishu/auth/finish", "feishu/auth/logout", "toolchain/status", "toolchain/install"] {
            XCTAssertTrue(client.contains(method), method)
        }
        XCTAssertTrue(client.contains("params: [\"scope\": \"required\"]"))
        XCTAssertTrue(client.contains("params: [\"confirm\": true]"))
        XCTAssertFalse(client.contains("deviceCode"))
        XCTAssertFalse(model.contains("feishuAuthQRCode"))
        XCTAssertTrue(model.contains("!feishuActionInProgress"))
        XCTAssertTrue(model.contains("!toolchainActionInProgress"))
        XCTAssertTrue(view.contains("用户授权不代表所有功能权限齐备"))
    }

    func testPermissionOverviewAcceptsNullMissingForUpgradeCompatibility() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/CoreServiceProcessClient.swift"))
        XCTAssertTrue(source.contains("decodeIfPresent([String].self, forKey: .missing) ?? []"))
    }

    func testReadySettingsExposePermissionFeatureAndDiagnosticsInline() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"))
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
