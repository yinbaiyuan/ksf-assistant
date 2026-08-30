import AppKit
import CodexUsageCore
import Foundation

@main
private enum TaskActivityLiveProbe {
    static func main() async {
        let socketURL = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".codex/ipc/ipc.sock")
        let client = CodexDesktopTaskActivityClient(
            transportFactory: { UnixSocketDesktopIPCTransport(socketURL: socketURL) },
            desktopIsRunning: {
                !NSRunningApplication.runningApplications(
                    withBundleIdentifier: "com.openai.codex"
                ).isEmpty
            }
        )
        let candidateIDs = Set(CommandLine.arguments.dropFirst())

        await client.start()
        if !candidateIDs.isEmpty {
            await client.reconcileLocalTaskCandidates(candidateIDs)
        }
        let box = SnapshotBox()
        let collector = Task {
            for await update in client.updates where update.availability != .loading {
                await box.store(update)
            }
        }
        try? await Task.sleep(nanoseconds: 1_500_000_000)
        collector.cancel()
        let snapshot = await box.value
        await client.stop()

        guard let snapshot else {
            fputs("FAIL desktop IPC did not produce a task snapshot\n", stderr)
            exit(1)
        }
        switch snapshot.availability {
        case .available, .desktopNotRunning:
            print("PASS desktop task IPC: running=\(snapshot.runningCount), waiting=\(snapshot.waitingCount), observed=\(snapshot.observations.count), availability=\(snapshot.availability.rawValue)")
        case .loading, .unsupportedProtocol, .offline:
            fputs("FAIL desktop task IPC availability: \(snapshot.availability.rawValue)\n", stderr)
            exit(1)
        }
    }
}

private actor SnapshotBox {
    private(set) var value: TaskActivitySnapshot?

    func store(_ snapshot: TaskActivitySnapshot) {
        value = snapshot
    }
}
