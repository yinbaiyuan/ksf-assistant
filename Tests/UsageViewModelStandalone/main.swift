import CodexUsageCore
import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

@main
private enum UsageViewModelStandaloneTestRunner {
    static func main() async {
        do {
            try await MainActor.run {
                let viewModel = UsageViewModel(autoStart: false)
                guard viewModel.projectDashboard.availability == .loading else {
                    throw TestFailure(description: "expected the initial project dashboard to be loading")
                }

                viewModel.applyTaskActivityUpdate(TaskActivitySnapshot(availability: .offline))
                guard let previousObservedAt = viewModel.projectDashboard.observedAt else {
                    throw TestFailure(description: "failed to prepare a cached project dashboard")
                }
                Thread.sleep(forTimeInterval: 0.01)

                let update = TaskActivitySnapshot(availability: .available)
                viewModel.applyTaskActivityUpdate(update)

                guard viewModel.taskActivity == update else {
                    throw TestFailure(description: "task activity was not applied synchronously")
                }
                guard viewModel.projectDashboard.observedAt ?? .distantPast > previousObservedAt else {
                    throw TestFailure(
                        description: "available task activity did not refresh the cached project dashboard immediately"
                    )
                }
            }

            print("PASS available task activity refreshes the cached project dashboard immediately")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
