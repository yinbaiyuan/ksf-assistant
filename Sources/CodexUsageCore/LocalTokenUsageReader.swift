import Foundation

public struct LocalTokenUsageReader {
    private struct CumulativeBreakdown {
        let inputTokens: Int64
        let cachedInputTokens: Int64
        let outputTokens: Int64
    }

    private struct TokenSample {
        let date: Date
        let totalTokens: Int64
        let breakdown: CumulativeBreakdown?
    }

    private struct TokenDelta {
        let date: Date
        let totalTokens: Int64
        let breakdown: TokenUsageBreakdown?
    }

    private struct UsageAccumulator {
        var totalTokens: Int64 = 0
        var regularInputTokens: Int64 = 0
        var cachedInputTokens: Int64 = 0
        var outputTokens: Int64 = 0
        var hasCompleteBreakdown = true

        mutating func add(totalDelta: Int64, breakdown: TokenUsageBreakdown?) {
            guard totalDelta >= 0 else { return }
            if totalDelta > 0 {
                totalTokens += totalDelta
            }
            guard let breakdown else {
                if totalDelta > 0 {
                    hasCompleteBreakdown = false
                }
                return
            }
            regularInputTokens += breakdown.regularInputTokens
            cachedInputTokens += breakdown.cachedInputTokens
            outputTokens += breakdown.outputTokens
        }

        mutating func merge(_ other: UsageAccumulator) {
            totalTokens += other.totalTokens
            regularInputTokens += other.regularInputTokens
            cachedInputTokens += other.cachedInputTokens
            outputTokens += other.outputTokens
            if other.totalTokens > 0, !other.hasCompleteBreakdown {
                hasCompleteBreakdown = false
            }
        }

        var breakdown: TokenUsageBreakdown? {
            guard hasCompleteBreakdown,
                  regularInputTokens >= 0,
                  cachedInputTokens >= 0,
                  outputTokens >= 0
            else { return nil }
            let result = TokenUsageBreakdown(
                regularInputTokens: regularInputTokens,
                cachedInputTokens: cachedInputTokens,
                outputTokens: outputTokens
            )
            guard result.totalTokens == totalTokens else { return nil }
            return result
        }
    }

    private let sessionRoots: [URL]
    private let fileManager: FileManager
    private let tokenCountNeedle = Data(#""token_count""#.utf8)

    public init(
        sessionRoot: URL? = nil,
        fileManager: FileManager = .default
    ) {
        self.fileManager = fileManager
        if let sessionRoot {
            self.sessionRoots = [sessionRoot]
        } else {
            let codexRoot = fileManager.homeDirectoryForCurrentUser
                .appendingPathComponent(".codex", isDirectory: true)
            self.sessionRoots = [
                codexRoot.appendingPathComponent("sessions", isDirectory: true),
                codexRoot.appendingPathComponent("archived_sessions", isDirectory: true),
            ]
        }
    }

    public init(
        sessionRoots: [URL],
        fileManager: FileManager = .default
    ) {
        self.fileManager = fileManager
        self.sessionRoots = sessionRoots
    }

    public func readToday(
        now: Date = Date(),
        calendar: Calendar = .current
    ) -> DailyUsageBucket? {
        read(on: now, calendar: calendar)
    }

    public func read(
        on day: Date,
        calendar: Calendar = .current
    ) -> DailyUsageBucket? {
        guard
            let endOfDay = calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: day))
        else {
            return nil
        }

        let startOfDay = calendar.startOfDay(for: day)
        guard let files = sessionFiles(modifiedOnOrAfter: startOfDay) else { return nil }

        var usage = UsageAccumulator()
        for fileURL in files {
            usage.merge(tokensUsed(
                in: fileURL,
                from: startOfDay,
                to: endOfDay
            ))
        }

        return DailyUsageBucket(
            startDate: Self.dateString(for: day, calendar: calendar),
            tokens: usage.totalTokens,
            breakdown: usage.breakdown
        )
    }

    public func readHistory(
        through day: Date = Date(),
        dayCount: Int = 30,
        calendar: Calendar = .current
    ) -> [DailyUsageBucket]? {
        guard dayCount > 0 else { return [] }

        let latestStart = calendar.startOfDay(for: day)
        guard
            let earliestStart = calendar.date(
                byAdding: .day,
                value: -(dayCount - 1),
                to: latestStart
            ),
            let end = calendar.date(byAdding: .day, value: 1, to: latestStart)
        else {
            return nil
        }

        var dayStarts: [Date] = []
        for offset in 0..<dayCount {
            guard let start = calendar.date(byAdding: .day, value: offset, to: earliestStart) else {
                return nil
            }
            dayStarts.append(start)
        }

        guard let files = sessionFiles(modifiedOnOrAfter: earliestStart) else { return nil }

        var usageByDate: [String: UsageAccumulator] = [:]
        for fileURL in files {
            let fileUsage = tokensUsedByDay(
                in: fileURL,
                from: earliestStart,
                to: end,
                calendar: calendar
            )
            for (date, usage) in fileUsage {
                var accumulated = usageByDate[date] ?? UsageAccumulator()
                accumulated.merge(usage)
                usageByDate[date] = accumulated
            }
        }

        return dayStarts.map { start in
            let date = Self.dateString(for: start, calendar: calendar)
            let usage = usageByDate[date] ?? UsageAccumulator()
            return DailyUsageBucket(
                startDate: date,
                tokens: usage.totalTokens,
                breakdown: usage.breakdown
            )
        }
    }

    private func sessionFiles(modifiedOnOrAfter earliestDate: Date) -> [URL]? {
        let keys: Set<URLResourceKey> = [
            .isRegularFileKey,
            .contentModificationDateKey,
            .fileSizeKey,
        ]
        var foundRoot = false
        var selected: [String: (url: URL, size: Int, modifiedAt: Date)] = [:]

        for root in sessionRoots {
            var isDirectory: ObjCBool = false
            guard fileManager.fileExists(atPath: root.path, isDirectory: &isDirectory),
                  isDirectory.boolValue
            else { continue }
            foundRoot = true
            guard let enumerator = fileManager.enumerator(
                at: root,
                includingPropertiesForKeys: Array(keys),
                options: [.skipsHiddenFiles]
            ) else { continue }

            for case let fileURL as URL in enumerator where fileURL.pathExtension == "jsonl" {
                guard let values = try? fileURL.resourceValues(forKeys: keys),
                      values.isRegularFile != false
                else { continue }
                let modifiedAt = values.contentModificationDate ?? .distantPast
                guard modifiedAt >= earliestDate else { continue }
                let candidate = (fileURL, values.fileSize ?? 0, modifiedAt)
                let identity = sessionIdentity(for: fileURL)
                if let existing = selected[identity],
                   existing.size > candidate.1
                    || (existing.size == candidate.1 && existing.modifiedAt >= candidate.2)
                {
                    continue
                }
                selected[identity] = candidate
            }
        }

        guard foundRoot else { return nil }
        return selected.values.map(\.url).sorted { $0.path < $1.path }
    }

    private func sessionIdentity(for fileURL: URL) -> String {
        let stem = fileURL.deletingPathExtension().lastPathComponent
        let suffix = String(stem.suffix(36))
        let parts = suffix.split(separator: "-", omittingEmptySubsequences: false)
        if parts.map(\.count) == [8, 4, 4, 4, 12] {
            return suffix.lowercased()
        }
        return stem
    }

    public static func dateString(for date: Date, calendar: Calendar = .current) -> String {
        let parts = calendar.dateComponents([.year, .month, .day], from: date)
        guard let year = parts.year, let month = parts.month, let day = parts.day else {
            return ""
        }
        return String(format: "%04d-%02d-%02d", year, month, day)
    }

    private func tokensUsed(in fileURL: URL, from start: Date, to end: Date) -> UsageAccumulator {
        var accumulated = UsageAccumulator()
        for delta in tokenDeltas(in: fileURL, from: start, to: end) {
            accumulated.add(totalDelta: delta.totalTokens, breakdown: delta.breakdown)
        }
        return accumulated
    }

    private func tokensUsedByDay(
        in fileURL: URL,
        from start: Date,
        to end: Date,
        calendar: Calendar
    ) -> [String: UsageAccumulator] {
        var usageByDate: [String: UsageAccumulator] = [:]
        for delta in tokenDeltas(in: fileURL, from: start, to: end) {
            let date = Self.dateString(for: delta.date, calendar: calendar)
            var usage = usageByDate[date] ?? UsageAccumulator()
            usage.add(totalDelta: delta.totalTokens, breakdown: delta.breakdown)
            usageByDate[date] = usage
        }
        return usageByDate
    }

    private func tokenDeltas(in fileURL: URL, from start: Date, to end: Date) -> [TokenDelta] {
        // Session files can remain active for days and grow very large. The token counter is
        // cumulative, so search backward and stop after the last sample before the requested
        // range instead of parsing historical conversation events from the beginning.
        guard let data = try? Data(contentsOf: fileURL, options: .mappedIfSafe) else {
            return []
        }

        var reverseSamples: [TokenSample] = []
        var baseline: TokenSample?
        var searchEnd = data.endIndex

        while searchEnd > data.startIndex,
              let match = data.range(
                of: tokenCountNeedle,
                options: .backwards,
                in: data.startIndex..<searchEnd
              ) {
            let lineStart = data[..<match.lowerBound].lastIndex(of: 0x0A)
                .map { data.index(after: $0) }
                ?? data.startIndex
            let lineEnd = data[match.upperBound...].firstIndex(of: 0x0A) ?? data.endIndex

            if let sample = tokenSample(from: Data(data[lineStart..<lineEnd])) {
                if sample.date < start {
                    baseline = sample
                    break
                }
                if sample.date < end {
                    reverseSamples.append(sample)
                }
            }

            guard lineStart > data.startIndex else { break }
            searchEnd = lineStart
        }

        var previous = baseline
        var deltas: [TokenDelta] = []
        for sample in reverseSamples.reversed() {
            let totalDelta: Int64
            if let previous {
                totalDelta = max(0, sample.totalTokens - previous.totalTokens)
            } else {
                totalDelta = max(0, sample.totalTokens)
            }
            deltas.append(TokenDelta(
                date: sample.date,
                totalTokens: totalDelta,
                breakdown: breakdownDelta(
                    current: sample,
                    previous: previous,
                    totalDelta: totalDelta
                )
            ))
            previous = sample
        }
        return deltas
    }

    private func breakdownDelta(
        current: TokenSample,
        previous: TokenSample?,
        totalDelta: Int64
    ) -> TokenUsageBreakdown? {
        guard let currentBreakdown = current.breakdown else { return nil }

        let inputDelta: Int64
        let cachedInputDelta: Int64
        let outputDelta: Int64
        if let previous {
            guard let previousBreakdown = previous.breakdown else { return nil }
            // Codex can revise the cumulative split without changing the cumulative total,
            // for example by reclassifying previously ordinary input as cached input. Keep
            // those signed component deltas so the final daily composition still reconciles.
            inputDelta = currentBreakdown.inputTokens - previousBreakdown.inputTokens
            cachedInputDelta = currentBreakdown.cachedInputTokens - previousBreakdown.cachedInputTokens
            outputDelta = currentBreakdown.outputTokens - previousBreakdown.outputTokens
        } else {
            inputDelta = max(0, currentBreakdown.inputTokens)
            cachedInputDelta = max(0, currentBreakdown.cachedInputTokens)
            outputDelta = max(0, currentBreakdown.outputTokens)
        }

        let regularInputDelta = inputDelta - cachedInputDelta
        let breakdown = TokenUsageBreakdown(
            regularInputTokens: regularInputDelta,
            cachedInputTokens: cachedInputDelta,
            outputTokens: outputDelta
        )
        guard breakdown.totalTokens == totalDelta else { return nil }
        return breakdown
    }

    private func tokenSample(from line: Data) -> TokenSample? {
        guard
            let object = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
            let timestamp = object["timestamp"] as? String,
            let payload = object["payload"] as? [String: Any],
            payload["type"] as? String == "token_count",
            let info = payload["info"] as? [String: Any],
            let usage = info["total_token_usage"] as? [String: Any],
            let total = usage["total_tokens"] as? NSNumber,
            let date = parseTimestamp(timestamp)
        else {
            return nil
        }
        let breakdown: CumulativeBreakdown?
        if
            let input = usage["input_tokens"] as? NSNumber,
            let cachedInput = usage["cached_input_tokens"] as? NSNumber,
            let output = usage["output_tokens"] as? NSNumber
        {
            breakdown = CumulativeBreakdown(
                inputTokens: input.int64Value,
                cachedInputTokens: cachedInput.int64Value,
                outputTokens: output.int64Value
            )
        } else {
            breakdown = nil
        }
        return TokenSample(
            date: date,
            totalTokens: total.int64Value,
            breakdown: breakdown
        )
    }

    private func parseTimestamp(_ value: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = fractional.date(from: value) { return date }

        let standard = ISO8601DateFormatter()
        standard.formatOptions = [.withInternetDateTime]
        return standard.date(from: value)
    }
}
