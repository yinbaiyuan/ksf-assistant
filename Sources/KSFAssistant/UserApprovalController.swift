import AppKit
import CoreGraphics
import KSFAssistantCore

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

    var allowsConfigurationSubmission: Bool {
        interactive && panel == nil && active == nil
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
        let panel = UserApprovalPanel(contentRect: NSRect(x: 0, y: 0, width: 440, height: 320), styleMask: [.titled, .closable], backing: .buffered, defer: false)
        panel.cancelHandler = { [weak self] in self?.decide(false) }
        panel.title = "KSFAssistant · 飞书操作"
        panel.isReleasedWhenClosed = false
        panel.hidesOnDeactivate = false
        panel.delegate = self
        let content = UserApprovalContentView(request: request) { [weak self] approve in self?.decide(approve) }
        panel.contentView = content
        let maximumHeight = max(240, (NSScreen.main?.visibleFrame.height ?? 800) - 100)
        content.onSizeChange = { [weak panel, weak content] in
            guard let panel, let content else { return }
            let top = panel.frame.maxY
            panel.setContentSize(content.preferredSize(maximumHeight: maximumHeight))
            panel.setFrameOrigin(NSPoint(x: panel.frame.minX, y: top - panel.frame.height))
        }
        panel.setContentSize(content.preferredSize(maximumHeight: maximumHeight))
        panel.defaultButtonCell = nil
        self.panel = panel
        active = request
        panel.center()
        NSApp.activate(ignoringOtherApps: true)
        panel.makeKeyAndOrderFront(nil)
        panel.makeFirstResponder(content.cancelButton)
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
