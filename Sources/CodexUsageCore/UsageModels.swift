import Foundation

public struct RateLimitWindow: Codable, Equatable, Hashable {
    public let usedPercent: Int
    public let windowDurationMins: Int64?
    public let resetsAt: Int64?

    public init(usedPercent: Int, windowDurationMins: Int64? = nil, resetsAt: Int64? = nil) {
        self.usedPercent = usedPercent
        self.windowDurationMins = windowDurationMins
        self.resetsAt = resetsAt
    }

    public var remainingPercent: Int {
        min(100, max(0, 100 - usedPercent))
    }

    public var resetDate: Date? {
        resetsAt.map { Date(timeIntervalSince1970: TimeInterval($0)) }
    }
}

public struct CreditsSnapshot: Codable, Equatable {
    public let hasCredits: Bool
    public let unlimited: Bool
    public let balance: String?

    public init(hasCredits: Bool, unlimited: Bool, balance: String? = nil) {
        self.hasCredits = hasCredits
        self.unlimited = unlimited
        self.balance = balance
    }
}

public struct SpendControlLimitSnapshot: Codable, Equatable {
    public let limit: String
    public let remainingPercent: Int
    public let resetsAt: Int64
    public let used: String
}

public struct RateLimitBucket: Codable, Equatable, Identifiable {
    public let limitId: String?
    public let limitName: String?
    public let primary: RateLimitWindow?
    public let secondary: RateLimitWindow?
    public let credits: CreditsSnapshot?
    public let individualLimit: SpendControlLimitSnapshot?
    public let planType: String?
    public let rateLimitReachedType: String?

    public init(
        limitId: String? = nil,
        limitName: String? = nil,
        primary: RateLimitWindow? = nil,
        secondary: RateLimitWindow? = nil,
        credits: CreditsSnapshot? = nil,
        individualLimit: SpendControlLimitSnapshot? = nil,
        planType: String? = nil,
        rateLimitReachedType: String? = nil
    ) {
        self.limitId = limitId
        self.limitName = limitName
        self.primary = primary
        self.secondary = secondary
        self.credits = credits
        self.individualLimit = individualLimit
        self.planType = planType
        self.rateLimitReachedType = rateLimitReachedType
    }

    public var id: String {
        limitId ?? limitName ?? "unknown"
    }

    public var displayName: String {
        if id == "codex" { return "Codex" }
        return limitName ?? limitId ?? "其他额度"
    }

    public var windows: [RateLimitWindow] {
        [primary, secondary]
            .compactMap { $0 }
            .sorted { ($0.windowDurationMins ?? .max) < ($1.windowDurationMins ?? .max) }
    }

    public var headlineRemainingPercent: Int? {
        windows.map(\.remainingPercent).min()
    }

    public var cacheRepresentation: RateLimitBucket {
        RateLimitBucket(
            limitId: limitId,
            limitName: limitName,
            primary: primary,
            secondary: secondary,
            planType: planType,
            rateLimitReachedType: rateLimitReachedType
        )
    }
}

public struct RateLimitsResponse: Codable, Equatable {
    public let rateLimits: RateLimitBucket
    public let rateLimitsByLimitId: [String: RateLimitBucket]?

    public init(rateLimits: RateLimitBucket, rateLimitsByLimitId: [String: RateLimitBucket]? = nil) {
        self.rateLimits = rateLimits
        self.rateLimitsByLimitId = rateLimitsByLimitId
    }

    public var normalizedBuckets: [RateLimitBucket] {
        var bucketsByID: [String: RateLimitBucket] = [:]
        for (key, bucket) in rateLimitsByLimitId ?? [:] {
            bucketsByID[bucket.limitId ?? key] = bucket
        }
        let rootID = rateLimits.limitId ?? "codex"
        if bucketsByID[rootID] == nil {
            bucketsByID[rootID] = rateLimits
        }
        let source = Array(bucketsByID.values)

        return source.sorted { left, right in
            if left.id == "codex" { return true }
            if right.id == "codex" { return false }
            return left.displayName.localizedCaseInsensitiveCompare(right.displayName) == .orderedAscending
        }
    }

    public var generalBucket: RateLimitBucket? {
        if let direct = rateLimitsByLimitId?["codex"] { return direct }
        if let matched = rateLimitsByLimitId?.values.first(where: { $0.limitId == "codex" }) { return matched }
        return rateLimits.limitId == "codex" ? rateLimits : nil
    }
}

public struct TokenUsageSummary: Codable, Equatable {
    public let lifetimeTokens: Int64?
    public let peakDailyTokens: Int64?
    public let longestRunningTurnSec: Int64?
    public let currentStreakDays: Int64?
    public let longestStreakDays: Int64?

    public init(
        lifetimeTokens: Int64? = nil,
        peakDailyTokens: Int64? = nil,
        longestRunningTurnSec: Int64? = nil,
        currentStreakDays: Int64? = nil,
        longestStreakDays: Int64? = nil
    ) {
        self.lifetimeTokens = lifetimeTokens
        self.peakDailyTokens = peakDailyTokens
        self.longestRunningTurnSec = longestRunningTurnSec
        self.currentStreakDays = currentStreakDays
        self.longestStreakDays = longestStreakDays
    }
}

public struct DailyUsageBucket: Codable, Equatable, Identifiable {
    public let startDate: String
    public let tokens: Int64
    public let breakdown: TokenUsageBreakdown?

    public init(
        startDate: String,
        tokens: Int64,
        breakdown: TokenUsageBreakdown? = nil
    ) {
        self.startDate = startDate
        self.tokens = tokens
        self.breakdown = breakdown
    }

    public var id: String { startDate }
}

public struct TokenUsageBreakdown: Codable, Equatable {
    public let regularInputTokens: Int64
    public let cachedInputTokens: Int64
    public let outputTokens: Int64

    public init(
        regularInputTokens: Int64,
        cachedInputTokens: Int64,
        outputTokens: Int64
    ) {
        self.regularInputTokens = regularInputTokens
        self.cachedInputTokens = cachedInputTokens
        self.outputTokens = outputTokens
    }

    public var totalTokens: Int64 {
        regularInputTokens + cachedInputTokens + outputTokens
    }
}

public struct TokenUsageResponse: Codable, Equatable {
    public let summary: TokenUsageSummary
    public let dailyUsageBuckets: [DailyUsageBucket]?

    public init(summary: TokenUsageSummary, dailyUsageBuckets: [DailyUsageBucket]? = nil) {
        self.summary = summary
        self.dailyUsageBuckets = dailyUsageBuckets
    }

    public func normalizedDailyBuckets(calendar _: Calendar = .current) -> [DailyUsageBucket] {
        Array((dailyUsageBuckets ?? []).sorted { $0.startDate < $1.startDate }.suffix(14))
    }

    public var latestDailyUsage: DailyUsageBucket? {
        dailyUsageBuckets?.max { $0.startDate < $1.startDate }
    }

    public func dailyUsage(on startDate: String) -> DailyUsageBucket? {
        dailyUsageBuckets?.first { $0.startDate == startDate }
    }
}

public struct UsageSnapshot: Codable, Equatable {
    public var buckets: [RateLimitBucket]
    public var tokenSummary: TokenUsageSummary?
    public var dailyUsageBuckets: [DailyUsageBucket]
    public var localDailyUsage: DailyUsageBucket?
    public var localPreviousDailyUsage: DailyUsageBucket?
    public var rateUpdatedAt: Date?
    public var tokenUpdatedAt: Date?
    public var localTokenUpdatedAt: Date?

    public init(
        buckets: [RateLimitBucket] = [],
        tokenSummary: TokenUsageSummary? = nil,
        dailyUsageBuckets: [DailyUsageBucket] = [],
        localDailyUsage: DailyUsageBucket? = nil,
        localPreviousDailyUsage: DailyUsageBucket? = nil,
        rateUpdatedAt: Date? = nil,
        tokenUpdatedAt: Date? = nil,
        localTokenUpdatedAt: Date? = nil
    ) {
        self.buckets = buckets
        self.tokenSummary = tokenSummary
        self.dailyUsageBuckets = dailyUsageBuckets
        self.localDailyUsage = localDailyUsage
        self.localPreviousDailyUsage = localPreviousDailyUsage
        self.rateUpdatedAt = rateUpdatedAt
        self.tokenUpdatedAt = tokenUpdatedAt
        self.localTokenUpdatedAt = localTokenUpdatedAt
    }

    public var generalBucket: RateLimitBucket? {
        buckets.first { $0.limitId == "codex" || $0.id == "codex" }
    }

    public var headlineRemainingPercent: Int? {
        generalBucket?.headlineRemainingPercent
    }

    public var latestDailyUsage: DailyUsageBucket? {
        dailyUsageBuckets.max { $0.startDate < $1.startDate }
    }

    public func accountDailyUsage(on startDate: String) -> DailyUsageBucket? {
        dailyUsageBuckets.first { $0.startDate == startDate }
    }
}
