import Foundation

enum CodexLocator {
    static func locate(fileManager: FileManager = .default) -> URL? {
        candidates(fileManager: fileManager).first { fileManager.isExecutableFile(atPath: $0.path) }
    }

    static func candidates(fileManager: FileManager = .default) -> [URL] {
        let home = fileManager.homeDirectoryForCurrentUser
        var paths = [
            home.appendingPathComponent(".local/bin/codex"),
            URL(fileURLWithPath: "/opt/homebrew/bin/codex"),
            URL(fileURLWithPath: "/usr/local/bin/codex"),
            URL(fileURLWithPath: "/Applications/ChatGPT.app/Contents/Resources/codex"),
        ]

        if let path = ProcessInfo.processInfo.environment["PATH"] {
            paths.append(contentsOf: path.split(separator: ":").map {
                URL(fileURLWithPath: String($0)).appendingPathComponent("codex")
            })
        }

        var seen = Set<String>()
        return paths.filter { seen.insert($0.standardizedFileURL.path).inserted }
    }

    static var searchDescription: String {
        candidates().map(\.path).joined(separator: "\n")
    }
}

