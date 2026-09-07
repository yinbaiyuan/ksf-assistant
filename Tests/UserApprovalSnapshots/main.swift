import AppKit
import KSFAssistantCore

@main
struct ApprovalSnapshots {
    @MainActor static func main() throws {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        let output = URL(fileURLWithPath: CommandLine.arguments[1])
        try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)
        for mode in ["light", "dark"] {
            for scenario in ["message", "long", "delete", "details", "scope"] {
                let raw: [String: Any] = [
                    "id": "fixture", "title": "飞书用户身份操作批准", "user": "示例用户", "application": "项目助手 / default",
                    "action": scenario == "delete" ? "删除日程（默认通知参会人）" : scenario == "scope" ? "更新文档权限" : "发送飞书消息",
                    "target": scenario == "scope" ? String(repeating: "示例文档路径 / ", count: 20) : scenario == "delete" ? "团队周会" : "项目协作群",
                    "content": "text: 今天的方案已更新，请大家查看。\nchat-id: oc_fixture\n附件 方案.pdf: 2400000 bytes; SHA256 \(String(repeating: "a", count: 64))",
                    "preview": ["content": scenario == "long" ? (1...100).map { "第\($0)项：完整操作内容，不截断或省略。" }.joined(separator: "\n") : scenario == "scope" ? "将向团队成员开放文档编辑权限。\n完整修改参数：{\"permission\":\"edit\"}" : scenario == "delete" ? "need-notification：true" : "今天的方案已更新，请大家查看。", "confirmLabel": scenario == "delete" ? "删除" : scenario == "scope" ? "更新" : "发送", "destructive": scenario == "delete"],
                    "attachments": scenario == "delete" ? [] : [["name": "方案.pdf", "size": 2400000, "sha256": String(repeating: "a", count: 64)]],
                    "source": "来源未验证", "expiresAt": ISO8601DateFormatter().string(from: Date().addingTimeInterval(120))
                ]
                let request = try JSONDecoder().decode(UserApprovalRequest.self, from: JSONSerialization.data(withJSONObject: raw))
                var decisions: [Bool] = []
                let content = UserApprovalContentView(request: request) { decisions.append($0) }
                let window = UserApprovalPanel(contentRect: NSRect(x: 0, y: 0, width: 440, height: 400), styleMask: [.titled], backing: .buffered, defer: false)
                window.cancelHandler = { decisions.append(false) }
                window.appearance = NSAppearance(named: mode == "dark" ? .darkAqua : .aqua)
                window.contentView = content
                if scenario == "details" { content.toggleDetails() }
                if scenario == "long" { content.toggleContent() }
                window.setContentSize(content.preferredSize(maximumHeight: 620))
                window.setFrameOrigin(NSPoint(x: 40, y: 80))
                window.orderFront(nil)
                content.layoutSubtreeIfNeeded()
                RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.08))
                window.displayIfNeeded()
                let outer = content.subviews.compactMap { $0 as? NSScrollView }.first!
                precondition(outer.documentView!.isFlipped && outer.contentView.bounds.minY == 0, "Review must open at its heading")
                precondition(decisions.isEmpty, "Opening and disclosure must never decide")
                if let preview = request.preview, !preview.content.isEmpty { precondition(content.previewText?.string == preview.content) }
                if scenario == "details" { precondition(content.detailsText?.string == request.details) }
                precondition(content.bounds.contains(content.cancelButton.frame) && content.bounds.contains(content.approveButton.frame), "Buttons must remain reachable")
                precondition(content.approveButton.keyEquivalent.isEmpty && content.cancelButton.keyEquivalent.isEmpty, "No Return shortcut may approve")
                guard let bitmap = content.bitmapImageRepForCachingDisplay(in: content.bounds) else { fatalError("No bitmap") }
                content.cacheDisplay(in: content.bounds, to: bitmap)
                try bitmap.representation(using: .png, properties: [:])!.write(to: output.appendingPathComponent("\(scenario)-\(mode).png"))
                content.cancelButton.performClick(nil)
                content.approveButton.performClick(nil)
                precondition(decisions == [false, true], "Native actions must forward exact decisions")
                decisions = []
                window.makeFirstResponder(content.previewText ?? content.cancelButton)
                for code: UInt16 in [36, 76, 53] {
                    let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: window.windowNumber, context: nil, characters: "\r", charactersIgnoringModifiers: "\r", isARepeat: false, keyCode: code)!
                    window.sendEvent(event)
                }
                precondition(decisions == [false, false, false], "Return, keypad Enter and Escape must cancel from the preview")
                print("PASS \(scenario)-\(mode) \(Int(content.bounds.width))×\(Int(content.bounds.height)) content/disclosure/buttons")
                window.orderOut(nil)
            }
        }
    }
}
