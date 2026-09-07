import AppKit
import KSFAssistantCore

@MainActor
final class UserApprovalPanel: NSPanel {
    var cancelHandler: (() -> Void)?
    override func cancelOperation(_ sender: Any?) { cancelHandler?() }
    override func sendEvent(_ event: NSEvent) {
        if event.type == .keyDown && (event.keyCode == 36 || event.keyCode == 76 || event.keyCode == 53) {
            cancelHandler?()
        } else { super.sendEvent(event) }
    }
}

@MainActor
private final class ApprovalStackView: NSStackView {
    override var isFlipped: Bool { true }
}

/// Native presentation only. The controller and Core still own every decision.
@MainActor
final class UserApprovalContentView: NSView {
    let cancelButton = NSButton(title: "取消", target: nil, action: nil)
    let approveButton = NSButton(title: "执行", target: nil, action: nil)
    var onSizeChange: (() -> Void)?
    private let request: UserApprovalRequest
    private let decision: (Bool) -> Void
    private let document = ApprovalStackView()
    private let scroll = NSScrollView()
    private let detailButton = NSButton(title: "查看详情", target: nil, action: nil)
    private var detailsVisible = false
    private var contentExpanded = false
    private let contentWidth: CGFloat = 400
    private var documentHeight: CGFloat = 0
    private(set) var previewText: NSTextView?
    private(set) var detailsText: NSTextView?

    init(request: UserApprovalRequest, decision: @escaping (Bool) -> Void) {
        self.request = request
        self.decision = decision
        super.init(frame: NSRect(x: 0, y: 0, width: 440, height: 320))
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        document.orientation = .vertical
        document.alignment = .leading
        document.spacing = 12
        scroll.documentView = document
        scroll.translatesAutoresizingMaskIntoConstraints = false
        addSubview(scroll)

        cancelButton.target = self
        cancelButton.action = #selector(cancelPressed)
        cancelButton.keyEquivalent = ""
        approveButton.title = request.preview?.confirmLabel ?? "执行"
        approveButton.target = self
        approveButton.action = #selector(approvePressed)
        approveButton.keyEquivalent = ""
        for button in [cancelButton, approveButton] {
            button.bezelStyle = .rounded
            button.translatesAutoresizingMaskIntoConstraints = false
            addSubview(button)
        }
        approveButton.contentTintColor = request.preview?.destructive == true ? .systemRed : .controlAccentColor
        approveButton.font = .systemFont(ofSize: 13, weight: .semibold)
        approveButton.attributedTitle = NSAttributedString(string: approveButton.title, attributes: [
            .font: NSFont.systemFont(ofSize: 13, weight: .semibold),
            .foregroundColor: request.preview?.destructive == true ? NSColor.systemRed : NSColor.controlAccentColor,
        ])
        detailButton.target = self
        detailButton.action = #selector(toggleDetails)
        detailButton.bezelStyle = .inline
        detailButton.isBordered = false
        detailButton.contentTintColor = .secondaryLabelColor
        detailButton.setAccessibilityHelp("展开原始参数、附件校验信息和有效期；不会批准操作")
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: topAnchor, constant: 20),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 20),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -20),
            scroll.bottomAnchor.constraint(equalTo: cancelButton.topAnchor, constant: -20),
            cancelButton.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -16),
            approveButton.centerYAnchor.constraint(equalTo: cancelButton.centerYAnchor),
            approveButton.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -20),
            cancelButton.trailingAnchor.constraint(equalTo: approveButton.leadingAnchor, constant: -10),
        ])
        rebuild()
    }

    required init?(coder: NSCoder) { nil }

    override func draw(_ dirtyRect: NSRect) {
        NSColor.windowBackgroundColor.setFill()
        dirtyRect.fill()
    }

    func preferredSize(maximumHeight: CGFloat) -> NSSize {
        NSSize(width: 440, height: min(maximumHeight, documentHeight + 88))
    }

    private func textHeight(_ value: String, font: NSFont, width: CGFloat) -> CGFloat {
        let bounds = (value as NSString).boundingRect(with: NSSize(width: width, height: .greatestFiniteMagnitude),
            options: [.usesLineFragmentOrigin, .usesFontLeading], attributes: [.font: font])
        return ceil(bounds.height) + 4
    }

    private func label(_ value: String, font: NSFont = .systemFont(ofSize: 12), color: NSColor = .labelColor) -> NSView {
        let field = NSTextField(wrappingLabelWithString: value)
        field.font = font
        field.textColor = color
        field.isSelectable = true
        field.lineBreakMode = .byCharWrapping
        field.preferredMaxLayoutWidth = contentWidth
        field.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            field.widthAnchor.constraint(equalToConstant: contentWidth),
            field.heightAnchor.constraint(equalToConstant: textHeight(value, font: font, width: contentWidth)),
        ])
        return field
    }

    private func textBlock(_ value: String, maximumHeight: CGFloat) -> (NSScrollView, NSTextView) {
        let font = NSFont.systemFont(ofSize: 13)
        let box = NSScrollView()
        box.hasVerticalScroller = true
        box.autohidesScrollers = true
        box.borderType = .noBorder
        box.drawsBackground = false
        let text = NSTextView(frame: NSRect(x: 0, y: 0, width: contentWidth, height: 40))
        text.isEditable = false
        text.isSelectable = true
        text.isRichText = false
        text.isAutomaticLinkDetectionEnabled = false
        text.drawsBackground = false
        text.textColor = .labelColor
        text.font = font
        text.textContainerInset = NSSize(width: 0, height: 4)
        text.textContainer?.lineFragmentPadding = 0
        text.string = value
        text.isVerticallyResizable = true
        text.isHorizontallyResizable = false
        text.autoresizingMask = [.width]
        text.textContainer?.widthTracksTextView = true
        text.textContainer?.containerSize = NSSize(width: contentWidth, height: .greatestFiniteMagnitude)
        box.documentView = text
        box.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            box.widthAnchor.constraint(equalToConstant: contentWidth),
            box.heightAnchor.constraint(equalToConstant: min(maximumHeight, max(32, textHeight(value, font: font, width: contentWidth) + 12))),
        ])
        return (box, text)
    }

    private func rebuild() {
        for view in document.arrangedSubviews { document.removeArrangedSubview(view); view.removeFromSuperview() }
        document.addArrangedSubview(label(request.action, font: .systemFont(ofSize: 18, weight: .semibold)))
        document.addArrangedSubview(label("目标：\(request.target)"))
        document.addArrangedSubview(label("以\(request.user)的飞书身份 · 仅本次\n应用：\(request.application)", color: .secondaryLabelColor))
        // The broker owns this source text. Never turn an unverified caller into Codex.
        document.addArrangedSubview(label(request.source, font: .systemFont(ofSize: 12, weight: .medium)))

        let content = request.preview?.content ?? request.content
        if !content.isEmpty {
            let rule = NSBox()
            rule.boxType = .separator
            rule.translatesAutoresizingMaskIntoConstraints = false
            rule.widthAnchor.constraint(equalToConstant: contentWidth).isActive = true
            document.addArrangedSubview(rule)
            let (box, text) = textBlock(content, maximumHeight: contentExpanded ? 360 : 160)
            previewText = text
            text.setAccessibilityLabel("完整操作内容，可滚动查看")
            document.addArrangedSubview(box)
            if textHeight(content, font: .systemFont(ofSize: 13), width: contentWidth) > 160 {
                let expand = NSButton(title: contentExpanded ? "收起内容" : "展开内容", target: self, action: #selector(toggleContent))
                expand.bezelStyle = .inline
                expand.isBordered = false
                expand.contentTintColor = .controlAccentColor
                document.addArrangedSubview(expand)
            }
        }
        if !request.attachments.isEmpty {
            let names = request.attachments.map { attachment in
                "\(attachment.name) · \(ByteCountFormatter.string(fromByteCount: attachment.size, countStyle: .file))"
            }.joined(separator: "\n")
            document.addArrangedSubview(label("附件：\(names)", color: .secondaryLabelColor))
        }
        detailButton.title = detailsVisible ? "收起详情" : "查看详情"
        detailButton.image = NSImage(systemSymbolName: detailsVisible ? "chevron.down" : "chevron.right", accessibilityDescription: nil)
        detailButton.imagePosition = .imageLeading
        document.addArrangedSubview(detailButton)
        detailsText = nil
        if detailsVisible {
            let (box, text) = textBlock(request.details, maximumHeight: 220)
            detailsText = text
            text.setAccessibilityLabel("完整请求详情，可滚动查看")
            document.addArrangedSubview(box)
        }
        document.setFrameSize(NSSize(width: contentWidth, height: 1))
        document.layoutSubtreeIfNeeded()
        documentHeight = document.fittingSize.height
        document.setFrameSize(NSSize(width: contentWidth, height: documentHeight))
        needsLayout = true
    }

    @objc func toggleDetails() {
        detailsVisible.toggle()
        rebuild()
        onSizeChange?()
        window?.makeFirstResponder(detailButton)
    }

    @objc func toggleContent() {
        contentExpanded.toggle()
        rebuild()
        onSizeChange?()
    }

    @objc private func cancelPressed() { decision(false) }
    @objc private func approvePressed() { decision(true) }
}
