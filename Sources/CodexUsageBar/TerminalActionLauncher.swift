import AppKit
import CodexUsageCore
import Foundation

enum TerminalActionError: Error, LocalizedError {
    case invalidAction
    case couldNotOpenTerminal

    var errorDescription: String? {
        switch self {
        case .invalidAction:
            return "启动动作包含无法安全执行的字段。"
        case .couldNotOpenTerminal:
            return "无法打开 Terminal。"
        }
    }
}

struct TerminalActionLauncher {
    func launch(_ action: ProjectLaunchAction) throws {
        guard
            isSafe(action.executable),
            action.arguments.allSatisfy(isSafe),
            isSafe(action.workingDirectory)
        else { throw TerminalActionError.invalidAction }

        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("CodexUsageBar-Actions", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let script = directory.appendingPathComponent("\(UUID().uuidString).command")
        let command = ([action.executable] + action.arguments).map(shellQuote).joined(separator: " ")
        let source = """
        #!/bin/zsh
        rm -f -- "$0"
        cd -- \(shellQuote(action.workingDirectory))
        \(command)
        action_status=$?
        echo
        echo "[Codex Usage Bar] 命令已结束，状态：$action_status"
        exec /bin/zsh -l
        """
        try Data(source.utf8).write(to: script, options: .atomic)
        try FileManager.default.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o700))],
            ofItemAtPath: script.path
        )
        guard NSWorkspace.shared.open(script) else {
            throw TerminalActionError.couldNotOpenTerminal
        }
    }

    private func shellQuote(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    private func isSafe(_ value: String) -> Bool {
        !value.isEmpty && value.unicodeScalars.allSatisfy {
            !CharacterSet.controlCharacters.contains($0)
        }
    }
}
