import Foundation

enum AppConfiguration {
    static let bundleIdentifier = "com.codexassistant.desktop"
    static let applicationSupportDirectoryName = bundleIdentifier
    static let appServerQueueLabel = bundleIdentifier + ".app-server.stdout"
    static let desktopIPCQueueLabel = bundleIdentifier + ".desktop-ipc.read"

    static func applicationSupportDirectory(fileManager: FileManager = .default) -> URL {
        let base = (try? fileManager.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? fileManager.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support")
        let current = base.appendingPathComponent(applicationSupportDirectoryName, isDirectory: true)
        let legacyIdentifier = ["com", "ksf", "codexusagebar"].joined(separator: ".")
        let legacy = base.appendingPathComponent(legacyIdentifier, isDirectory: true)
        if !fileManager.fileExists(atPath: current.path), fileManager.fileExists(atPath: legacy.path) {
            try? fileManager.copyItem(at: legacy, to: current)
        }
        return current
    }
}
