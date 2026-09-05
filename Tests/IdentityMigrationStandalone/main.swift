import Foundation
import Darwin

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private final class IsolatedFileManager: FileManager, @unchecked Sendable {
    let root: URL
    var failCopyNamed: String?
    var beforeCopy: ((URL) throws -> Void)?

    init(root: URL) {
        self.root = root
        super.init()
    }

    override var homeDirectoryForCurrentUser: URL { root }

    override func urls(for directory: FileManager.SearchPathDirectory, in domainMask: FileManager.SearchPathDomainMask) -> [URL] {
        [root]
    }

    override func url(for directory: FileManager.SearchPathDirectory, in domain: FileManager.SearchPathDomainMask, appropriateFor url: URL?, create shouldCreate: Bool) throws -> URL {
        root
    }

    override func copyItem(at source: URL, to destination: URL) throws {
        try beforeCopy?(source)
        if source.lastPathComponent == failCopyNamed {
            throw TestFailure(description: "injected copy failure: \(source.lastPathComponent)")
        }
        try super.copyItem(at: source, to: destination)
    }
}

private final class IsolatedDefaults: UserDefaults, @unchecked Sendable {
    var failedSynchronizations = 0

    override func synchronize() -> Bool {
        if failedSynchronizations > 0 {
            failedSynchronizations -= 1
            return false
        }
        return super.synchronize()
    }
}

private final class Fixture {
    let manager: IsolatedFileManager
    let defaults: IsolatedDefaults
    let currentDomain = "test.ksfassistant.current.\(UUID().uuidString)"
    let legacyDomains = ["test.ksfassistant.first.\(UUID().uuidString)", "test.ksfassistant.second.\(UUID().uuidString)"]
    var current: URL { AppConfiguration.applicationSupportDirectory(fileManager: manager) }

    init() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("identity-migration-\(UUID().uuidString)")
        manager = IsolatedFileManager(root: root)
        guard let isolated = IsolatedDefaults(suiteName: currentDomain) else {
            throw TestFailure(description: "cannot create isolated defaults suite")
        }
        defaults = isolated
        try manager.createDirectory(at: root, withIntermediateDirectories: true)
    }

    func cleanup() throws {
        for domain in [currentDomain] + legacyDomains { defaults.removePersistentDomain(forName: domain) }
        guard defaults.synchronize() else { throw TestFailure(description: "fixture defaults cleanup failed") }
        try manager.removeItem(at: manager.root)
    }

    func migrate() throws {
        try AppConfiguration.migrateIdentity(defaults: defaults, currentDomain: currentDomain, legacyDomains: legacyDomains, fileManager: manager)
    }

    func write(_ path: String, _ content: String, identity: String = AppConfiguration.bundleIdentifier) throws {
        let file = manager.root.appendingPathComponent(identity).appendingPathComponent(path)
        try manager.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data(content.utf8).write(to: file)
    }

    func read(_ path: String, identity: String = AppConfiguration.bundleIdentifier) throws -> String {
        try String(contentsOf: manager.root.appendingPathComponent(identity).appendingPathComponent(path), encoding: .utf8)
    }

    func preferences() -> [String: Any] { defaults.persistentDomain(forName: currentDomain) ?? [:] }

    func expectNoStage() throws {
        let names = try manager.contentsOfDirectory(atPath: manager.root.path)
        try expect(!names.contains { $0.hasPrefix(".ksfassistant-migration-") }, "no abandoned staging directory")
    }
}

private func expect(_ condition: @autoclosure () throws -> Bool, _ message: String) throws {
    guard try condition() else { throw TestFailure(description: message) }
}

private func expectFailure(_ fragment: String, _ operation: () throws -> Void) throws {
    do { try operation() }
    catch {
        try expect(error.localizedDescription.contains(fragment), "unexpected failure: \(error.localizedDescription)")
        return
    }
    throw TestFailure(description: "expected failure containing: \(fragment)")
}

@main
private enum IdentityMigrationTests {
    static let first = "com.codexassistant.desktop"
    static let second = "com.ksf.codexusagebar"
    static let snapshot = "{\"buckets\":[],\"dailyUsageBuckets\":[]}"

    static func main() {
        do {
            try run()
        } catch {
            fputs("FAIL: \(error)\n", stderr)
            exit(1)
        }
    }

    static func test(_ name: String, _ body: (Fixture) throws -> Void) throws {
        let fixture = try Fixture()
        do { try body(fixture) }
        catch {
            try fixture.cleanup()
            throw error
        }
        try fixture.cleanup()
        print("PASS: \(name)")
    }

    static func run() throws {
        try test("path getter is pure; exact bundle identities") { fixture in
            try expect(AppConfiguration.bundleIdentifier == "com.ksfassistant.desktop", "new bundle ID")
            try expect(AppConfiguration.legacyBundleIdentifiers == [first, second], "ordered legacy identities")
            for identity in [first, second, "com.ksf.ksfassistant"] {
                try fixture.write("untouched", "source", identity: identity)
            }
            let destination = AppConfiguration.applicationSupportDirectory(fileManager: fixture.manager)
            try expect(!fixture.manager.fileExists(atPath: destination.path), "path lookup must not migrate or create application data")
            try expect(fixture.preferences().isEmpty, "getter must not touch preferences")
        }

        try test("first legacy wins; older fills gaps; unknown fields and source bytes retained") { fixture in
            let raw = "{\"buckets\":[],\"dailyUsageBuckets\":[],\"unknownFutureField\":{\"precise\":9007199254740993},\"version\":99}\n"
            try fixture.write("snapshot.json", raw, identity: first)
            try fixture.write("snapshot.json", "{}", identity: second)
            try fixture.write("nested/first", "first", identity: first)
            try fixture.write("nested/second", "second", identity: second)
            let projectUsage = "{\"project\":{\"cumulativeTokens\":42,\"todayTokens\":7,\"trackingStartedAt\":0,\"isComplete\":true,\"uncountedThreadCount\":0,\"future\":{\"tokens\":42}}}"
            try fixture.write("project-usage.json", projectUsage, identity: second)
            fixture.defaults.setPersistentDomain(["priority": "first", "future": ["nested": [1, 2]], "didMigrateKSFAssistantDefaults": true], forName: fixture.legacyDomains[0])
            fixture.defaults.setPersistentDomain(["priority": "second", "olderOnly": Data([1, 2, 3])], forName: fixture.legacyDomains[1])
            fixture.defaults.register(defaults: ["priority": "registered", "olderOnly": "registered"])
            try fixture.migrate()
            try expect(try fixture.read("snapshot.json") == raw, "unknown JSON fields and precision must retain original bytes")
            try expect(try fixture.read("snapshot.json", identity: first) == raw, "legacy source remains unchanged")
            try expect(try fixture.read("nested/first") == "first", "first nested file imported")
            try expect(try fixture.read("nested/second") == "second", "second nested gap filled")
            try expect(try fixture.read("project-usage.json") == projectUsage, "project usage unknown fields retained byte-for-byte")
            try expect(fixture.preferences()["priority"] as? String == "first", "newer legacy preferences win over older and registration")
            try expect(fixture.preferences()["future"] as? [String: [Int]] == ["nested": [1, 2]], "unknown preference fields retained")
            try expect(fixture.preferences()["olderOnly"] as? Data == Data([1, 2, 3]), "unknown binary preference retained")
            try expect(fixture.preferences()[AppConfiguration.identityMigrationKey] as? Bool == true, "new completion marker")
            try fixture.expectNoStage()
        }

        try test("existing current values win, including false, empty arrays and files") { fixture in
            try fixture.write("snapshot.json", "current bytes")
            try fixture.write("nested/current", "current")
            try fixture.write("snapshot.json", "not valid JSON", identity: first)
            try fixture.write("nested/missing", "imported", identity: first)
            let current: [String: Any] = ["launchAtLoginEnabled": false, "pinnedProjectIDs": [String](), "projectListOrder": ["unchanged"], "priority": "current"]
            fixture.defaults.setPersistentDomain(current, forName: fixture.currentDomain)
            fixture.defaults.setPersistentDomain(["launchAtLoginEnabled": true, "pinnedProjectIDs": ["old"], "projectListOrder": ["other"], "priority": "legacy", "gap": 42], forName: fixture.legacyDomains[0])
            try fixture.migrate()
            try expect(try fixture.read("snapshot.json") == "current bytes", "existing file is never overwritten or reparsed")
            try expect(try fixture.read("nested/current") == "current", "existing nested file unchanged")
            try expect(try fixture.read("nested/missing") == "imported", "missing nested file imported")
            for (key, value) in current {
                try expect(NSDictionary(dictionary: [key: fixture.preferences()[key] as Any]).isEqual(to: [key: value]), "current preference changed: \(key)")
            }
            try expect(fixture.preferences()["gap"] as? Int == 42, "missing current preference filled")
            try fixture.expectNoStage()
        }

        try test("current project identity aliases remap without importing older pin/order values") { fixture in
            let old = AppConfiguration.legacyProjectID
            let new = AppConfiguration.currentProjectID
            fixture.defaults.setPersistentDomain([
                "pinnedProjectIDs": ["first", new, old, "last"],
                "projectListOrder": [old, "middle", new, "middle", "last"],
                "unrelated": old,
                "didMigrateKSFAssistantDefaults": true,
            ], forName: fixture.currentDomain)
            fixture.defaults.setPersistentDomain(["pinnedProjectIDs": ["do-not-import"], "projectListOrder": ["do-not-import"]], forName: fixture.legacyDomains[0])
            try fixture.migrate()
            try expect(fixture.preferences()["pinnedProjectIDs"] as? [String] == ["first", new, "last"], "current pins remap and deduplicate only the identity alias")
            try expect(fixture.preferences()["projectListOrder"] as? [String] == [new, "middle", "middle", "last"], "current order remaps only the exact identity and preserves unrelated duplicates")
            try expect(fixture.preferences()["unrelated"] as? String == old, "unrelated current string unchanged")
        }

        try test("catalog-relative exact project ID remap and stable deduplication") { fixture in
            let old = "10项目/Codex Usage Bar/项目记忆卡.md"
            let new = "10项目/KSFAssistant/项目记忆卡.md"
            let unrelated = ["Codex Usage Bar", "/workspace/" + old, old + ".bak", "10项目/Other/项目记忆卡.md"]
            let identifiers = ["before", old, "middle", new, old] + unrelated
            fixture.defaults.setPersistentDomain(["pinnedProjectIDs": identifiers, "projectListOrder": identifiers, "unrelated": old], forName: fixture.legacyDomains[0])
            try fixture.migrate()
            let expected = ["before", new, "middle"] + unrelated
            try expect(fixture.preferences()["pinnedProjectIDs"] as? [String] == expected, "pins use exact catalog identity")
            try expect(fixture.preferences()["projectListOrder"] as? [String] == expected, "order retains first occurrence positions")
            try expect(fixture.preferences()["unrelated"] as? String == old, "no broad string replacement")
        }

        try test("oldest-only installation and idempotence without resurrecting deleted values") { fixture in
            try fixture.write("snapshot.json", snapshot, identity: second)
            fixture.defaults.setPersistentDomain(["oldestOnly": true], forName: fixture.legacyDomains[1])
            try fixture.migrate()
            try expect(try fixture.read("snapshot.json") == snapshot, "oldest path migrated")
            var preferences = fixture.preferences()
            preferences.removeValue(forKey: "oldestOnly")
            fixture.defaults.setPersistentDomain(preferences, forName: fixture.currentDomain)
            try fixture.manager.removeItem(at: fixture.current.appendingPathComponent("snapshot.json"))
            try fixture.migrate()
            try expect(fixture.preferences()["oldestOnly"] == nil, "deleted preference not resurrected")
            try expect(!fixture.manager.fileExists(atPath: fixture.current.appendingPathComponent("snapshot.json").path), "deleted file not resurrected")
        }

        try test("fresh install and unrelated Feishu data untouched") { fixture in
            try fixture.write("bridge.pid", "321", identity: ".config/feishu-bridge")
            try fixture.write("private", "untouched", identity: "com.ksf.ksfassistant")
            try fixture.migrate()
            try expect(!fixture.manager.fileExists(atPath: fixture.current.path), "no fabricated empty store directory")
            try expect(try fixture.read("bridge.pid", identity: ".config/feishu-bridge") == "321", "shared Feishu state untouched")
            try expect(try fixture.read("private", identity: "com.ksf.ksfassistant") == "untouched", "mechanically renamed fake legacy domain ignored")
            try fixture.expectNoStage()
        }

        for existing in [false, true] {
            try test("copy failure rolls back staged data, existing current=\(existing)") { fixture in
                if existing { try fixture.write("current", "untouched") }
                try fixture.write("a-first", "first", identity: first)
                try fixture.write("z-fail", "second", identity: first)
                fixture.defaults.setPersistentDomain(["keep": false], forName: fixture.currentDomain)
                fixture.defaults.setPersistentDomain(["imported": true], forName: fixture.legacyDomains[0])
                fixture.manager.failCopyNamed = "z-fail"
                fixture.manager.beforeCopy = { _ in
                    try expect(!fixture.manager.fileExists(atPath: fixture.current.appendingPathComponent("a-first").path), "no partial files visible before commit")
                }
                try expectFailure("已中止启动") { try fixture.migrate() }
                try expect(fixture.preferences().count == 1 && fixture.preferences()["keep"] as? Bool == false, "preferences unchanged on copy failure")
                try expect(!fixture.manager.fileExists(atPath: fixture.current.appendingPathComponent("a-first").path), "no partial destination")
                if existing { try expect(try fixture.read("current") == "untouched", "existing current unchanged") }
                else { try expect(!fixture.manager.fileExists(atPath: fixture.current.path), "failed new destination absent") }
                try fixture.expectNoStage()
                fixture.manager.failCopyNamed = nil
                fixture.manager.beforeCopy = nil
                try fixture.migrate()
                try expect(try fixture.read("z-fail") == "second", "retry completes after failed staging")
            }

            try test("defaults persistence failure restores filesystem, existing current=\(existing)") { fixture in
                if existing { try fixture.write("current", "untouched") }
                try fixture.write("imported", "legacy", identity: first)
                fixture.defaults.setPersistentDomain(["keep": true], forName: fixture.currentDomain)
                fixture.defaults.failedSynchronizations = 1
                try expectFailure("持久化失败") { try fixture.migrate() }
                try expect(fixture.preferences().count == 1 && fixture.preferences()["keep"] as? Bool == true, "preferences rolled back")
                try expect(!fixture.manager.fileExists(atPath: fixture.current.appendingPathComponent("imported").path), "published filesystem rolled back")
                if existing { try expect(try fixture.read("current") == "untouched", "original directory restored") }
                else { try expect(!fixture.manager.fileExists(atPath: fixture.current.path), "new directory removed on rollback") }
                try fixture.expectNoStage()
            }
        }

        try test("malformed imported JSON aborts instead of an empty-cache fallback") { fixture in
            try fixture.write("snapshot.json", "{broken", identity: first)
            try fixture.write("snapshot.json", "{}", identity: second)
            try expectFailure("已中止启动") { try fixture.migrate() }
            try expect(fixture.preferences().isEmpty, "malformed newer source must not fall back to older source")
            try expect(!fixture.manager.fileExists(atPath: fixture.current.path), "malformed JSON not published")
            try fixture.expectNoStage()
        }

        try test("invalid project preference aborts explicitly") { fixture in
            fixture.defaults.setPersistentDomain(["pinnedProjectIDs": "invalid"], forName: fixture.legacyDomains[0])
            try expectFailure("不是项目 ID 数组") { try fixture.migrate() }
            try expect(fixture.preferences().isEmpty, "invalid preferences not marked complete")
        }

        for filename in ["snapshot.json", "project-usage.json"] {
            try test("valid JSON with incompatible store schema aborts: \(filename)") { fixture in
                try fixture.write(filename, "{\"unexpected\":true}", identity: first)
                try expectFailure("迁移缓存无法读取") { try fixture.migrate() }
                try expect(fixture.preferences().isEmpty, "incompatible store not marked complete")
                try expect(!fixture.manager.fileExists(atPath: fixture.current.path), "incompatible store not published")
                try fixture.expectNoStage()
            }
        }

        try test("symlink sources are not followed") { fixture in
            try fixture.write("original", "external", identity: "external")
            try fixture.manager.createSymbolicLink(at: fixture.manager.root.appendingPathComponent(first), withDestinationURL: fixture.manager.root.appendingPathComponent("external"))
            try expectFailure("符号链接") { try fixture.migrate() }
            try expect(try fixture.read("original", identity: "external") == "external", "symlink target unchanged")
            try fixture.expectNoStage()
        }

        try test("symlink children abort without copying shared data") { fixture in
            try fixture.write("keep", "shared", identity: "shared-feishu")
            try fixture.write("a", "ordinary", identity: first)
            try fixture.manager.createSymbolicLink(at: fixture.manager.root.appendingPathComponent(first).appendingPathComponent("z-link"), withDestinationURL: fixture.manager.root.appendingPathComponent("shared-feishu"))
            try expectFailure("符号链接") { try fixture.migrate() }
            try expect(!fixture.manager.fileExists(atPath: fixture.current.path), "unsafe tree never published")
            try fixture.expectNoStage()
        }

        try test("destination publication collision preserves concurrent destination") { fixture in
            try fixture.write("imported", "legacy", identity: first)
            fixture.manager.beforeCopy = { _ in try fixture.write("concurrent", "keep") }
            try expectFailure("原子发布") { try fixture.migrate() }
            try expect(try fixture.read("concurrent") == "keep", "exclusive rename must not replace concurrent destination")
            try expect(fixture.preferences().isEmpty, "collision must not commit preferences")
            try fixture.expectNoStage()
        }

        try test("migration lock contention aborts without writes to stores") { fixture in
            let lock = fixture.manager.root.appendingPathComponent(".ksfassistant-identity-migration.lock")
            let descriptor = open(lock.path, O_CREAT | O_RDWR, mode_t(0o600))
            try expect(descriptor >= 0, "fixture lock open")
            defer { close(descriptor) }
            try expect(flock(descriptor, LOCK_EX | LOCK_NB) == 0, "fixture lock held")
            defer { flock(descriptor, LOCK_UN) }
            try expectFailure("另一个实例正在迁移") { try fixture.migrate() }
            try expect(fixture.preferences().isEmpty, "blocked migration writes no preferences")
            try expect(!fixture.manager.fileExists(atPath: fixture.current.path), "blocked migration creates no stores")
        }

        for identity in [first, second, AppConfiguration.bundleIdentifier] {
            try test("live bundle blocks before migration or services: \(identity)") { fixture in
                var initialized = false
                try expectFailure("请先关闭") {
                    try AppConfiguration.startupPreflight(
                        runningApplications: [.init(bundleIdentifier: identity, processIdentifier: 22)],
                        currentPID: 11,
                        migrate: { try fixture.migrate() }
                    )
                    initialized = true
                }
                try expect(!initialized && fixture.preferences().isEmpty, "live process blocks before migration and initialization")
                try expect(try fixture.manager.contentsOfDirectory(atPath: fixture.manager.root.path).isEmpty, "process conflict must not write any migration files")
            }
        }

        try test("current PID excluded; nil and unrelated bundles allowed") { fixture in
            var initialized = false
            try AppConfiguration.startupPreflight(
                runningApplications: [
                    .init(bundleIdentifier: AppConfiguration.bundleIdentifier, processIdentifier: 11),
                    .init(bundleIdentifier: nil, processIdentifier: 22),
                    .init(bundleIdentifier: "com.example.unrelated", processIdentifier: 33),
                ],
                currentPID: 11,
                migrate: { try fixture.migrate() }
            )
            initialized = true
            try expect(initialized && fixture.preferences()[AppConfiguration.identityMigrationKey] as? Bool == true, "startup proceeds only after successful preflight")
        }

        try test("migration error prevents service initialization") { fixture in
            var initialized = false
            try fixture.write("snapshot.json", "broken", identity: first)
            try expectFailure("已中止启动") {
                try AppConfiguration.startupPreflight(runningApplications: [], currentPID: 11, migrate: { try fixture.migrate() })
                initialized = true
            }
            try expect(!initialized, "startup must not continue after failure")
        }
        print("PASS: all identity migration tests (isolated suites and temporary directories only)")
    }
}
