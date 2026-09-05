import Foundation

public enum LocalTokenHistoryStoreError: Error, LocalizedError {
    case invalidKSFRoot
    case unsafePath(String)
    case corrupted

    public var errorDescription: String? {
        switch self {
        case .invalidKSFRoot:
            return "KSF 根目录不可用。"
        case let .unsafePath(path):
            return "Token 历史缓存路径不安全：\(path)"
        case .corrupted:
            return "Token 历史缓存已损坏，原文件未被覆盖。"
        }
    }
}

public struct LocalTokenHistorySnapshot: Equatable {
    public let updatedAt: Date
    public let days: [DailyUsageBucket]
}

public struct LocalTokenHistoryStore {
    public static let protocolName = "codex-local-token-history-v2"

    public let fileURL: URL
    private let directoryURL: URL
    private let ksfRootURL: URL
    private let fileManager: FileManager

    public init(ksfRootURL: URL, fileManager: FileManager = .default) {
        let root = ksfRootURL.standardizedFileURL
        self.ksfRootURL = root
        self.fileManager = fileManager
        directoryURL = root
            .appendingPathComponent(".agents", isDirectory: true)
            .appendingPathComponent("runtime-data", isDirectory: true)
            .appendingPathComponent("codex-usage-bar", isDirectory: true)
        fileURL = directoryURL.appendingPathComponent("token-history-v2.json")
    }

    public func load() throws -> LocalTokenHistorySnapshot {
        try validateRootAndExistingPath()
        guard let document = try loadDocument() else {
            return LocalTokenHistorySnapshot(updatedAt: .distantPast, days: [])
        }
        return LocalTokenHistorySnapshot(
            updatedAt: document.updatedAt,
            days: document.days.map(\.bucket).sorted { $0.startDate < $1.startDate }
        )
    }

    @discardableResult
    public func mergeAndSave(
        _ observed: [DailyUsageBucket],
        observedAt: Date = Date()
    ) throws -> LocalTokenHistorySnapshot {
        try validateRootAndExistingPath()
        let existing = try loadDocument()
        var byDate = Dictionary(uniqueKeysWithValues: (existing?.days ?? []).map { ($0.date, $0) })
        for candidate in observed where Self.isValid(candidate) {
            guard let current = byDate[candidate.startDate] else {
                byDate[candidate.startDate] = Record(bucket: candidate, lastObservedAt: observedAt)
                continue
            }
            let currentBucket = current.bucket
            if candidate.tokens > currentBucket.tokens
                || (candidate.tokens == currentBucket.tokens
                    && currentBucket.breakdown == nil
                    && candidate.breakdown?.totalTokens == candidate.tokens)
            {
                byDate[candidate.startDate] = Record(bucket: candidate, lastObservedAt: observedAt)
            } else {
                byDate[candidate.startDate] = current.observedAgain(at: observedAt)
            }
        }
        let records = byDate.values.sorted { $0.date < $1.date }
        let days = records.map(\.bucket)
        try prepareDirectory()
        let document = Document(
            protocolName: Self.protocolName,
            updatedAt: observedAt,
            days: records
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        let data = try encoder.encode(document)
        try data.write(to: fileURL, options: .atomic)
        try fileManager.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o600))],
            ofItemAtPath: fileURL.path
        )
        return LocalTokenHistorySnapshot(updatedAt: observedAt, days: days)
    }

    private func loadDocument() throws -> Document? {
        guard fileManager.fileExists(atPath: fileURL.path) else { return nil }
        guard let data = try? Data(contentsOf: fileURL) else {
            throw LocalTokenHistoryStoreError.corrupted
        }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        guard let document = try? decoder.decode(Document.self, from: data),
              document.protocolName == Self.protocolName,
              document.days.allSatisfy(\.isValid)
        else {
            throw LocalTokenHistoryStoreError.corrupted
        }
        return document
    }

    private static func isValid(_ bucket: DailyUsageBucket) -> Bool {
        guard bucket.tokens >= 0 else { return false }
        guard let breakdown = bucket.breakdown else { return true }
        return breakdown.regularInputTokens >= 0
            && breakdown.cachedInputTokens >= 0
            && breakdown.outputTokens >= 0
            && breakdown.totalTokens == bucket.tokens
    }

    private func prepareDirectory() throws {
        try validateRootAndExistingPath()
        try fileManager.createDirectory(at: directoryURL, withIntermediateDirectories: true)
        try validateNoSymbolicLinks(from: ksfRootURL, through: directoryURL)
        try fileManager.setAttributes(
            [.posixPermissions: NSNumber(value: Int16(0o700))],
            ofItemAtPath: directoryURL.path
        )
    }

    private func validateRootAndExistingPath() throws {
        var isDirectory: ObjCBool = false
        guard fileManager.fileExists(atPath: ksfRootURL.path, isDirectory: &isDirectory),
              isDirectory.boolValue
        else { throw LocalTokenHistoryStoreError.invalidKSFRoot }
        let rootPath = ksfRootURL.path.hasSuffix("/") ? ksfRootURL.path : ksfRootURL.path + "/"
        guard fileURL.standardizedFileURL.path.hasPrefix(rootPath) else {
            throw LocalTokenHistoryStoreError.unsafePath(fileURL.path)
        }
        try validateNoSymbolicLinks(from: ksfRootURL, through: fileURL)
    }

    private func validateNoSymbolicLinks(from root: URL, through target: URL) throws {
        var current = root
        let relative = target.path.dropFirst(root.path.count)
            .split(separator: "/")
            .map(String.init)
        for component in [""] + relative {
            if !component.isEmpty { current.appendPathComponent(component) }
            guard fileManager.fileExists(atPath: current.path) else { continue }
            let values = try current.resourceValues(forKeys: [.isSymbolicLinkKey])
            if values.isSymbolicLink == true {
                throw LocalTokenHistoryStoreError.unsafePath(current.path)
            }
        }
    }

    private struct Document: Codable {
        let protocolName: String
        let updatedAt: Date
        let days: [Record]

        enum CodingKeys: String, CodingKey {
            case protocolName = "protocol"
            case updatedAt
            case days
        }
    }

    private struct Record: Codable {
        let date: String
        let totalTokens: Int64
        let regularInputTokens: Int64?
        let cachedInputTokens: Int64?
        let outputTokens: Int64?
        let lastObservedAt: Date

        init(bucket: DailyUsageBucket, lastObservedAt: Date) {
            date = bucket.startDate
            totalTokens = bucket.tokens
            regularInputTokens = bucket.breakdown?.regularInputTokens
            cachedInputTokens = bucket.breakdown?.cachedInputTokens
            outputTokens = bucket.breakdown?.outputTokens
            self.lastObservedAt = lastObservedAt
        }

        var isValid: Bool {
            guard totalTokens >= 0 else { return false }
            let values = [regularInputTokens, cachedInputTokens, outputTokens]
            if values.allSatisfy({ $0 == nil }) { return true }
            guard let regularInputTokens, let cachedInputTokens, let outputTokens else { return false }
            return regularInputTokens >= 0
                && cachedInputTokens >= 0
                && outputTokens >= 0
                && regularInputTokens + cachedInputTokens + outputTokens == totalTokens
        }

        var bucket: DailyUsageBucket {
            let breakdown: TokenUsageBreakdown?
            if let regularInputTokens, let cachedInputTokens, let outputTokens {
                breakdown = TokenUsageBreakdown(
                    regularInputTokens: regularInputTokens,
                    cachedInputTokens: cachedInputTokens,
                    outputTokens: outputTokens
                )
            } else {
                breakdown = nil
            }
            return DailyUsageBucket(startDate: date, tokens: totalTokens, breakdown: breakdown)
        }

        func observedAgain(at date: Date) -> Record {
            Record(bucket: bucket, lastObservedAt: date)
        }
    }
}

public actor LocalTokenHistoryCache {
    public init() {}

    public func load(ksfRootURL: URL) throws -> LocalTokenHistorySnapshot {
        try LocalTokenHistoryStore(
            ksfRootURL: ksfRootURL,
            fileManager: .default
        ).load()
    }

    public func merge(
        ksfRootURL: URL,
        observed: [DailyUsageBucket],
        observedAt: Date = Date()
    ) throws -> LocalTokenHistorySnapshot {
        try LocalTokenHistoryStore(
            ksfRootURL: ksfRootURL,
            fileManager: .default
        ).mergeAndSave(observed, observedAt: observedAt)
    }
}
