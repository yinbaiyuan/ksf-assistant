import AppKit
import CoreGraphics
import KSFAssistantCore

@MainActor
private final class UserApprovalPanel: NSPanel {
    var cancelHandler: (() -> Void)?
    override func cancelOperation(_ sender: Any?) { cancelHandler?() }
}

@MainActor
final class UserApprovalController: NSObject, NSWindowDelegate {
    private let core: CoreServiceProcessClient
    private var polling: Task<Void, Never>?
    private var panel: NSPanel?
    private var active: UserApprovalRequest?
    private var suppressed: [String: Date] = [:]
    private var stopped = false
    private var unavailableReasons: Set<String> = []
    private var lastHeartbeat = Date.distantPast
    private var epoch = 0
    private var observers: [(NotificationCenter, NSObjectProtocol)] = []

    init(core: CoreServiceProcessClient) {
        self.core = core
        super.init()
    }

    func start() {
        guard polling == nil, !stopped else { return }
        observe(NSWorkspace.shared.notificationCenter, NSWorkspace.sessionDidResignActiveNotification, reason: "session", unavailable: true)
        observe(NSWorkspace.shared.notificationCenter, NSWorkspace.screensDidSleepNotification, reason: "screens", unavailable: true)
        observe(NSWorkspace.shared.notificationCenter, NSWorkspace.willSleepNotification, reason: "sleep", unavailable: true)
        observe(NSWorkspace.shared.notificationCenter, NSWorkspace.sessionDidBecomeActiveNotification, reason: "session", unavailable: false)
        observe(NSWorkspace.shared.notificationCenter, NSWorkspace.screensDidWakeNotification, reason: "screens", unavailable: false)
        observe(NSWorkspace.shared.notificationCenter, NSWorkspace.didWakeNotification, reason: "sleep", unavailable: false)
        observe(DistributedNotificationCenter.default(), Notification.Name("com.apple.screenIsLocked"), reason: "lock", unavailable: true)
        observe(DistributedNotificationCenter.default(), Notification.Name("com.apple.screenIsUnlocked"), reason: "lock", unavailable: false)
        polling = Task { [weak self] in
            while !Task.isCancelled {
                guard let self, !self.stopped else { return }
                await self.poll()
                try? await Task.sleep(nanoseconds: 500_000_000)
            }
        }
    }

    func stop() async {
        stopped = true
        epoch += 1
        polling?.cancel()
        polling = nil
        dismiss()
        for (center, token) in observers { center.removeObserver(token) }
        observers.removeAll()
        _ = try? await core.pollUserApproval(interactive: false)
    }

    private var interactive: Bool {
        guard !stopped, unavailableReasons.isEmpty, NSApp.isRunning, !NSApp.isHidden,
              NSScreen.screens.contains(where: { screen in
                  guard let identifier = screen.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber else { return false }
                  return CGDisplayIsAsleep(identifier.uint32Value) == 0
              }),
              NSApp.modalWindow == nil,
              let session = CGSessionCopyCurrentDictionary() as? [String: Any],
              session[kCGSessionOnConsoleKey as String] as? Bool == true,
              session[kCGSessionLoginDoneKey as String] as? Bool == true else { return false }
        return session["CGSSessionScreenIsLocked"] as? Bool != true
    }

    private func observe(_ center: NotificationCenter, _ name: Notification.Name, reason: String, unavailable: Bool) {
        let token = center.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in
            guard let controller = self else { return }
            Task { @MainActor [controller] in
                if unavailable {
                    controller.unavailableReasons.insert(reason)
                    controller.epoch += 1
                    controller.dismiss()
                    controller.lastHeartbeat = .distantPast
                    _ = try? await controller.core.pollUserApproval(interactive: false)
                } else {
                    controller.unavailableReasons.remove(reason)
                }
            }
        }
        observers.append((center, token))
    }

    private func poll() async {
        let wasInteractive = interactive
        let currentEpoch = epoch
        if !wasInteractive { dismiss() }
        do {
            let result = try await core.pollUserApproval(interactive: wasInteractive)
            guard !stopped, currentEpoch == epoch, wasInteractive, interactive else { dismiss(); return }
            lastHeartbeat = Date()
            suppressed = suppressed.filter { $0.value > Date() }
            guard let request = result.request else { dismiss(); return }
            if let active {
                if active != request {
                    suppressed[active.id] = active.expiration
                    dismiss()
                } else { return }
            }
            guard suppressed[request.id] == nil else { return }
            present(request)
        } catch {
            lastHeartbeat = .distantPast
            dismiss()
        }
    }

    private func present(_ request: UserApprovalRequest) {
        guard panel == nil, interactive else { return }
        let panel = UserApprovalPanel(contentRect: NSRect(x: 0, y: 0, width: 640, height: 530), styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        panel.cancelHandler = { [weak self] in self?.decide(false) }
        panel.title = "KSFAssistant · 用户身份操作批准"
        panel.isReleasedWhenClosed = false
        panel.hidesOnDeactivate = false
        panel.delegate = self
        panel.minSize = NSSize(width: 480, height: 360)
        guard let container = panel.contentView else { return }
        let title = NSTextField(wrappingLabelWithString: "本机受管工具请求以你的身份操作飞书\n批准仅限本次操作；请完整查看以下内容。")
        title.font = .boldSystemFont(ofSize: 13)
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        let text = NSTextView(frame: NSRect(x: 0, y: 0, width: 596, height: 360))
        text.isEditable = false
        text.isSelectable = true
        text.isRichText = false
        text.isAutomaticLinkDetectionEnabled = false
        text.font = .systemFont(ofSize: 12)
        text.string = request.details
        text.isVerticallyResizable = true
        text.isHorizontallyResizable = false
        text.autoresizingMask = [.width]
        text.textContainer?.widthTracksTextView = true
        text.textContainer?.containerSize = NSSize(width: 596, height: CGFloat.greatestFiniteMagnitude)
        scroll.documentView = text
        let reject = NSButton(title: "拒绝", target: self, action: #selector(rejectPressed))
        reject.keyEquivalent = "\r"
        let approve = NSButton(title: "批准本次操作", target: self, action: #selector(approvePressed))
        approve.keyEquivalent = ""
        for view in [title, scroll, reject, approve] {
            view.translatesAutoresizingMaskIntoConstraints = false
            container.addSubview(view)
        }
        NSLayoutConstraint.activate([
            title.topAnchor.constraint(equalTo: container.topAnchor, constant: 16),
            title.leadingAnchor.constraint(equalTo: container.leadingAnchor, constant: 16),
            title.trailingAnchor.constraint(equalTo: container.trailingAnchor, constant: -16),
            scroll.topAnchor.constraint(equalTo: title.bottomAnchor, constant: 12),
            scroll.leadingAnchor.constraint(equalTo: title.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: title.trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: reject.topAnchor, constant: -16),
            reject.bottomAnchor.constraint(equalTo: container.bottomAnchor, constant: -16),
            approve.centerYAnchor.constraint(equalTo: reject.centerYAnchor),
            approve.trailingAnchor.constraint(equalTo: container.trailingAnchor, constant: -16),
            reject.trailingAnchor.constraint(equalTo: approve.leadingAnchor, constant: -12)
        ])
        panel.defaultButtonCell = reject.cell as? NSButtonCell
        self.panel = panel
        active = request
        panel.center()
        NSApp.activate(ignoringOtherApps: true)
        panel.makeKeyAndOrderFront(nil)
        panel.makeFirstResponder(reject)
    }

    @objc private func rejectPressed() { decide(false) }
    @objc private func approvePressed() { decide(true) }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        decide(false)
        return false
    }

    private func decide(_ approve: Bool) {
        guard let request = active else { return }
        let allowed = interactive && Date().timeIntervalSince(lastHeartbeat) < 2 && (request.expiration ?? .distantPast) > Date()
        suppressed[request.id] = request.expiration
        dismiss()
        guard allowed else { return }
        Task {
            do {
                let accepted = try await core.decideUserApproval(id: request.id, approve: approve)
                if !accepted { NSSound.beep() }
            } catch {
                NSSound.beep()
            }
        }
    }

    private func dismiss() {
        active = nil
        panel?.delegate = nil
        panel?.orderOut(nil)
        panel?.close()
        panel = nil
    }
}
