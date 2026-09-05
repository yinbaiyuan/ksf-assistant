import Foundation

public struct LocalTokenHistorySeries: Equatable {
    public let days: [DailyUsageBucket]

    public init(
        days: [DailyUsageBucket],
        through endDate: Date = Date(),
        calendar: Calendar = .current
    ) {
        var observedByDate: [String: DailyUsageBucket] = [:]
        for day in days {
            observedByDate[day.startDate] = day
        }

        let end = calendar.startOfDay(for: endDate)
        self.days = (0..<30).compactMap { offset in
            guard let date = calendar.date(byAdding: .day, value: offset - 29, to: end) else {
                return nil
            }
            let startDate = LocalTokenUsageReader.dateString(for: date, calendar: calendar)
            return observedByDate[startDate] ?? DailyUsageBucket(
                startDate: startDate,
                tokens: 0,
                breakdown: TokenUsageBreakdown(
                    regularInputTokens: 0,
                    cachedInputTokens: 0,
                    outputTokens: 0
                )
            )
        }
    }

    public var totalTokens: Int64 {
        days.reduce(0) { $0 + max(0, $1.tokens) }
    }

    public var averageTokens: Int64 {
        guard !days.isEmpty else { return 0 }
        return totalTokens / Int64(days.count)
    }

    public var activeDayCount: Int {
        days.filter { $0.tokens > 0 }.count
    }

    public var maximumTokens: Int64 {
        days.map(\.tokens).max().map { max(0, $0) } ?? 0
    }

    public var latestDay: DailyUsageBucket? {
        days.last
    }

    public func usage(on startDate: String) -> DailyUsageBucket? {
        days.first { $0.startDate == startDate }
    }
}

public struct TokenHistoryComparisonSeries: Equatable {
    public let days: [TokenHistoryComparisonDay]

    public init(days: [TokenHistoryComparisonDay]) {
        self.days = days.sorted { $0.startDate < $1.startDate }
    }

    public init(
        localDays: [DailyUsageBucket],
        serverDays: [DailyUsageBucket],
        through endDate: Date = Date(),
        calendar: Calendar = .current
    ) {
        let localSeries = LocalTokenHistorySeries(
            days: localDays,
            through: endDate,
            calendar: calendar
        )
        let serverByDate = Dictionary(
            serverDays.map { ($0.startDate, $0.tokens) },
            uniquingKeysWith: { _, latest in latest }
        )
        days = localSeries.days.map { local in
            TokenHistoryComparisonDay(
                startDate: local.startDate,
                serverTokens: serverByDate[local.startDate],
                localTokens: local.tokens,
                localBreakdown: local.breakdown
            )
        }
    }

    public var localTotalTokens: Int64 {
        days.reduce(0) { $0 + max(0, $1.localTokens) }
    }

    public var localAverageTokens: Int64 {
        guard !days.isEmpty else { return 0 }
        return localTotalTokens / Int64(days.count)
    }

    public var localActiveDayCount: Int {
        days.filter { $0.localTokens > 0 }.count
    }

    public var maximumTokens: Int64 {
        days.reduce(0) { maximum, day in
            max(maximum, max(max(0, day.localTokens), max(0, day.serverTokens ?? 0)))
        }
    }

    public var latestDay: TokenHistoryComparisonDay? {
        days.last
    }

    public func usage(on startDate: String) -> TokenHistoryComparisonDay? {
        days.first { $0.startDate == startDate }
    }

    public func localShare(on startDate: String) -> Double? {
        guard let usage = usage(on: startDate),
              let serverTokens = usage.serverTokens,
              serverTokens > 0 else { return nil }
        return Double(max(0, usage.localTokens)) / Double(serverTokens)
    }
}
