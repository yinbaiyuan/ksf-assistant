import AppKit
import KSFAssistantCore
import Foundation

enum TerminalActionError: Error, LocalizedError {
    case couldNotOpenTerminal

    var errorDescription: String? {
        switch self {
        case .couldNotOpenTerminal:
            return "无法打开 Terminal。"
        }
    }
}

struct TerminalActionLauncher {
    func launch(_ action: ProjectLaunchAction) throws {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("KSFAssistant-Actions", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let script = directory.appendingPathComponent("\(UUID().uuidString).command")
        try Data(commandSource(for: action).utf8).write(to: script, options: .atomic)
        try FileManager.default.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o700))],
            ofItemAtPath: script.path
        )
        guard NSWorkspace.shared.open(script) else {
            throw TerminalActionError.couldNotOpenTerminal
        }
    }

    func commandSource(for action: ProjectLaunchAction) throws -> String {
        try ProjectStartScriptResolver().validate(action)
        return """
        #!/bin/zsh
        rm -f -- "$0"
        cd -- \(shellQuote(action.workingDirectory))
        \(shellQuote(action.scriptPath))
        action_status=$?
        echo
        echo "[KSFAssistant] 命令已结束，状态：$action_status"
        exec /bin/zsh -l
        """
    }

    private func shellQuote(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}
