import CodexUsageCore
import Foundation

struct ProjectUsageStore {
    private let fileURL: URL

    init(fileManager: FileManager = .default) {
        let directory = AppConfiguration.applicationSupportDirectory(fileManager: fileManager)
        try? fileManager.createDirectory(at: directory, withIntermediateDirectories: true)
        try? fileManager.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o700))],
            ofItemAtPath: directory.path
        )
        fileURL = directory.appendingPathComponent("project-usage.json")
    }

    func load() -> [String: ProjectUsageSummary] {
        guard let data = try? Data(contentsOf: fileURL) else { return [:] }
        return (try? JSONDecoder().decode([String: ProjectUsageSummary].self, from: data)) ?? [:]
    }

    func save(_ summaries: [String: ProjectUsageSummary]) {
        guard let data = try? JSONEncoder().encode(summaries) else { return }
        try? data.write(to: fileURL, options: .atomic)
        try? FileManager.default.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o600))],
            ofItemAtPath: fileURL.path
        )
    }
}
