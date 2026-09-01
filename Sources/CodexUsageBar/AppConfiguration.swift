import Foundation

enum AppConfiguration {
    static let bundleIdentifier = "com.ksf.codexusagebar"
    static let applicationSupportDirectoryName = bundleIdentifier
    static let weChatKeychainService = bundleIdentifier + ".wechat"
    static let appServerQueueLabel = bundleIdentifier + ".app-server.stdout"
    static let desktopIPCQueueLabel = bundleIdentifier + ".desktop-ipc.read"

    static func suggestedKSFRoot(fileManager: FileManager = .default) -> String {
        let candidate = fileManager.homeDirectoryForCurrentUser
            .appendingPathComponent("Documents/KSF", isDirectory: true)
        var isDirectory: ObjCBool = false
        return fileManager.fileExists(atPath: candidate.path, isDirectory: &isDirectory)
            && isDirectory.boolValue
            ? candidate.path
            : ""
    }

    static func applicationSupportDirectory(fileManager: FileManager = .default) -> URL {
        let base = (try? fileManager.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )) ?? fileManager.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support")
        return base.appendingPathComponent(applicationSupportDirectoryName, isDirectory: true)
    }
}
