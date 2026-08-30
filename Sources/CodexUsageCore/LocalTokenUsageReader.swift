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

    private struct UsageAccumulator {
        var totalTokens: Int64 = 0
        var regularInputTokens: Int64 = 0
        var cachedInputTokens: Int64 = 0
        var outputTokens: Int64 = 0
        var hasCompleteBreakdown = true

        mutating func add(totalDelta: Int64, breakdown: TokenUsageBreakdown?) {
            guard totalDelta > 0 else { return }
            totalTokens += totalDelta
            guard let breakdown else {
                hasCompleteBreakdown = false
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
            guard hasCompleteBreakdown else { return nil }
            return TokenUsageBreakdown(
                regularInputTokens: regularInputTokens,
                cachedInputTokens: cachedInputTokens,
                outputTokens: outputTokens
            )
        }
    }

    private let sessionRoot: URL
    private let fileManager: FileManager
    private let tokenCountNeedle = Data(#""token_count""#.utf8)

    public init(
        sessionRoot: URL? = nil,
        fileManager: FileManager = .default
    ) {
        self.fileManager = fileManager
        self.sessionRoot = sessionRoot
            ?? fileManager.homeDirectoryForCurrentUser
                .appendingPathComponent(".codex/sessions", isDirectory: true)
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
        var isDirectory: ObjCBool = false
        guard
            fileManager.fileExists(atPath: sessionRoot.path, isDirectory: &isDirectory),
            isDirectory.boolValue,
            let endOfDay = calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: day))
        else {
            return nil
        }

        let startOfDay = calendar.startOfDay(for: day)
        let keys: [URLResourceKey] = [.isRegularFileKey, .contentModificationDateKey]
        guard let enumerator = fileManager.enumerator(
            at: sessionRoot,
            includingPropertiesForKeys: keys,
            options: [.skipsHiddenFiles]
        ) else {
            return nil
        }

        var usage = UsageAccumulator()
        for case let fileURL as URL in enumerator {
            guard fileURL.pathExtension == "jsonl" else { continue }
            let values = try? fileURL.resourceValues(forKeys: Set(keys))
            guard values?.isRegularFile != false else { continue }
            if let modifiedAt = values?.contentModificationDate, modifiedAt < startOfDay {
                continue
            }
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

    public static func dateString(for date: Date, calendar: Calendar = .current) -> String {
        let parts = calendar.dateComponents([.year, .month, .day], from: date)
        guard let year = parts.year, let month = parts.month, let day = parts.day else {
            return ""
        }
        return String(format: "%04d-%02d-%02d", year, month, day)
    }

    private func tokensUsed(in fileURL: URL, from start: Date, to end: Date) -> UsageAccumulator {
        // Session files can remain active for days and grow very large. The token counter is
        // cumulative, so search backward and stop after the last pre-midnight sample instead
        // of parsing historical conversation events from the beginning of the file.
        guard let data = try? Data(contentsOf: fileURL, options: .mappedIfSafe) else {
            return UsageAccumulator()
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
        var accumulated = UsageAccumulator()
        for sample in reverseSamples.reversed() {
            let totalDelta: Int64
            if let previous {
                totalDelta = max(0, sample.totalTokens - previous.totalTokens)
            } else {
                totalDelta = max(0, sample.totalTokens)
            }
            accumulated.add(
                totalDelta: totalDelta,
                breakdown: breakdownDelta(current: sample, previous: previous, totalDelta: totalDelta)
            )
            previous = sample
        }
        return accumulated
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
            inputDelta = max(0, currentBreakdown.inputTokens - previousBreakdown.inputTokens)
            cachedInputDelta = max(
                0,
                currentBreakdown.cachedInputTokens - previousBreakdown.cachedInputTokens
            )
            outputDelta = max(0, currentBreakdown.outputTokens - previousBreakdown.outputTokens)
        } else {
            inputDelta = max(0, currentBreakdown.inputTokens)
            cachedInputDelta = max(0, currentBreakdown.cachedInputTokens)
            outputDelta = max(0, currentBreakdown.outputTokens)
        }

        let regularInputDelta = max(0, inputDelta - cachedInputDelta)
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
