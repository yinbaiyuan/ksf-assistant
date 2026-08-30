import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private func expect<T: Equatable>(_ actual: T, _ expected: T, _ message: String) throws {
    guard actual == expected else {
        throw TestFailure(description: "\(message): expected \(expected), got \(actual)")
    }
}

private func testHeadlineSelection() throws {
    let data = Data(
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
    let response = try JSONDecoder().decode(RateLimitsResponse.self, from: data)
    try expect(response.generalBucket?.headlineRemainingPercent, 30, "headline must use the most constrained general window")
    try expect(response.normalizedBuckets.count, 2, "all buckets must remain visible")
}

private func testClamping() throws {
    try expect(RateLimitWindow(usedPercent: -10).remainingPercent, 100, "negative usage clamps")
    try expect(RateLimitWindow(usedPercent: 125).remainingPercent, 0, "over-limit usage clamps")
    try expect(RateLimitBucket(limitId: "codex").headlineRemainingPercent, nil, "empty windows have no headline")
    try expect(
        RateLimitWindow(usedPercent: 10, resetsAt: 1_725_000_000).resetDate,
        Date(timeIntervalSince1970: 1_725_000_000),
        "reset timestamp converts to Date"
    )
}

private func testGeneralBucketCompatibilityMerge() throws {
    let response = RateLimitsResponse(
        rateLimits: RateLimitBucket(
            limitId: "codex",
            primary: RateLimitWindow(usedPercent: 15)
        ),
        rateLimitsByLimitId: [
            "codex_model": RateLimitBucket(
                limitId: "codex_model",
                limitName: "Model quota",
                primary: RateLimitWindow(usedPercent: 90)
            ),
        ]
    )
    try expect(response.normalizedBuckets.map(\.id), ["codex", "codex_model"], "general bucket survives compatibility merge")
    try expect(response.generalBucket?.headlineRemainingPercent, 85, "legacy general bucket remains the headline")
}

private func testCacheMinimization() throws {
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
    try expect(bucket.cacheRepresentation.credits, nil, "cache drops credit balance")
    try expect(bucket.cacheRepresentation.individualLimit, nil, "cache drops spend-control details")
    try expect(bucket.cacheRepresentation.planType, "pro", "cache keeps display metadata")
}

private func testTokenNormalization() throws {
    let buckets = (1...20).map { day in
        DailyUsageBucket(startDate: String(format: "2026-08-%02d", day), tokens: Int64(day * 1_000))
    }.reversed()
    let response = TokenUsageResponse(
        summary: TokenUsageSummary(lifetimeTokens: 100_000, peakDailyTokens: 20_000, currentStreakDays: 4),
        dailyUsageBuckets: Array(buckets)
    )
    try expect(response.normalizedDailyBuckets().count, 14, "cache keeps fourteen daily buckets")
    try expect(response.latestDailyUsage?.startDate, "2026-08-20", "latest usage is explicitly dated")
}

private func testMillionTokenFormatting() throws {
    try expect(TokenCountFormatter.millions(0), "0.0M", "zero stays in million units")
    try expect(TokenCountFormatter.millions(125_000), "0.1M", "sub-million usage stays in million units")
    try expect(TokenCountFormatter.millions(8_500_000), "8.5M", "million usage keeps one decimal")
    try expect(TokenCountFormatter.millions(1_234_500_000), "1234.5M", "large usage never switches to billions")
    try expect(TokenCountFormatter.millions(-1), "0.0M", "invalid negative usage clamps to zero")
}

private func testLocalTodayUsage() throws {
    let fileManager = FileManager.default
    let root = fileManager.temporaryDirectory
        .appendingPathComponent("codex-usage-bar-\(UUID().uuidString)", isDirectory: true)
    try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? fileManager.removeItem(at: root) }

    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(identifier: "Asia/Shanghai")!
    let now = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30, hour: 12))!
    let sessionA = [
        #"{"timestamp":"2026-08-29T15:59:59Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100,"input_tokens":90,"cached_input_tokens":60,"output_tokens":10}}}}"#,
        #"{"timestamp":"2026-08-29T16:00:10Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":140,"input_tokens":125,"cached_input_tokens":85,"output_tokens":15}}}}"#,
        #"{"type":"event_msg","payload":{"type":"user_message","message":"ignored"}}"#,
        #"{"timestamp":"2026-08-29T17:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":180,"input_tokens":160,"cached_input_tokens":110,"output_tokens":20}}}}"#,
    ]
    let sessionB = [
        #"{"timestamp":"2026-08-30T01:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":50,"input_tokens":45,"cached_input_tokens":30,"output_tokens":5}}}}"#,
    ]
    let firstURL = root.appendingPathComponent("existing.jsonl")
    let secondURL = root.appendingPathComponent("new.jsonl")
    try Data((sessionA.joined(separator: "\n") + "\n").utf8).write(to: firstURL)
    try Data((sessionB.joined(separator: "\n") + "\n").utf8).write(to: secondURL)
    try fileManager.setAttributes([.modificationDate: now], ofItemAtPath: firstURL.path)
    try fileManager.setAttributes([.modificationDate: now], ofItemAtPath: secondURL.path)

    let local = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
        .readToday(now: now, calendar: calendar)
    try expect(local?.startDate, "2026-08-30", "local usage uses the local calendar day")
    try expect(local?.tokens, 130, "local usage subtracts the prior-day baseline and sums sessions")
    try expect(local?.breakdown?.regularInputTokens, 35, "local usage separates uncached input")
    try expect(local?.breakdown?.cachedInputTokens, 80, "local usage separates cached input")
    try expect(local?.breakdown?.outputTokens, 15, "local usage separates output")
    try expect(local?.breakdown?.totalTokens, local?.tokens, "local split reconciles to the total")

    let previousDay = calendar.date(
        from: DateComponents(year: 2026, month: 8, day: 29, hour: 12)
    )!
    let previousLocal = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
        .read(on: previousDay, calendar: calendar)
    try expect(previousLocal?.startDate, "2026-08-29", "local reader accepts a requested day")
    try expect(previousLocal?.tokens, 100, "requested day excludes counters after its local boundary")

    let cachedSnapshot = UsageSnapshot(localPreviousDailyUsage: previousLocal)
    let restoredSnapshot = try JSONDecoder().decode(
        UsageSnapshot.self,
        from: JSONEncoder().encode(cachedSnapshot)
    )
    try expect(
        restoredSnapshot.localPreviousDailyUsage,
        previousLocal,
        "cache preserves the previous local day"
    )

    let account = TokenUsageResponse(
        summary: TokenUsageSummary(lifetimeTokens: 100),
        dailyUsageBuckets: [DailyUsageBucket(startDate: "2026-08-29", tokens: 40)]
    )
    try expect(account.dailyUsage(on: "2026-08-30"), nil, "missing account day remains unsynchronized")
}

private func testResetDetection() throws {
    let previous = RateLimitBucket(
        limitId: "codex",
        primary: RateLimitWindow(usedPercent: 80, windowDurationMins: 300, resetsAt: 1_000)
    )
    let current = RateLimitBucket(
        limitId: "codex",
        primary: RateLimitWindow(usedPercent: 5, windowDurationMins: 300, resetsAt: 2_000)
    )
    let events = ResetDetector.detect(
        previous: previous,
        current: current,
        previousCapturedAt: Date(timeIntervalSince1970: 900),
        currentCapturedAt: Date(timeIntervalSince1970: 1_010),
        notifiedResetTimestamps: []
    )
    try expect(events.map(\.resetTimestamp), [1_000], "crossed main reset emits once")

    let modelPrevious = RateLimitBucket(
        limitId: "codex_model",
        primary: RateLimitWindow(usedPercent: 80, windowDurationMins: 300, resetsAt: 1_000)
    )
    let modelCurrent = RateLimitBucket(
        limitId: "codex_model",
        primary: RateLimitWindow(usedPercent: 0, windowDurationMins: 300, resetsAt: 2_000)
    )
    try expect(
        ResetDetector.detect(
            previous: modelPrevious,
            current: modelCurrent,
            previousCapturedAt: Date(timeIntervalSince1970: 900),
            currentCapturedAt: Date(timeIntervalSince1970: 1_010),
            notifiedResetTimestamps: []
        ).count,
        0,
        "model-specific resets never notify"
    )

    try expect(
        ResetDetector.detect(
            previous: previous,
            current: current,
            previousCapturedAt: Date(timeIntervalSince1970: 900),
            currentCapturedAt: Date(timeIntervalSince1970: 950),
            notifiedResetTimestamps: []
        ).count,
        0,
        "server reschedule before the boundary does not notify"
    )

    try expect(
        ResetDetector.detect(
            previous: previous,
            current: current,
            previousCapturedAt: Date(timeIntervalSince1970: 900),
            currentCapturedAt: Date(timeIntervalSince1970: 1_010),
            notifiedResetTimestamps: [1_000]
        ).count,
        0,
        "old reset timestamp is deduplicated"
    )
}

@main
private enum StandaloneTestRunner {
    static func main() {
        let tests: [(String, () throws -> Void)] = [
            ("headline selection", testHeadlineSelection),
            ("percentage clamping", testClamping),
            ("general bucket compatibility merge", testGeneralBucketCompatibilityMerge),
            ("cache minimization", testCacheMinimization),
            ("token normalization", testTokenNormalization),
            ("million token formatting", testMillionTokenFormatting),
            ("local today usage", testLocalTodayUsage),
            ("reset detection", testResetDetection),
        ]

        do {
            for (name, test) in tests {
                try test()
                print("PASS \(name)")
            }
            print("PASS \(tests.count) standalone tests")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
