import CodexUsageCore
import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

@main
private enum TaskOpeningTestRunner {
    static func main() {
        do {
            var openedURL: URL?
            let opener = WorkspaceCodexTaskOpener { url in
                openedURL = url
                return true
            }
            try opener.openTask(id: "task/with space")
            guard openedURL?.absoluteString == "codex://threads/task%2Fwith%20space" else {
                throw TestFailure(description: "task opener did not use the safely encoded Codex deep link")
            }

            let rejected = WorkspaceCodexTaskOpener { _ in false }
            do {
                try rejected.openTask(id: "task")
                throw TestFailure(description: "task opener ignored an NSWorkspace rejection")
            } catch CodexTaskOpeningError.applicationRejectedURL {
                // Expected.
            }

            print("PASS Codex task deep-link opening and local failure mapping")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
