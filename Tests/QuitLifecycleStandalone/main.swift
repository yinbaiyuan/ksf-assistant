import AppKit
import KSFAssistantCore

// Exercise the real quit action against AppKit's deferred-termination protocol.
// No Core, Codex, Feishu, or account connection is started in this process.
@MainActor
private final class QuitDelegate: NSObject, NSApplicationDelegate {
    let model = UsageViewModel(autoStart: false, cleanupLegacyWeChatData: false)
    var shuttingDown = false
    var ready = false
    var cleanupCount = 0

    func applicationDidFinishLaunching(_ notification: Notification) {
        fputs("launch completed\n", stderr)
        // System Quit is delivered as an AppKit event, not inside a dispatch
        // block (which itself would hold the main queue during a modal loop).
        perform(#selector(requestQuit), with: nil, afterDelay: 0)
    }

    @objc private func requestQuit() {
        if CommandLine.arguments.contains("--system") {
            NSApplication.shared.terminate(nil)
        } else {
            model.quit()
            if CommandLine.arguments.contains("--repeat") { model.quit() }
        }
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        fputs("termination requested\n", stderr)
        if ready { return .terminateNow }
        if shuttingDown { return .terminateLater }
        shuttingDown = true
        // Reentrant system requests must not begin another cleanup.
        precondition(applicationShouldTerminate(sender) == .terminateLater)
        Task { @MainActor in
            fputs("cleanup started\n", stderr)
            await model.shutdown()
            cleanupCount += 1
            ready = true
            fputs("cleanup completed\n", stderr)
            sender.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }

    func applicationWillTerminate(_ notification: Notification) {
        guard ready, cleanupCount == 1 else { exit(3) }
        print("PASS: AppKit termination completed after one cleanup.")
    }
}

@main
private enum QuitLifecycleTest {
    @MainActor static func main() {
        // A stuck MainActor must not prevent the test from failing on time.
        DispatchQueue.global().asyncAfter(deadline: .now() + 6) {
            fputs("FAIL: quit did not finish within 6 seconds\n", stderr)
            exit(2)
        }
        let app = NSApplication.shared
        app.setActivationPolicy(.prohibited)
        let delegate = QuitDelegate()
        app.delegate = delegate
        withExtendedLifetime(delegate) { app.run() }
    }
}
