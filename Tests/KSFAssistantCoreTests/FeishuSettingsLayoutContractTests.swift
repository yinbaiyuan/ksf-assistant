import Foundation
import XCTest

final class FeishuSettingsLayoutContractTests: XCTestCase {
    private func source(_ name: String) throws -> String {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        return try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/\(name).swift"))
    }

    func testConfigurationUsesOnlyCoordinatorAndNoRetiredMutationEndpoints() throws {
        let client = try source("CoreServiceProcessClient")
        let model = try source("UsageViewModel")
        for method in ["feishu/configuration/read", "feishu/configuration/action"] {
            XCTAssertTrue(client.contains(method), method)
        }
        for retired in ["feishu/setup/", "feishu/auth/", "feishu/features/update", "feishu/service/control", "feishu/test\""] {
            XCTAssertFalse(client.contains(retired), retired)
        }
        XCTAssertTrue(model.contains("await refreshFeishuConfiguration()"))
        XCTAssertTrue(model.contains("session.onChange = { [weak self] state in self?.feishuConfiguration = state }"))
        XCTAssertTrue(model.contains("expectedContext: expectedContext"))
        XCTAssertFalse(model.contains("FeishuSetupState.notStarted"))
        XCTAssertFalse(model.contains("applyFeishuSetup"))
    }

    func testCoreTitlesFactsAndActionsReplaceHostReadinessInference() throws {
        let view = try source("UsagePopoverView")
        XCTAssertTrue(view.contains("value: fact.value"))
        XCTAssertTrue(view.contains("Button(action.title)"))
        XCTAssertTrue(view.contains("feishuNeedsApplicationSetup"))
        XCTAssertTrue(view.contains("let create = configuration.snapshot?.action(\"create_app\")"))
        XCTAssertTrue(view.contains("if !create.enabled, let reason = create.reason"))
        XCTAssertFalse(view.contains("configuration.snapshot?.action(\"connect_app\")"))
        XCTAssertTrue(view.contains("Text(viewModel.feishuConfiguration.summaryTitle)"))
        for retired in ["FeishuConfigurationPresentation(", "switch viewModel.feishuSetup.stage",
                        "feishuAuthStatus?.profileValid == true", "samePendingStep", "23 类事件",
                        "单聊收发、卡片回调与 Codex 控制均已就绪"] {
            XCTAssertFalse(view.contains(retired), retired)
        }
        XCTAssertTrue(view.contains("[\"robot\", \"authorizedUser\", \"taskConnection\"].compactMap { snapshot.fact($0) }"))
        XCTAssertFalse(view.contains("[\"application\", \"user\", \"bot\", \"connection\"]"))
        XCTAssertFalse(view.contains("[\"user\", \"bot\", \"operator\"]"))
        XCTAssertTrue(view.contains("\"desktop\""))
        XCTAssertFalse(view.contains("snapshot.setup.stage"))
    }

    func testApplicationOwnsPollingUntilShutdown() throws {
        let view = try source("UsagePopoverView")
        let model = try source("UsageViewModel")
        let start = try XCTUnwrap(view.range(of: "private var feishuPage:"))
        let end = try XCTUnwrap(view.range(of: "private func feishuPageLayout"))
        let page = view[start.lowerBound..<end.lowerBound]
        XCTAssertFalse(page.contains("refreshToolchainStatus"))
        XCTAssertFalse(page.contains("startFeishuConfigurationPolling()"))
        XCTAssertTrue(model.contains("feishuConfigurationSession.startPolling()"))
        XCTAssertFalse(page.contains(".onDisappear"))
        XCTAssertFalse(page.contains("stopFeishuConfigurationPolling()"))
        let closeStart = try XCTUnwrap(model.range(of: "func popoverDidClose()"))
        let closeEnd = try XCTUnwrap(model.range(of: "func completeOnboarding()"))
        XCTAssertFalse(model[closeStart.lowerBound..<closeEnd.lowerBound].contains("stopFeishuConfigurationPolling()"))
        let shutdown = try XCTUnwrap(model.range(of: "func shutdown()"))
        let quit = try XCTUnwrap(model.range(of: "func quit()"))
        XCTAssertTrue(model[shutdown.lowerBound..<quit.lowerBound].contains("feishuConfigurationSession.shutdown()"))
        XCTAssertTrue(view.contains("refreshFeishuConfiguration(refresh: true)"))
    }

    func testLayoutKeepsFullWidthSurfacesSingleDisclosureAndHeightFallback() throws {
        let view = try source("UsagePopoverView")
        let start = try XCTUnwrap(view.range(of: "private var feishuPageContent:"))
        let end = try XCTUnwrap(view.range(of: "private var feishuConfigurationOverview:"))
        let page = view[start.lowerBound..<end.lowerBound]
        let overview = try XCTUnwrap(page.range(of: "feishuConfigurationOverview"))
        XCTAssertFalse(page.contains("feishuSurface { feishuSetupContent }"))
        XCTAssertTrue(view.contains("feishuSetupContent.frame(maxWidth: .infinity)"))
        let diagnostics = try XCTUnwrap(page.range(of: "feishuDiagnostics"))
        XCTAssertLessThan(overview.lowerBound, diagnostics.lowerBound)
        for label in ["诊断详情", "飞书接入状态"] {
            XCTAssertTrue(view.contains("Text(\"\(label)\")"), label)
        }
        XCTAssertTrue(view.contains("feishuExpandedSection = $0 ? section : nil"))
        XCTAssertTrue(view.contains("ViewThatFits(in: .vertical)"))
        XCTAssertTrue(view.contains("ScrollView { feishuPageContent }"))
        XCTAssertFalse(view.contains("showExistingFeishuApp"))
        XCTAssertFalse(view.contains("feishuAdvancedPage"))
    }

    func testFlowAndConfirmationUseCapturedContextNotLegacyQR() throws {
        let view = try source("UsagePopoverView")
        let model = try source("UsageViewModel")
        XCTAssertTrue(view.contains("let dataURL = flow.qrDataURL"))
        XCTAssertTrue(view.contains("if let flow = configuration.actionFlow"))
        XCTAssertTrue(view.contains(".id(flow.id)"))
        XCTAssertTrue(view.contains("flowID: flow.id"))
        XCTAssertTrue(view.contains("expectedContext: intent.context"))
        XCTAssertTrue(view.contains(".confirmationDialog(pendingFeishuAction"))
        XCTAssertTrue(view.contains("action.requiresConfirmation"))
        XCTAssertTrue(view.contains("\"test_message\""))
        XCTAssertTrue(view.contains("configuration.pendingSetupActions"))
        XCTAssertFalse(view.contains("不会随着启用出站自动发送"))
        XCTAssertTrue(view.contains("取消本次登录，不撤销已有授权"))
        XCTAssertFalse(view.contains("auth?.qrDataURL"))
        XCTAssertFalse(view.contains("feishuSetupQRCode"))
        XCTAssertTrue(model.contains("flow.id == flowID"))
        XCTAssertTrue(model.contains("url.scheme?.lowercased() == \"https\""))
    }

    func testAgentToolchainInstallationIsRemoved() throws {
        let view = try source("UsagePopoverView")
        let client = try source("CoreServiceProcessClient")
        XCTAssertFalse(view.contains("Codex 飞书技能"))
        XCTAssertFalse(view.contains(" · lark-cli "))
        XCTAssertFalse(client.contains("toolchain/install"))
        XCTAssertFalse(client.contains("deviceCode"))
        for retired in ["主设备", "仅手动能力", "feishuProfileText", "setFeishuProfile"] {
            XCTAssertFalse(view.contains(retired), retired)
        }
    }

    func testConfigurationSubmissionReusesExistingDesktopApprovalAvailability() throws {
        let controller = try source("DesktopInteractionGuard")
        let model = try source("UsageViewModel")
        let view = try source("UsagePopoverView")
        XCTAssertTrue(controller.contains("NSApp.modalWindow == nil"))
        XCTAssertTrue(controller.contains("CGSSessionScreenIsLocked"))
        XCTAssertTrue(model.contains("!self.quitRequested && !self.shutdownStarted && self.popoverIsOpen"))
        XCTAssertTrue(model.contains("DesktopInteractionGuard.allowsConfigurationSubmission"))
        XCTAssertTrue(model.contains("FeishuConfigurationSession(canSubmit:"))
        XCTAssertTrue(view.contains(".help(fact.evidenceHelp)"))
        XCTAssertTrue(view.contains("configuration.priorityAction"))
    }
}
