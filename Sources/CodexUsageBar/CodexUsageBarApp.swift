import AppKit
import Combine
import SwiftUI

@main
struct CodexUsageBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    var body: some Scene {
        Settings {
            EmptyView()
        }
    }
}

@MainActor
private final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusController: StatusItemController?

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusController = StatusItemController()
    }
}

@MainActor
private final class StatusItemController: NSObject {
    private let viewModel = UsageViewModel()
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let weChatConnectionIndicator = StatusItemImageRenderer.makeConnectionIndicator()
    private let popover = NSPopover()
    private var viewModelObservation: AnyCancellable?
    private var lastPresentation: StatusItemPresentation?

    override init() {
        super.init()

        let contentView = UsagePopoverView(viewModel: viewModel)
        let hostingController = NSHostingController(rootView: contentView)
        hostingController.sizingOptions = [.preferredContentSize]
        hostingController.view.setFrameSize(NSSize(width: 336, height: 560))
        let fittingHeight = max(1, hostingController.view.fittingSize.height)
        popover.contentViewController = hostingController
        popover.contentSize = NSSize(width: 336, height: fittingHeight)
        popover.behavior = .transient
        popover.animates = false

        if let button = statusItem.button {
            button.target = self
            button.action = #selector(togglePopover(_:))
            button.sendAction(on: [.leftMouseUp])
            button.imagePosition = .imageOnly
            button.imageScaling = .scaleNone
            button.addSubview(weChatConnectionIndicator)
        }

        updateStatusItem()
        viewModelObservation = viewModel.objectWillChange.sink { [weak self] _ in
            DispatchQueue.main.async { self?.updateStatusItem() }
        }
    }

    @objc private func togglePopover(_ sender: NSStatusBarButton) {
        if popover.isShown {
            popover.performClose(sender)
        } else {
            popover.show(relativeTo: sender.bounds, of: sender, preferredEdge: .minY)
        }
    }

    private func updateStatusItem() {
        guard let button = statusItem.button else { return }
        let presentation = StatusItemPresentation(
            quotaText: viewModel.menuPercentageText,
            localTokenText: viewModel.menuLocalTodayTokenText,
            runningText: viewModel.runningTaskText,
            waitingText: viewModel.waitingTaskText,
            accessibilityLabel: viewModel.menuTitle,
            weChatConnected: viewModel.weChatState == .connected
        )
        guard presentation != lastPresentation else { return }

        let image = StatusItemImageRenderer.render(
            codexIcon: StatusItemImageRenderer.codexIcon(),
            quotaText: presentation.quotaText,
            localTokenText: presentation.localTokenText,
            runningText: presentation.runningText,
            waitingText: presentation.waitingText
        )
        image.isTemplate = true
        let targetLength = image.size.width + 6
        if abs(statusItem.length - targetLength) > 0.5 {
            statusItem.length = targetLength
        }
        button.image = image
        button.toolTip = presentation.accessibilityLabel
        button.setAccessibilityLabel(presentation.accessibilityLabel)
        weChatConnectionIndicator.isHidden = !presentation.weChatConnected
        lastPresentation = presentation
    }

    private struct StatusItemPresentation: Equatable {
        let quotaText: String
        let localTokenText: String
        let runningText: String
        let waitingText: String
        let accessibilityLabel: String
        let weChatConnected: Bool
    }
}
