import AppKit
import CoreImage.CIFilterBuiltins
import KSFAssistantCore
import SwiftUI

@main
struct FeishuLayoutSnapshots {
    @MainActor
    static func main() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let home = environment["CFFIXED_USER_HOME"],
              home.contains("ksfas-layout-preview"),
              FileManager.default.homeDirectoryForCurrentUser.resolvingSymlinksInPath().path == URL(fileURLWithPath: home).resolvingSymlinksInPath().path,
              AppConfiguration.applicationSupportDirectory().resolvingSymlinksInPath().path.hasPrefix(URL(fileURLWithPath: home).resolvingSymlinksInPath().path + "/"),
              let output = environment["KSF_LAYOUT_OUTPUT"] else {
            fatalError("An isolated preview home and output directory are required.")
        }
        _ = NSApplication.shared
        NSApplication.shared.setActivationPolicy(.prohibited)
        for dark in [false, true] {
            let model = UsageViewModel(autoStart: false, cleanupLegacyWeChatData: false)
            let host = NSHostingView(rootView: UsagePopoverView(viewModel: model, refreshOnAppear: false)
                .settingsLayoutPreview().environment(\.colorScheme, dark ? .dark : .light))
            host.frame = NSRect(origin: .zero, size: host.fittingSize)
            host.layoutSubtreeIfNeeded()
            let bitmap = host.bitmapImageRepForCachingDisplay(in: host.bounds)!
            host.cacheDisplay(in: host.bounds, to: bitmap)
            try bitmap.representation(using: .png, properties: [:])!.write(
                to: URL(fileURLWithPath: output).appendingPathComponent("settings-\(dark ? "dark" : "light").png"))
        }
        let toolchain = ToolchainStatus(schemaVersion: 1, version: "1.0.93", installed: true, healthy: true,
            skills: (1...28).map { .init(name: "fixture-\($0)", state: "managed") }, problemCount: 0, installationState: "installed", installationTitle: "已安装", installationAction: "", installationDetails: [])
        let fixtureRoot = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let fixtureData = try Data(contentsOf: fixtureRoot.appendingPathComponent("Fixtures/FeishuConfiguration/snapshot.json"))
        let original = try JSONSerialization.jsonObject(with: fixtureData) as! [String: Any]
        try verifyConfigurationSurvivesWindowFocus(fixtureData: fixtureData, toolchain: toolchain)
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data("KSFAssistant isolated visual fixture, not an authorization code".utf8)
        let qr = filter.outputImage!.transformed(by: CGAffineTransform(scaleX: 6, y: 6))
        let qrBitmap = NSBitmapImageRep(cgImage: CIContext().createCGImage(qr, from: qr.extent)!)
        let qrDataURL = "data:image/png;base64," + qrBitmap.representation(using: .png, properties: [:])!.base64EncodedString()
        for fixture in ["loading", "unknown", "signed_out", "create", "blocked", "app_pending", "auth_pending", "refreshing", "connected", "failed", "completed", "expired", "connected-authorization", "connected-features", "connected-test", "connected-diagnostics"] {
            let parts = fixture.split(separator: "-", maxSplits: 1).map(String.init)
            let state = parts[0]
            let expandedSection = parts.count == 2 ? parts[1] : nil
            for dark in [false, true] {
                var object = original
                if ["create", "blocked", "app_pending"].contains(state) {
                    let data = try Data(contentsOf: fixtureRoot.appendingPathComponent("Fixtures/FeishuConfiguration/not-configured.json"))
                    object = try JSONSerialization.jsonObject(with: data) as! [String: Any]
                    precondition((object["summary"] as? [String: Any])?["state"] as? String == "not_configured")
                }
                // Explicit offline visual states; never derive readiness in the host.
                if !["create", "blocked", "app_pending"].contains(state) {
                    let signedOut = ["signed_out", "auth_pending", "expired"].contains(state)
                    var facts = object["facts"] as! [[String: Any]]
                    let labels = ["robot": signedOut ? "登录后显示" : "示例机器人", "authorizedUser": signedOut ? "尚未绑定" : "示例用户 · 已绑定", "taskConnection": signedOut ? "等待飞书登录" : "正常", "application": "cli_visual_fixture"]
                    for i in facts.indices {
                        if let value = labels[facts[i]["id"] as! String] {
                            facts[i]["value"] = value
                            facts[i]["state"] = signedOut && facts[i]["id"] as! String != "application" ? "missing" : "present"
                        }
                    }
                    object["facts"] = facts
                    object["issues"] = []
                    var auth = object["auth"] as! [String: Any]
                    auth["identityValid"] = !signedOut; auth["profileValid"] = true
                    auth["status"] = signedOut ? "unauthorized" : "authorized"
                    object["auth"] = auth
                    object["diagnostics"] = ["serviceVersion": "0.11.0-preview.16", "cliVersion": "1.0.93-ksfassistant.1", "cliState": "ready", "permissionRevision": "visual", "selfTarget": signedOut ? "" : "fixture"]
                }
                var actions = object["actions"] as! [[String: Any]]
                if state == "blocked" {
                    for index in actions.indices {
                        if actions[index]["id"] as? String == "create_app" {
                            actions[index]["enabled"] = false
                            actions[index]["reason"] = "仍有活动飞书连接，请先完成注销清理。"
                        }
                        if actions[index]["id"] as? String == "logout" {
                            actions[index]["enabled"] = true
                            actions[index]["title"] = "清理旧连接数据"
                        }
                    }
                }
                for i in actions.indices {
                    if actions[i]["id"] as? String == "start_auth" { actions[i]["title"] = "登录飞书" }
                    if !["create", "blocked", "app_pending"].contains(state),
                       ["start_auth", "logout", "bind_operator", "test_message"].contains(actions[i]["id"] as! String) {
                        let signedOut = ["signed_out", "auth_pending", "expired"].contains(state)
                        actions[i]["enabled"] = signedOut ? actions[i]["id"] as! String == "start_auth" : ["logout", "test_message"].contains(actions[i]["id"] as! String)
                    }
                }
                if ["app_pending", "auth_pending", "completed", "expired"].contains(state) {
                    let app = state == "app_pending"
                    let flowState = ["expired", "completed"].contains(state) ? state : "pending"
                    var flow = ["id": "isolated-preview-flow", "kind": app ? "app" : "user",
                        "state": flowState, "expiresAt": "2099-01-01T00:00:00Z"]
                    if flowState == "pending" {
                        flow["qrDataURL"] = qrDataURL
                        flow["verificationURL"] = "https://example.invalid/visual-fixture"
                        flow["userCode"] = "VISUAL-ONLY"
                        for index in actions.indices {
                            actions[index]["enabled"] = ["cancel_flow", app ? "finish_app" : "finish_auth"].contains(actions[index]["id"] as! String)
                        }
                    } else if flowState == "completed" {
                        for index in actions.indices where actions[index]["id"] as? String == "finish_auth" {
                            actions[index]["enabled"] = true
                        }
                    }
                    object["flow"] = flow
                }
                if state == "refreshing" { object["refreshing"] = true }
                object["actions"] = actions
                let snapshot = try JSONDecoder().decode(FeishuConfigurationSnapshot.self, from: JSONSerialization.data(withJSONObject: object))
                let model = UsageViewModel(autoStart: false, cleanupLegacyWeChatData: false)
                try model.loadFeishuLayoutPreview(snapshot: ["loading", "unknown"].contains(state) ? nil : snapshot,
                    toolchain: state == "signed_out" ? ToolchainStatus(schemaVersion: 1, version: "1.0.93", installed: false, healthy: false, skills: [], problemCount: 0, installationState: "not_installed", installationTitle: "未安装", installationAction: "安装到 Codex", installationDetails: []) : toolchain, failed: ["unknown", "failed"].contains(state))
                let content = UsagePopoverView(viewModel: model, refreshOnAppear: false).feishuLayoutPreview(expandedSection: expandedSection)
                    .padding(12).frame(width: 336).fixedSize(horizontal: false, vertical: true)
                    .background(Color(nsColor: .windowBackgroundColor))
                    .environment(\.colorScheme, dark ? .dark : .light)
                let host = NSHostingView(rootView: content)
                host.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
                let size = host.fittingSize
                host.frame = NSRect(origin: .zero, size: size)
                let window = NSWindow(contentRect: host.frame, styleMask: .borderless, backing: .buffered, defer: false)
                window.isReleasedWhenClosed = false
                window.contentView = host
                RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.05))
                host.layoutSubtreeIfNeeded()
                let bounds = host.bounds
                guard let bitmap = host.bitmapImageRepForCachingDisplay(in: bounds) else { fatalError("Cannot render") }
                host.cacheDisplay(in: bounds, to: bitmap)
                let name = fixture + (dark ? "-dark" : "-light")
                precondition(size.width == 336 && size.height <= 565, "Preview exceeds its viewport")
                try bitmap.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: output).appendingPathComponent(name + ".png"))
                print("\(name): \(Int(size.width)) × \(Int(size.height))")
                for scroll in scrollViews(in: host) {
                    guard let document = scroll.documentView,
                          document.bounds.height > scroll.contentView.bounds.height else { continue }
                    let bottom = document.isFlipped ? document.bounds.height - scroll.contentView.bounds.height : 0
                    scroll.contentView.scroll(to: NSPoint(x: 0, y: bottom))
                    scroll.reflectScrolledClipView(scroll.contentView)
                    host.layoutSubtreeIfNeeded()
                    precondition(abs(scroll.contentView.bounds.origin.y - bottom) < 2, "Cannot reach overflow controls")
                    host.cacheDisplay(in: bounds, to: bitmap)
                    try bitmap.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: output).appendingPathComponent(name + "-bottom.png"))
                    print("\(name): overflow bottom reachable")
                }
                window.close()
            }
        }
    }

    @MainActor
    static func verifyConfigurationSurvivesWindowFocus(fixtureData: Data, toolchain: ToolchainStatus) throws {
        let model = UsageViewModel(autoStart: false, cleanupLegacyWeChatData: false)
        try model.loadFeishuLayoutPreview(snapshot: JSONDecoder().decode(FeishuConfigurationSnapshot.self, from: fixtureData), toolchain: toolchain, failed: false)
        let host = NSHostingView(rootView: UsagePopoverView(viewModel: model, refreshOnAppear: false).feishuFocusPreview())
        host.frame = NSRect(origin: .zero, size: host.fittingSize)
        let window = NSWindow(contentRect: host.frame, styleMask: .borderless, backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = host
        defer { window.close() }
        RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.1))
        func capture() -> Data {
            host.layoutSubtreeIfNeeded()
            let bitmap = host.bitmapImageRepForCachingDisplay(in: host.bounds)!
            host.cacheDisplay(in: host.bounds, to: bitmap)
            return bitmap.representation(using: .png, properties: [:])!
        }
        let before = capture()
        let confirmation = NSWindow(contentRect: .zero, styleMask: .titled, backing: .buffered, defer: false)
        confirmation.isReleasedWhenClosed = false
        defer { confirmation.close() }
        NotificationCenter.default.post(name: NSWindow.didBecomeKeyNotification, object: confirmation)
        RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.1))
        guard before == capture() else {
            throw NSError(domain: "FeishuFocusRegression", code: 1, userInfo: [NSLocalizedDescriptionKey: "Another window gaining focus reset the configuration page"])
        }
        NotificationCenter.default.post(name: NSWindow.didBecomeKeyNotification, object: window)
        RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.1))
        guard before == capture() else {
            throw NSError(domain: "FeishuFocusRegression", code: 2, userInfo: [NSLocalizedDescriptionKey: "Returning focus reset the configuration page"])
        }
        print("PASS: configuration survives confirmation and returning-window focus")
    }

    @MainActor
    static func scrollViews(in view: NSView) -> [NSScrollView] {
        (view as? NSScrollView).map { [$0] } ?? view.subviews.flatMap { scrollViews(in: $0) }
    }
}
