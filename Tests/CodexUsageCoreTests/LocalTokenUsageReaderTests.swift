import XCTest
@testable import CodexUsageCore

final class LocalTokenUsageReaderTests: XCTestCase {
    func testHistorySeriesSortsDaysAndDerivesComparableSummary() {
        let series = LocalTokenHistorySeries(days: [
            DailyUsageBucket(startDate: "2026-08-30", tokens: 20),
            DailyUsageBucket(startDate: "2026-08-28", tokens: 0),
            DailyUsageBucket(startDate: "2026-08-29", tokens: 80),
        ])

        XCTAssertEqual(series.days.map(\.startDate), ["2026-08-28", "2026-08-29", "2026-08-30"])
        XCTAssertEqual(series.totalTokens, 100)
        XCTAssertEqual(series.averageTokens, 33)
        XCTAssertEqual(series.activeDayCount, 2)
        XCTAssertEqual(series.maximumTokens, 80)
        XCTAssertEqual(series.latestDay?.startDate, "2026-08-30")
        XCTAssertEqual(series.usage(on: "2026-08-29")?.tokens, 80)
    }

    func testTodayUsageSumsPositiveDeltasAcrossLocalSessions() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("codex-usage-bar-\(UUID().uuidString)", isDirectory: true)
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
            .appendingPathComponent("codex-usage-bar-\(UUID().uuidString)", isDirectory: true)
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

    func testRequestedDayUsesThatLocalCalendarBoundary() throws {
        let fileManager = FileManager.default
        let root = fileManager.temporaryDirectory
            .appendingPathComponent("codex-usage-bar-\(UUID().uuidString)", isDirectory: true)
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
            .appendingPathComponent("codex-usage-bar-\(UUID().uuidString)", isDirectory: true)
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

    private func writeSession(at url: URL, lines: [String], modifiedAt: Date) throws {
        try Data((lines.joined(separator: "\n") + "\n").utf8).write(to: url)
        try FileManager.default.setAttributes([.modificationDate: modifiedAt], ofItemAtPath: url.path)
    }
}
