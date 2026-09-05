import XCTest
@testable import KSFAssistantCore

final class UsageModelsTests: XCTestCase {
    func testHeadlineUsesMostConstrainedGeneralWindowAndIgnoresModelBuckets() throws {
        let response = try JSONDecoder().decode(
            RateLimitsResponse.self,
            from: Data(
                #"""
                {
                  "rateLimits": {
                    "limitId": "codex",
                    "primary": { "usedPercent": 20, "windowDurationMins": 300, "resetsAt": 1000 },
                    "secondary": { "usedPercent": 70, "windowDurationMins": 10080, "resetsAt": 2000 }
                  },
                  "rateLimitsByLimitId": {
                    "codex_model": {
                      "limitId": "codex_model",
                      "limitName": "Model quota",
                      "primary": { "usedPercent": 99, "windowDurationMins": 300, "resetsAt": 1000 }
                    },
                    "codex": {
                      "limitId": "codex",
                      "primary": { "usedPercent": 20, "windowDurationMins": 300, "resetsAt": 1000 },
                      "secondary": { "usedPercent": 70, "windowDurationMins": 10080, "resetsAt": 2000 }
                    }
                  }
                }
                """#.utf8
            )
        )

        XCTAssertEqual(response.generalBucket?.headlineRemainingPercent, 30)
        XCTAssertEqual(response.normalizedBuckets.count, 2)
    }

    func testRemainingPercentIsClampedToValidRange() {
        XCTAssertEqual(RateLimitWindow(usedPercent: -10).remainingPercent, 100)
        XCTAssertEqual(RateLimitWindow(usedPercent: 125).remainingPercent, 0)
    }

    func testEmptyWindowHasNoHeadline() {
        XCTAssertNil(RateLimitBucket(limitId: "codex").headlineRemainingPercent)
    }

    func testResetTimestampConvertsToDate() {
        XCTAssertEqual(
            RateLimitWindow(usedPercent: 10, resetsAt: 1_725_000_000).resetDate,
            Date(timeIntervalSince1970: 1_725_000_000)
        )
    }

    func testCacheRepresentationDropsUnusedFinancialFields() {
        let bucket = RateLimitBucket(
            limitId: "codex",
            primary: RateLimitWindow(usedPercent: 10),
            credits: CreditsSnapshot(hasCredits: true, unlimited: false, balance: "12.34"),
            individualLimit: SpendControlLimitSnapshot(
                limit: "100",
                remainingPercent: 50,
                resetsAt: 2_000,
                used: "50"
            ),
            planType: "pro"
        )

        XCTAssertNil(bucket.cacheRepresentation.credits)
        XCTAssertNil(bucket.cacheRepresentation.individualLimit)
        XCTAssertEqual(bucket.cacheRepresentation.planType, "pro")
    }

    func testLegacyGeneralBucketIsMergedWhenMappingOnlyContainsModelLimits() throws {
        let response = try JSONDecoder().decode(
            RateLimitsResponse.self,
            from: Data(
                #"""
                {
                  "rateLimits": {
                    "limitId": "codex",
                    "primary": { "usedPercent": 15 }
                  },
                  "rateLimitsByLimitId": {
                    "codex_model": {
                      "limitId": "codex_model",
                      "limitName": "Model quota",
                      "primary": { "usedPercent": 90 }
                    }
                  }
                }
                """#.utf8
            )
        )

        XCTAssertEqual(response.normalizedBuckets.map(\.id), ["codex", "codex_model"])
        XCTAssertEqual(response.generalBucket?.headlineRemainingPercent, 85)
    }

    func testTokenUsageKeepsLatestFourteenBucketsAndLabelsLatestRecordedDate() throws {
        let calendar = Calendar(identifier: .gregorian)
        let buckets = (1...20).map { day in
            DailyUsageBucket(startDate: String(format: "2026-08-%02d", day), tokens: Int64(day * 1_000))
        }.reversed()
        let response = TokenUsageResponse(
            summary: TokenUsageSummary(lifetimeTokens: 100_000, peakDailyTokens: 20_000, currentStreakDays: 4),
            dailyUsageBuckets: Array(buckets)
        )

        let normalized = response.normalizedDailyBuckets(calendar: calendar)

        XCTAssertEqual(normalized.count, 14)
        XCTAssertEqual(normalized.last?.startDate, "2026-08-20")
        XCTAssertEqual(response.latestDailyUsage?.startDate, "2026-08-20")
        XCTAssertEqual(response.latestDailyUsage?.tokens, 20_000)
    }

    func testUsageSnapshotPreservesPreviousLocalDay() throws {
        let previous = DailyUsageBucket(startDate: "2026-08-30", tokens: 658_819_853)
        let snapshot = UsageSnapshot(localPreviousDailyUsage: previous)

        let decoded = try JSONDecoder().decode(
            UsageSnapshot.self,
            from: JSONEncoder().encode(snapshot)
        )

        XCTAssertEqual(decoded.localPreviousDailyUsage, previous)
    }

    func testMenuTokenFormattingAlwaysUsesMillions() {
        XCTAssertEqual(TokenCountFormatter.millions(0), "0.0M")
        XCTAssertEqual(TokenCountFormatter.millions(125_000), "0.1M")
        XCTAssertEqual(TokenCountFormatter.millions(8_500_000), "8.5M")
        XCTAssertEqual(TokenCountFormatter.millions(1_234_500_000), "1234.5M")
        XCTAssertEqual(TokenCountFormatter.millions(-1), "0.0M")
    }
}
