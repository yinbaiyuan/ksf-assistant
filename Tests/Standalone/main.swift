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

private func testLocalTodayUsagePreservesCounterReclassification() throws {
    let fileManager = FileManager.default
    let root = fileManager.temporaryDirectory
        .appendingPathComponent("codex-usage-reclassification-\(UUID().uuidString)", isDirectory: true)
    try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? fileManager.removeItem(at: root) }

    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(identifier: "Asia/Shanghai")!
    let now = calendar.date(from: DateComponents(year: 2026, month: 9, day: 1, hour: 18))!
    let lines = [
        #"{"timestamp":"2026-09-01T09:02:41Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":60672,"input_tokens":60605,"cached_input_tokens":11008,"output_tokens":67}}}}"#,
        #"{"timestamp":"2026-09-01T09:07:20Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":60726,"input_tokens":60680,"cached_input_tokens":60160,"output_tokens":46}}}}"#,
    ]
    let sessionURL = root.appendingPathComponent("reclassified.jsonl")
    try Data((lines.joined(separator: "\n") + "\n").utf8).write(to: sessionURL)
    try fileManager.setAttributes([.modificationDate: now], ofItemAtPath: sessionURL.path)

    let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
        .readToday(now: now, calendar: calendar)
    try expect(usage?.tokens, 60_726, "counter reclassification preserves the total")
    try expect(usage?.breakdown?.regularInputTokens, 520, "counter reclassification preserves ordinary input")
    try expect(usage?.breakdown?.cachedInputTokens, 60_160, "counter reclassification preserves cached input")
    try expect(usage?.breakdown?.outputTokens, 46, "counter reclassification preserves output")
    try expect(usage?.breakdown?.totalTokens, usage?.tokens, "reclassified split reconciles")
}

private func testLocalTokenHistory() throws {
    let fileManager = FileManager.default
    let root = fileManager.temporaryDirectory
        .appendingPathComponent("codex-usage-history-\(UUID().uuidString)", isDirectory: true)
    try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? fileManager.removeItem(at: root) }

    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(identifier: "Asia/Shanghai")!
    let now = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30, hour: 12))!
    let lines = [
        #"{"timestamp":"2026-08-27T15:59:59Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100,"input_tokens":90,"cached_input_tokens":60,"output_tokens":10}}}}"#,
        #"{"timestamp":"2026-08-27T16:00:10Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":140,"input_tokens":125,"cached_input_tokens":85,"output_tokens":15}}}}"#,
        #"{"timestamp":"2026-08-28T15:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":180,"input_tokens":160,"cached_input_tokens":110,"output_tokens":20}}}}"#,
        #"{"timestamp":"2026-08-29T16:00:10Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":220,"input_tokens":195,"cached_input_tokens":135,"output_tokens":25}}}}"#,
    ]
    let sessionURL = root.appendingPathComponent("history.jsonl")
    try Data((lines.joined(separator: "\n") + "\n").utf8).write(to: sessionURL)
    try fileManager.setAttributes([.modificationDate: now], ofItemAtPath: sessionURL.path)

    let history = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
        .readHistory(through: now, dayCount: 3, calendar: calendar)
    try expect(
        history?.map(\.startDate),
        ["2026-08-28", "2026-08-29", "2026-08-30"],
        "history returns every requested natural day"
    )
    try expect(history?.map(\.tokens), [80, 0, 40], "history preserves zero-use days")
    try expect(history?.first?.breakdown?.totalTokens, 80, "history split reconciles")
}

private func testLocalTokenHistoryIncludesArchivedSessionsWithoutDuplicates() throws {
    let fileManager = FileManager.default
    let root = fileManager.temporaryDirectory
        .appendingPathComponent("codex-usage-roots-\(UUID().uuidString)", isDirectory: true)
    let active = root.appendingPathComponent("sessions", isDirectory: true)
    let archived = root.appendingPathComponent("archived_sessions", isDirectory: true)
    try fileManager.createDirectory(at: active, withIntermediateDirectories: true)
    try fileManager.createDirectory(at: archived, withIntermediateDirectories: true)
    defer { try? fileManager.removeItem(at: root) }

    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(identifier: "Asia/Shanghai")!
    let now = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30, hour: 12))!
    let activeLines = [
        #"{"timestamp":"2026-08-30T01:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100,"input_tokens":90,"cached_input_tokens":60,"output_tokens":10}}}}"#,
    ]
    let archivedLines = [
        #"{"timestamp":"2026-08-30T02:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":200,"input_tokens":180,"cached_input_tokens":120,"output_tokens":20}}}}"#,
    ]
    let duplicateName = "rollout-2026-08-30T09-00-00-01a050b4-3c9a-7582-aaf4-c83d099b75c0.jsonl"
    let archivedOnlyName = "rollout-2026-08-30T10-00-00-01a05180-b488-7231-a230-f5024b9d8d18.jsonl"
    try Data((activeLines.joined(separator: "\n") + "\n").utf8)
        .write(to: active.appendingPathComponent(duplicateName))
    try Data((activeLines.joined(separator: "\n") + "\n").utf8)
        .write(to: archived.appendingPathComponent(duplicateName))
    try Data((archivedLines.joined(separator: "\n") + "\n").utf8)
        .write(to: archived.appendingPathComponent(archivedOnlyName))

    let usage = LocalTokenUsageReader(
        sessionRoots: [active, archived],
        fileManager: fileManager
    ).readToday(now: now, calendar: calendar)
    try expect(usage?.tokens, 300, "active and archived roots merge and duplicate sessions count once")
}

private func testLocalTokenHistoryStoreKeepsMonotonicHistory() throws {
    let fileManager = FileManager.default
    let root = fileManager.temporaryDirectory
        .appendingPathComponent("codex-history-store-\(UUID().uuidString)", isDirectory: true)
    try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? fileManager.removeItem(at: root) }

    let store = LocalTokenHistoryStore(ksfRootURL: root, fileManager: fileManager)
    let legacyURL = root
        .appendingPathComponent(".agents/runtime-data/codex-usage-bar", isDirectory: true)
        .appendingPathComponent("token-history-v1.json")
    try fileManager.createDirectory(at: legacyURL.deletingLastPathComponent(), withIntermediateDirectories: true)
    let legacyData = Data(#"{"protocol":"codex-local-token-history-v1","days":[]}"#.utf8)
    try legacyData.write(to: legacyURL)
    try expect(try store.load().days.isEmpty, true, "v2 history does not import the personal v1 cache")
    try expect(try Data(contentsOf: legacyURL), legacyData, "v2 history leaves the personal v1 cache untouched")
    let first = DailyUsageBucket(
        startDate: "2026-08-30",
        tokens: 600,
        breakdown: TokenUsageBreakdown(
            regularInputTokens: 100,
            cachedInputTokens: 450,
            outputTokens: 50
        )
    )
    let smaller = DailyUsageBucket(startDate: "2026-08-30", tokens: 200)
    let prior = DailyUsageBucket(startDate: "2026-08-29", tokens: 50)
    _ = try store.mergeAndSave([prior, first], observedAt: Date(timeIntervalSince1970: 10))
    let merged = try store.mergeAndSave([smaller], observedAt: Date(timeIntervalSince1970: 20))

    try expect(merged.days.map(\.tokens), [50, 600], "history cache never decreases after logs disappear")
    try expect(
        merged.days.last?.breakdown?.totalTokens,
        600,
        "an incomplete smaller observation does not replace a complete cached split"
    )
    try expect(store.fileURL.path.hasSuffix(".agents/runtime-data/codex-usage-bar/token-history-v2.json"), true, "history cache uses the KSF runtime path")

    let invalidLarger = DailyUsageBucket(
        startDate: "2026-08-30",
        tokens: 700,
        breakdown: TokenUsageBreakdown(
            regularInputTokens: 100,
            cachedInputTokens: 450,
            outputTokens: 50
        )
    )
    let afterInvalid = try store.mergeAndSave(
        [invalidLarger],
        observedAt: Date(timeIntervalSince1970: 30)
    )
    try expect(
        afterInvalid.days.last?.tokens,
        600,
        "a split that does not reconcile cannot replace verified history"
    )
    let encoded = try JSONSerialization.jsonObject(
        with: Data(contentsOf: store.fileURL)
    ) as? [String: Any]
    let encodedDays = encoded?["days"] as? [[String: Any]]
    let untouched = encodedDays?.first { $0["date"] as? String == "2026-08-29" }
    try expect(
        untouched?["lastObservedAt"] as? String,
        "1970-01-01T00:00:10Z",
        "unobserved historical days keep their own observation timestamp"
    )

    let directoryPermissions = try fileManager.attributesOfItem(
        atPath: store.fileURL.deletingLastPathComponent().path
    )[.posixPermissions] as? NSNumber
    let filePermissions = try fileManager.attributesOfItem(
        atPath: store.fileURL.path
    )[.posixPermissions] as? NSNumber
    try expect(directoryPermissions?.intValue, 0o700, "history directory is private")
    try expect(filePermissions?.intValue, 0o600, "history file is private")

    let corrupt = Data("not-json".utf8)
    try corrupt.write(to: store.fileURL)
    var rejectedCorruption = false
    do {
        _ = try store.mergeAndSave([first])
    } catch LocalTokenHistoryStoreError.corrupted {
        rejectedCorruption = true
    }
    try expect(rejectedCorruption, true, "corrupt history is rejected")
    try expect(try Data(contentsOf: store.fileURL), corrupt, "corrupt history is not overwritten")

    let unsafeRoot = root.appendingPathComponent("unsafe", isDirectory: true)
    let outside = root.appendingPathComponent("outside", isDirectory: true)
    try fileManager.createDirectory(at: unsafeRoot, withIntermediateDirectories: true)
    try fileManager.createDirectory(at: outside, withIntermediateDirectories: true)
    try fileManager.createSymbolicLink(
        at: unsafeRoot.appendingPathComponent(".agents"),
        withDestinationURL: outside
    )
    var rejectedSymlink = false
    do {
        _ = try LocalTokenHistoryStore(ksfRootURL: unsafeRoot, fileManager: fileManager).load()
    } catch LocalTokenHistoryStoreError.unsafePath {
        rejectedSymlink = true
    }
    try expect(rejectedSymlink, true, "history cache rejects symbolic-link paths")
}

private func testLocalTokenHistorySeries() throws {
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(identifier: "Asia/Shanghai")!
    let endDate = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30))!
    let series = LocalTokenHistorySeries(days: [
        DailyUsageBucket(startDate: "2026-08-30", tokens: 20),
        DailyUsageBucket(startDate: "2026-08-28", tokens: 0),
        DailyUsageBucket(startDate: "2026-08-29", tokens: 80),
    ], through: endDate, calendar: calendar)
    try expect(
        series.days.count,
        30,
        "history series always contains thirty natural days"
    )
    try expect(series.days.first?.startDate, "2026-08-01", "history series starts twenty-nine days earlier")
    try expect(series.days.last?.startDate, "2026-08-30", "history series ends on the requested day")
    try expect(series.usage(on: "2026-08-27")?.tokens, 0, "history series fills missing days with zero")
    try expect(series.totalTokens, 100, "history series sums the visible range")
    try expect(series.averageTokens, 3, "history series averages all thirty natural days")
    try expect(series.activeDayCount, 2, "history series counts non-zero days")
    try expect(series.maximumTokens, 80, "history series exposes the factual scale maximum")
    try expect(series.latestDay?.startDate, "2026-08-30", "history series defaults to the latest day")
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
            ("local today counter reclassification", testLocalTodayUsagePreservesCounterReclassification),
            ("local token history", testLocalTokenHistory),
            ("local token roots", testLocalTokenHistoryIncludesArchivedSessionsWithoutDuplicates),
            ("local token history store", testLocalTokenHistoryStoreKeepsMonotonicHistory),
            ("local token history series", testLocalTokenHistorySeries),
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
