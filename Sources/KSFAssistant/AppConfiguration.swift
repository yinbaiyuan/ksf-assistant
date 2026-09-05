import Foundation
import Darwin
import KSFAssistantCore

enum AppConfiguration {
    static let bundleIdentifier = "com.ksfassistant.desktop"
    static let applicationSupportDirectoryName = bundleIdentifier
    static let legacyBundleIdentifiers = ["com.codexassistant.desktop", "com.ksf.codexusagebar"]
    static let identityMigrationKey = "didMigrateKSFAssistantIdentityV1"
    static let legacyProjectID = "10项目/Codex Usage Bar/项目记忆卡.md"
    static let currentProjectID = "10项目/KSFAssistant/项目记忆卡.md"

    struct RunningApplication {
        let bundleIdentifier: String?
        let processIdentifier: Int32
    }

    struct StartupError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    static func applicationSupportDirectory(fileManager: FileManager = .default) -> URL {
        let base = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? fileManager.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support")
        return base.appendingPathComponent(applicationSupportDirectoryName, isDirectory: true)
    }

    static func startupPreflight(
        runningApplications: [RunningApplication],
        currentPID: Int32,
        migrate: () throws -> Void = { try migrateIdentity() }
    ) throws {
        let conflicts = runningApplications.filter {
            $0.processIdentifier != currentPID
                && ($0.bundleIdentifier == bundleIdentifier || legacyBundleIdentifiers.contains($0.bundleIdentifier ?? ""))
        }
        guard conflicts.isEmpty else {
            let identities = conflicts.map { "\($0.bundleIdentifier ?? "") (PID \($0.processIdentifier))" }.joined(separator: "、")
            throw StartupError(message: "请先关闭旧版或已运行的 KSFAssistant，再重新启动：\(identities)。未迁移数据，也未终止任何现有进程。此检查不能防止旧版随后启动；请同时关闭其他目录中独立运行的 Node 飞书桥。")
        }
        try migrate()
    }

    static func migrateIdentity(
        defaults: UserDefaults = .standard,
        currentDomain: String = bundleIdentifier,
        legacyDomains: [String] = legacyBundleIdentifiers,
        fileManager: FileManager = .default
    ) throws {
        let current = applicationSupportDirectory(fileManager: fileManager)
        let base = current.deletingLastPathComponent()
        try fileManager.createDirectory(at: base, withIntermediateDirectories: true)
        try requireDirectory(base, fileManager: fileManager)
        let lockURL = base.appendingPathComponent(".ksfassistant-identity-migration.lock")
        let descriptor = open(lockURL.path, O_CREAT | O_RDWR | O_NOFOLLOW, mode_t(0o600))
        guard descriptor >= 0 else { throw posixFailure("无法打开迁移锁", path: lockURL.path) }
        defer { close(descriptor) }
        guard flock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            throw posixFailure("另一个实例正在迁移，请关闭后重试", path: lockURL.path)
        }
        defer { flock(descriptor, LOCK_UN) }

        let original = defaults.persistentDomain(forName: currentDomain)
        let currentPreferences = original ?? [:]
        if currentPreferences[identityMigrationKey] as? Bool == true { return }
        var preferences = currentPreferences
        for domain in legacyDomains {
            for (key, value) in defaults.persistentDomain(forName: domain) ?? [:] where preferences[key] == nil {
                if key == "pinnedProjectIDs" || key == "projectListOrder" {
                    guard let identifiers = value as? [String] else {
                        throw StartupError(message: "迁移失败：\(domain) 的 \(key) 不是项目 ID 数组。原数据未修改。")
                    }
                    preferences[key] = identifiers
                } else {
                    preferences[key] = value
                }
            }
        }
        for key in ["pinnedProjectIDs", "projectListOrder"] {
            if let identifiers = preferences[key] as? [String], identifiers.contains(legacyProjectID) {
                var seen = Set<String>()
                preferences[key] = identifiers.map { $0 == legacyProjectID ? currentProjectID : $0 }
                    .filter { $0 != currentProjectID || seen.insert($0).inserted }
            }
        }
        preferences[identityMigrationKey] = true

        let stage = base.appendingPathComponent(".ksfassistant-migration-\(UUID().uuidString)", isDirectory: true)
        var staged = false
        var published = false
        var preferencesWritten = false
        let hadCurrent = try itemType(current, fileManager: fileManager) != nil
        var changed = false
        do {
            try fileManager.createDirectory(at: stage, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
            staged = true
            if hadCurrent {
                try requireDirectory(current, fileManager: fileManager)
                _ = try mergeDirectory(current, into: stage, validateImportedJSON: false, fileManager: fileManager)
            }
            for identifier in legacyBundleIdentifiers {
                let source = base.appendingPathComponent(identifier, isDirectory: true)
                if try itemType(source, fileManager: fileManager) != nil {
                    try requireDirectory(source, fileManager: fileManager)
                    let imported = try mergeDirectory(source, into: stage, validateImportedJSON: true, fileManager: fileManager)
                    changed = changed || imported
                }
            }
            if changed {
                try rename(stage, to: current, flags: hadCurrent ? UInt32(RENAME_SWAP) : UInt32(RENAME_EXCL))
                published = true
                staged = hadCurrent
            }
            defaults.setPersistentDomain(preferences, forName: currentDomain)
            preferencesWritten = true
            guard defaults.synchronize() else {
                throw StartupError(message: "迁移偏好设置持久化失败。")
            }
        } catch {
            var recoveryErrors: [String] = []
            if preferencesWritten {
                if let original {
                    defaults.setPersistentDomain(original, forName: currentDomain)
                } else {
                    defaults.removePersistentDomain(forName: currentDomain)
                }
                if !defaults.synchronize() { recoveryErrors.append("偏好设置回滚持久化失败") }
            }
            if published {
                do {
                    try rename(current, to: stage, flags: hadCurrent ? UInt32(RENAME_SWAP) : UInt32(RENAME_EXCL))
                    staged = true
                } catch {
                    recoveryErrors.append("文件回滚失败：\(error.localizedDescription)；恢复副本：\(stage.path)")
                    staged = false
                }
            }
            if staged {
                do { try fileManager.removeItem(at: stage) }
                catch { recoveryErrors.append("暂存清理失败：\(stage.path)：\(error.localizedDescription)") }
            }
            throw StartupError(message: "KSFAssistant 身份迁移失败，已中止启动：\(error.localizedDescription)\(recoveryErrors.isEmpty ? "" : "；" + recoveryErrors.joined(separator: "；"))")
        }
        if staged {
            do { try fileManager.removeItem(at: stage) }
            catch { throw StartupError(message: "迁移已提交，但暂存清理失败；已中止启动：\(stage.path)：\(error.localizedDescription)") }
        }
    }

    private static func itemType(_ url: URL, fileManager: FileManager) throws -> FileAttributeType? {
        do {
            let attributes = try fileManager.attributesOfItem(atPath: url.path)
            guard let type = attributes[.type] as? FileAttributeType else {
                throw StartupError(message: "无法确定文件类型：\(url.path)")
            }
            return type
        } catch let error as NSError where error.domain == NSCocoaErrorDomain
            && (error.code == NSFileNoSuchFileError || error.code == NSFileReadNoSuchFileError) {
            return nil
        }
    }

    private static func requireDirectory(_ url: URL, fileManager: FileManager) throws {
        guard try itemType(url, fileManager: fileManager) == .typeDirectory else {
            throw StartupError(message: "迁移路径不是普通目录（不允许符号链接）：\(url.path)")
        }
    }

    private static func mergeDirectory(
        _ source: URL,
        into destination: URL,
        validateImportedJSON: Bool,
        fileManager: FileManager
    ) throws -> Bool {
        var changed = false
        for child in try fileManager.contentsOfDirectory(at: source, includingPropertiesForKeys: nil).sorted(by: { $0.path < $1.path }) {
            let target = destination.appendingPathComponent(child.lastPathComponent)
            let targetType = try itemType(target, fileManager: fileManager)
            let sourceType = try itemType(child, fileManager: fileManager)
            if targetType != nil {
                if targetType == .typeDirectory && sourceType == .typeDirectory {
                    let imported = try mergeDirectory(child, into: target, validateImportedJSON: validateImportedJSON, fileManager: fileManager)
                    changed = changed || imported
                }
                continue
            }
            switch sourceType {
            case .typeDirectory:
                try fileManager.createDirectory(at: target, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
                _ = try mergeDirectory(child, into: target, validateImportedJSON: validateImportedJSON, fileManager: fileManager)
            case .typeRegular:
                if validateImportedJSON {
                    do {
                        switch child.lastPathComponent {
                        case "snapshot.json":
                            _ = try JSONDecoder().decode(UsageSnapshot.self, from: Data(contentsOf: child))
                        case "project-usage.json":
                            _ = try JSONDecoder().decode([String: ProjectUsageSummary].self, from: Data(contentsOf: child))
                        default:
                            break
                        }
                    } catch {
                        throw StartupError(message: "迁移缓存无法读取：\(child.path)：\(error.localizedDescription)")
                    }
                }
                try fileManager.copyItem(at: child, to: target)
            default:
                throw StartupError(message: "迁移拒绝符号链接或特殊文件：\(child.path)")
            }
            changed = true
        }
        return changed
    }

    private static func rename(_ source: URL, to destination: URL, flags: UInt32) throws {
        guard renamex_np(source.path, destination.path, flags) == 0 else {
            throw posixFailure("无法原子发布或回滚迁移", path: destination.path)
        }
    }

    private static func posixFailure(_ action: String, path: String) -> StartupError {
        let code = errno
        return StartupError(message: "\(action)：\(path) (errno \(code): \(String(cString: strerror(code))))")
    }
}
