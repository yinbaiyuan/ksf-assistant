import SwiftUI

@main
private struct KSFAssistantPreviewApp: App {
    @StateObject private var viewModel = UsageViewModel(autoStart: false)

    var body: some Scene {
        WindowGroup("KSFAssistant Preview") {
            UsagePopoverView(viewModel: viewModel, refreshOnAppear: false)
        }
        .windowResizability(.contentSize)
    }
}
