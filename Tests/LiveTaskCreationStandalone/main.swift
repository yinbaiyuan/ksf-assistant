import AppKit
import CodexUsageCore
import Foundation

private struct SmokeFailure: Error {}

/// Opt-in live smoke runner. It creates a real Codex task and is intentionally
/// excluded from the regular test script.
@main
private enum LiveTaskCreationSmokeRunner {
    static func main() async {
        do {
            guard CommandLine.arguments.count == 4 else {
                fputs("usage: live-task-creation-smoke <ksf-root> <project-name> <project-card>\n", stderr)
                exit(64)
            }
            guard let executable = CodexLocator.locate() else {
                throw SmokeFailure()
            }

            let root = URL(fileURLWithPath: CommandLine.arguments[1]).standardizedFileURL.path
            let projectName = CommandLine.arguments[2]
            let projectCard = URL(fileURLWithPath: CommandLine.arguments[3]).standardizedFileURL.path
            let client = CodexAppServerClient {
                ProcessAppServerTransport(executableURL: executable)
            }
            let threadID = try await client.createDraftThread(
                cwd: root,
                name: "\(projectName) · 新任务（验收）"
            )
            let socketURL = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent(".codex/ipc/ipc.sock")
            let submitter = CodexDesktopTaskSubmissionClient {
                UnixSocketDesktopIPCTransport(socketURL: socketURL)
            }
            let prompt = """
            这是 KSF 项目「\(projectName)」的新任务。
            项目记忆卡：\(projectCard)

            请按 KSF 规范加载该项目的基础上下文。本轮只做上下文准备：不要开始具体工作，不要修改文件，不要生成实施方案。完成后简短说明已就绪，并等待用户下一步指令。
            """
            try await submitter.submitInitialTurn(
                threadID: threadID,
                cwd: root,
                prompt: prompt,
                openTask: { @MainActor in
                    guard let url = URL(string: "codex://threads/\(threadID)"),
                          NSWorkspace.shared.open(url) else {
                        throw SmokeFailure()
                    }
                }
            )
            await client.stop()
            print(threadID)
        } catch {
            fputs("FAIL \(error.localizedDescription)\n", stderr)
            exit(1)
        }
    }
}
