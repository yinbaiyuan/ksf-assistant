import CodexUsageCore
import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

@main
private enum UsageViewModelStandaloneTestRunner {
    static func main() async {
        do {
            let fileManager = FileManager.default
            let root = fileManager.temporaryDirectory.appendingPathComponent("terminal-start-\(UUID().uuidString)")
            try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
            defer { try? fileManager.removeItem(at: root) }
            let script = root.appendingPathComponent(ProjectStartScriptResolver.fileName)
            try Data("#!/bin/zsh\nexit 0\n".utf8).write(to: script)
            try fileManager.setAttributes(
                [.posixPermissions: NSNumber(value: Int16(0o700))],
                ofItemAtPath: script.path
            )
            let action = ProjectLaunchAction(
                projectID: "project",
                scriptPath: script.path,
                workingDirectory: root.path
            )
            let command = try TerminalActionLauncher().commandSource(for: action)
            guard command.contains("cd -- '\(root.path)'") else {
                throw TestFailure(description: "Terminal command does not enter the project directory")
            }
            guard command.contains("'\(script.path)'") else {
                throw TestFailure(description: "Terminal command does not execute project start.sh")
            }

            try await MainActor.run {
                guard AppConfiguration.bundleIdentifier == "com.ksf.codexusagebar",
                      AppConfiguration.applicationSupportDirectory().lastPathComponent == "com.ksf.codexusagebar"
                else {
                    throw TestFailure(description: "team application identity is not isolated from the personal build")
                }
                let viewModel = UsageViewModel(autoStart: false, cleanupLegacyWeChatData: false)
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

                var calendar = Calendar(identifier: .gregorian)
                calendar.timeZone = TimeZone(secondsFromGMT: 0)!
                let now = Date(timeIntervalSince1970: 1_788_220_800) // 2026-09-01 UTC
                guard UsageViewModel.localHistoryRefreshDayCount(
                    cachedAt: now,
                    hasCachedDays: true,
                    now: now,
                    calendar: calendar
                ) == 1 else {
                    throw TestFailure(description: "same-day history cache should only refresh today")
                }
                guard UsageViewModel.localHistoryRefreshDayCount(
                    cachedAt: calendar.date(byAdding: .day, value: -2, to: now)!,
                    hasCachedDays: true,
                    now: now,
                    calendar: calendar
                ) == 3 else {
                    throw TestFailure(description: "history cache should refresh every day since its last update")
                }
                guard UsageViewModel.localHistoryRefreshDayCount(
                    cachedAt: .distantPast,
                    hasCachedDays: false,
                    now: now,
                    calendar: calendar
                ) == 30 else {
                    throw TestFailure(description: "an empty history cache should backfill 30 days")
                }
            }

            print("PASS team identity, project start, immediate task refresh, and incremental token history refresh")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
