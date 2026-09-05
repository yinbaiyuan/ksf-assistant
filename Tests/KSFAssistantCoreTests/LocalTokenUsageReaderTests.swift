import XCTest
@testable import KSFAssistantCore

final class LocalTokenUsageReaderTests: XCTestCase {
    func testHistorySeriesSortsDaysAndDerivesComparableSummary() {
        let calendar = shanghaiCalendar()
        let endDate = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30))!
        let series = LocalTokenHistorySeries(days: [
            DailyUsageBucket(startDate: "2026-08-30", tokens: 20),
            DailyUsageBucket(startDate: "2026-08-28", tokens: 0),
            DailyUsageBucket(startDate: "2026-08-29", tokens: 80),
        ], through: endDate, calendar: calendar)

        XCTAssertEqual(series.days.count, 30)
        XCTAssertEqual(series.days.first?.startDate, "2026-08-01")
        XCTAssertEqual(series.days.last?.startDate, "2026-08-30")
        XCTAssertEqual(series.usage(on: "2026-08-27")?.tokens, 0)
        XCTAssertEqual(series.totalTokens, 100)
        XCTAssertEqual(series.averageTokens, 3)
        XCTAssertEqual(series.activeDayCount, 2)
        XCTAssertEqual(series.maximumTokens, 80)
        XCTAssertEqual(series.latestDay?.startDate, "2026-08-30")
        XCTAssertEqual(series.usage(on: "2026-08-29")?.tokens, 80)
    }

    func testHistoryComparisonAlignsServerUsageAndCalculatesUnclampedShare() {
        let series = TokenHistoryComparisonSeries(days: [
            TokenHistoryComparisonDay(
                startDate: "2026-09-01",
                serverTokens: 200,
                localTokens: 50,
                localBreakdown: TokenUsageBreakdown(
                    regularInputTokens: 10,
                    cachedInputTokens: 30,
                    outputTokens: 10
                )
            ),
            TokenHistoryComparisonDay(
                startDate: "2026-09-02",
                serverTokens: 40,
                localTokens: 80
            ),
            TokenHistoryComparisonDay(
                startDate: "2026-09-03",
                localTokens: 10
            ),
        ])

        XCTAssertEqual(series.localTotalTokens, 140)
        XCTAssertEqual(series.localAverageTokens, 46)
        XCTAssertEqual(series.localActiveDayCount, 3)
        XCTAssertEqual(series.maximumTokens, 200)
        XCTAssertEqual(series.localShare(on: "2026-09-01"), 0.25)
        XCTAssertEqual(series.localShare(on: "2026-09-02"), 2.0)
        XCTAssertNil(series.localShare(on: "2026-09-03"))
        XCTAssertEqual(series.usage(on: "2026-09-01")?.localBreakdown?.cachedInputTokens, 30)
    }

    func testHistoryComparisonFallbackBuildsThirtyAlignedDays() {
        let calendar = shanghaiCalendar()
        let endDate = calendar.date(from: DateComponents(year: 2026, month: 9, day: 3))!
        let series = TokenHistoryComparisonSeries(
            localDays: [DailyUsageBucket(startDate: "2026-09-03", tokens: 20)],
            serverDays: [DailyUsageBucket(startDate: "2026-09-02", tokens: 100)],
            through: endDate,
            calendar: calendar
        )

        XCTAssertEqual(series.days.count, 30)
        XCTAssertEqual(series.usage(on: "2026-09-02")?.serverTokens, 100)
        XCTAssertEqual(series.usage(on: "2026-09-02")?.localTokens, 0)
        XCTAssertEqual(series.usage(on: "2026-09-03")?.localTokens, 20)
        XCTAssertNil(series.usage(on: "2026-09-03")?.serverTokens)
    }

    func testTodayUsageSumsPositiveDeltasAcrossLocalSessions() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let now = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30, hour: 12))!
        try writeSession(
            at: root.appendingPathComponent("existing.jsonl"),
            lines: [
                tokenLine(
                    timestamp: "2026-08-29T15:59:59Z",
                    total: 100,
                    input: 90,
                    cachedInput: 60,
                    output: 10
                ),
                tokenLine(
                    timestamp: "2026-08-29T16:00:10Z",
                    total: 140,
                    input: 125,
                    cachedInput: 85,
                    output: 15
                ),
                #"{"type":"event_msg","payload":{"type":"user_message","message":"ignored"}}"#,
                tokenLine(
                    timestamp: "2026-08-29T17:00:00Z",
                    total: 180,
                    input: 160,
                    cachedInput: 110,
                    output: 20
                ),
            ],
            modifiedAt: now
        )
        try writeSession(
            at: root.appendingPathComponent("new-today.jsonl"),
            lines: [
                tokenLine(
                    timestamp: "2026-08-30T01:00:00Z",
                    total: 50,
                    input: 45,
                    cachedInput: 30,
                    output: 5
                ),
            ],
            modifiedAt: now
        )

        let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .readToday(now: now, calendar: calendar)

        XCTAssertEqual(usage?.startDate, "2026-08-30")
        XCTAssertEqual(usage?.tokens, 130)
        XCTAssertEqual(usage?.breakdown?.regularInputTokens, 35)
        XCTAssertEqual(usage?.breakdown?.cachedInputTokens, 80)
        XCTAssertEqual(usage?.breakdown?.outputTokens, 15)
        XCTAssertEqual(usage?.breakdown?.totalTokens, usage?.tokens)
    }

    func testTodayUsageKeepsTotalButOmitsIncompleteBreakdown() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let now = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30, hour: 12))!
        try writeSession(
            at: root.appendingPathComponent("legacy.jsonl"),
            lines: [tokenLine(timestamp: "2026-08-30T01:00:00Z", total: 50)],
            modifiedAt: now
        )

        let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .readToday(now: now, calendar: calendar)

        XCTAssertEqual(usage?.tokens, 50)
        XCTAssertNil(usage?.breakdown)
    }

    func testTodayUsagePreservesCounterReclassificationWithinTheDay() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let now = calendar.date(from: DateComponents(year: 2026, month: 9, day: 1, hour: 18))!
        try writeSession(
            at: root.appendingPathComponent("reclassified.jsonl"),
            lines: [
                tokenLine(
                    timestamp: "2026-09-01T09:02:41Z",
                    total: 60_672,
                    input: 60_605,
                    cachedInput: 11_008,
                    output: 67
                ),
                tokenLine(
                    timestamp: "2026-09-01T09:07:20Z",
                    total: 60_726,
                    input: 60_680,
                    cachedInput: 60_160,
                    output: 46
                ),
            ],
            modifiedAt: now
        )

        let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .readToday(now: now, calendar: calendar)

        XCTAssertEqual(usage?.tokens, 60_726)
        XCTAssertEqual(usage?.breakdown?.regularInputTokens, 520)
        XCTAssertEqual(usage?.breakdown?.cachedInputTokens, 60_160)
        XCTAssertEqual(usage?.breakdown?.outputTokens, 46)
        XCTAssertEqual(usage?.breakdown?.totalTokens, usage?.tokens)
    }

    func testTodayUsageRestartsBreakdownAtCounterReset() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let now = calendar.date(from: DateComponents(year: 2026, month: 9, day: 3, hour: 12))!
        try writeSession(
            at: root.appendingPathComponent("reset.jsonl"),
            lines: [
                tokenLine(timestamp: "2026-09-03T01:00:00Z", total: 100, input: 80, cachedInput: 30, output: 20),
                tokenLine(timestamp: "2026-09-03T02:00:00Z", total: 40, input: 32, cachedInput: 12, output: 8),
                tokenLine(timestamp: "2026-09-03T03:00:00Z", total: 60, input: 48, cachedInput: 18, output: 12),
            ],
            modifiedAt: now
        )

        let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .readToday(now: now, calendar: calendar)

        XCTAssertEqual(usage?.tokens, 160)
        XCTAssertEqual(usage?.breakdown?.regularInputTokens, 80)
        XCTAssertEqual(usage?.breakdown?.cachedInputTokens, 48)
        XCTAssertEqual(usage?.breakdown?.outputTokens, 32)
    }

    func testRequestedDayUsesThatLocalCalendarBoundary() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let requestedDay = calendar.date(
            from: DateComponents(year: 2026, month: 8, day: 29, hour: 12)
        )!
        let modifiedAt = calendar.date(
            from: DateComponents(year: 2026, month: 8, day: 30, hour: 12)
        )!
        try writeSession(
            at: root.appendingPathComponent("cross-day.jsonl"),
            lines: [
                tokenLine(
                    timestamp: "2026-08-28T15:59:59Z",
                    total: 100,
                    input: 90,
                    cachedInput: 60,
                    output: 10
                ),
                tokenLine(
                    timestamp: "2026-08-28T16:00:10Z",
                    total: 140,
                    input: 125,
                    cachedInput: 85,
                    output: 15
                ),
                tokenLine(
                    timestamp: "2026-08-29T15:59:59Z",
                    total: 180,
                    input: 160,
                    cachedInput: 110,
                    output: 20
                ),
                tokenLine(
                    timestamp: "2026-08-29T16:00:10Z",
                    total: 220,
                    input: 195,
                    cachedInput: 135,
                    output: 25
                ),
            ],
            modifiedAt: modifiedAt
        )

        let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .read(on: requestedDay, calendar: calendar)

        XCTAssertEqual(usage?.startDate, "2026-08-29")
        XCTAssertEqual(usage?.tokens, 80)
        XCTAssertEqual(usage?.breakdown?.regularInputTokens, 20)
        XCTAssertEqual(usage?.breakdown?.cachedInputTokens, 50)
        XCTAssertEqual(usage?.breakdown?.outputTokens, 10)
    }

    func testHistoryReturnsRecentNaturalDaysIncludingZeroUsage() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let now = calendar.date(from: DateComponents(year: 2026, month: 8, day: 30, hour: 12))!
        try writeSession(
            at: root.appendingPathComponent("history.jsonl"),
            lines: [
                tokenLine(
                    timestamp: "2026-08-27T15:59:59Z",
                    total: 100,
                    input: 90,
                    cachedInput: 60,
                    output: 10
                ),
                tokenLine(
                    timestamp: "2026-08-27T16:00:10Z",
                    total: 140,
                    input: 125,
                    cachedInput: 85,
                    output: 15
                ),
                tokenLine(
                    timestamp: "2026-08-28T15:00:00Z",
                    total: 180,
                    input: 160,
                    cachedInput: 110,
                    output: 20
                ),
                tokenLine(
                    timestamp: "2026-08-29T16:00:10Z",
                    total: 220,
                    input: 195,
                    cachedInput: 135,
                    output: 25
                ),
            ],
            modifiedAt: now
        )

        let history = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .readHistory(through: now, dayCount: 3, calendar: calendar)

        XCTAssertEqual(history?.map(\.startDate), ["2026-08-28", "2026-08-29", "2026-08-30"])
        XCTAssertEqual(history?.map(\.tokens), [80, 0, 40])
        XCTAssertEqual(history?.first?.breakdown?.regularInputTokens, 20)
        XCTAssertEqual(history?.first?.breakdown?.cachedInputTokens, 50)
        XCTAssertEqual(history?.first?.breakdown?.outputTokens, 10)
        XCTAssertEqual(history?.last?.breakdown?.totalTokens, 40)
    }

    func testMirroredSubagentLineageIsCountedOnce() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let now = calendar.date(from: DateComponents(year: 2026, month: 9, day: 3, hour: 12))!
        let rootID = "12345678-1234-1234-1234-123456789abc"
        let childID = "22345678-1234-1234-1234-123456789abc"
        let independentID = "32345678-1234-1234-1234-123456789abc"
        try writeSession(
            at: root.appendingPathComponent("rollout-\(rootID).jsonl"),
            lines: [
                sessionMetaLine(id: rootID),
                tokenLine(timestamp: "2026-09-03T01:00:00Z", total: 100, input: 80, cachedInput: 30, output: 20),
                tokenLine(timestamp: "2026-09-03T02:00:00Z", total: 200, input: 160, cachedInput: 60, output: 40),
                tokenLine(timestamp: "2026-09-03T03:00:00Z", total: 300, input: 240, cachedInput: 90, output: 60),
            ],
            modifiedAt: now
        )
        try writeSession(
            at: root.appendingPathComponent("rollout-\(childID).jsonl"),
            lines: [
                sessionMetaLine(id: childID, parentID: rootID),
                tokenLine(timestamp: "2026-09-03T02:00:01Z", total: 200, input: 160, cachedInput: 60, output: 40),
                tokenLine(timestamp: "2026-09-03T03:00:01Z", total: 300, input: 240, cachedInput: 90, output: 60),
                tokenLine(timestamp: "2026-09-03T04:00:00Z", total: 350, input: 280, cachedInput: 105, output: 70),
            ],
            modifiedAt: now
        )
        try writeSession(
            at: root.appendingPathComponent("rollout-\(independentID).jsonl"),
            lines: [
                sessionMetaLine(id: independentID),
                tokenLine(timestamp: "2026-09-03T05:00:00Z", total: 50, input: 40, cachedInput: 15, output: 10),
            ],
            modifiedAt: now
        )

        let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .readToday(now: now, calendar: calendar)

        XCTAssertEqual(usage?.tokens, 400)
        XCTAssertEqual(usage?.breakdown?.regularInputTokens, 200)
        XCTAssertEqual(usage?.breakdown?.cachedInputTokens, 120)
        XCTAssertEqual(usage?.breakdown?.outputTokens, 80)
    }

    func testMirroredLineageKeepsThePreviousDayBaseline() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("ksf-assistant-\(UUID().uuidString)", isDirectory: true)
        try fileManager.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? fileManager.removeItem(at: root) }

        let calendar = shanghaiCalendar()
        let now = calendar.date(from: DateComponents(year: 2026, month: 9, day: 3, hour: 12))!
        let rootID = "42345678-1234-1234-1234-123456789abc"
        let childID = "52345678-1234-1234-1234-123456789abc"
        try writeSession(
            at: root.appendingPathComponent("rollout-\(rootID).jsonl"),
            lines: [
                sessionMetaLine(id: rootID),
                tokenLine(timestamp: "2026-09-02T15:00:00Z", total: 100, input: 80, cachedInput: 30, output: 20),
                tokenLine(timestamp: "2026-09-03T01:00:00Z", total: 150, input: 120, cachedInput: 45, output: 30),
            ],
            modifiedAt: now
        )
        try writeSession(
            at: root.appendingPathComponent("rollout-\(childID).jsonl"),
            lines: [
                sessionMetaLine(id: childID, parentID: rootID),
                tokenLine(timestamp: "2026-09-02T15:00:01Z", total: 100, input: 80, cachedInput: 30, output: 20),
                tokenLine(timestamp: "2026-09-03T01:00:01Z", total: 150, input: 120, cachedInput: 45, output: 30),
                tokenLine(timestamp: "2026-09-03T02:00:00Z", total: 180, input: 144, cachedInput: 54, output: 36),
            ],
            modifiedAt: now
        )

        let usage = LocalTokenUsageReader(sessionRoot: root, fileManager: fileManager)
            .readToday(now: now, calendar: calendar)

        XCTAssertEqual(usage?.tokens, 80)
        XCTAssertEqual(usage?.breakdown?.totalTokens, 80)
    }

    func testMissingSessionRootReturnsUnavailable() {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("missing-\(UUID().uuidString)")
        XCTAssertNil(LocalTokenUsageReader(sessionRoot: root).readToday())
    }

    func testAccountTodayRequiresExactDateBucket() {
        let response = TokenUsageResponse(
            summary: TokenUsageSummary(lifetimeTokens: 100),
            dailyUsageBuckets: [DailyUsageBucket(startDate: "2026-08-29", tokens: 40)]
        )
        XCTAssertNil(response.dailyUsage(on: "2026-08-30"))
        XCTAssertEqual(response.dailyUsage(on: "2026-08-29")?.tokens, 40)
    }

    private func shanghaiCalendar() -> Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "Asia/Shanghai")!
        return calendar
    }

    private func tokenLine(
        timestamp: String,
        total: Int64,
        input: Int64? = nil,
        cachedInput: Int64? = nil,
        output: Int64? = nil
    ) -> String {
        let breakdown: String
        if let input, let cachedInput, let output {
            breakdown = #", "input_tokens":\#(input), "cached_input_tokens":\#(cachedInput), "output_tokens":\#(output)"#
        } else {
            breakdown = ""
        }
        return #"{"timestamp":"\#(timestamp)","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":\#(total)\#(breakdown)}}}}"#
    }

    private func sessionMetaLine(id: String, parentID: String? = nil) -> String {
        guard let parentID else {
            return #"{"type":"session_meta","payload":{"id":"\#(id)","source":"vscode"}}"#
        }
        return #"{"type":"session_meta","payload":{"id":"\#(id)","parent_thread_id":"\#(parentID)","forked_from_id":"\#(parentID)","source":{"subagent":{"thread_spawn":{"parent_thread_id":"\#(parentID)"}}}}}"#
    }

    private func writeSession(at url: URL, lines: [String], modifiedAt: Date) throws {
        try Data((lines.joined(separator: "\n") + "\n").utf8).write(to: url)
        try FileManager.default.setAttributes([.modificationDate: modifiedAt], ofItemAtPath: url.path)
    }
}
