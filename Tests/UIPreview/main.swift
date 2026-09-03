import SwiftUI

@main
private struct CodexUsageBarPreviewApp: App {
    @StateObject private var viewModel = UsageViewModel(autoStart: false)

    var body: some Scene {
        WindowGroup("CodexAssistant Preview") {
            UsagePopoverView(viewModel: viewModel, refreshOnAppear: false)
        }
        .windowResizability(.contentSize)
    }
}
