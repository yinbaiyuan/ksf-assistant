import Foundation
import Security

enum LegacyWeChatDataCleanerError: Error, LocalizedError {
    case unsafeStateFile
    case keychain(OSStatus)

    var errorDescription: String? {
        switch self {
        case .unsafeStateFile: return "旧微信状态文件不是安全的普通文件，未自动删除。"
        case let .keychain(status): return "旧微信安全存储清理失败（\(status)）。"
        }
    }
}

struct LegacyWeChatDataCleaner {
    private let fileManager: FileManager

    init(fileManager: FileManager = .default) {
        self.fileManager = fileManager
    }

    func clean() throws {
        let service = AppConfiguration.bundleIdentifier + ".wechat"
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
        ]
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw LegacyWeChatDataCleanerError.keychain(status)
        }

        let stateURL = AppConfiguration.applicationSupportDirectory(fileManager: fileManager)
            .appendingPathComponent("wechat-state-v1.enc")
        guard fileManager.fileExists(atPath: stateURL.path) else { return }
        let values = try stateURL.resourceValues(forKeys: [.isRegularFileKey, .isSymbolicLinkKey])
        guard values.isRegularFile == true, values.isSymbolicLink != true else {
            throw LegacyWeChatDataCleanerError.unsafeStateFile
        }
        try fileManager.removeItem(at: stateURL)
    }
}
