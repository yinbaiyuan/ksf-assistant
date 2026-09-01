public struct LocalTokenHistorySeries: Equatable {
    public let days: [DailyUsageBucket]

    public init(days: [DailyUsageBucket]) {
        self.days = days.sorted { $0.startDate < $1.startDate }
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
