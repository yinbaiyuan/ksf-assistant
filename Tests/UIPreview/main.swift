import SwiftUI

@main
private struct CodexUsageBarPreviewApp: App {
    @StateObject private var viewModel = UsageViewModel(autoStart: false)

    var body: some Scene {
        WindowGroup("Codex Usage Bar Preview") {
            UsagePopoverView(viewModel: viewModel, refreshOnAppear: false)
        }
        .windowResizability(.contentSize)
    }
}
