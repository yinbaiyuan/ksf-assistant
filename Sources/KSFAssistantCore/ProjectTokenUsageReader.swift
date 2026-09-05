import Foundation

public struct ProjectTokenUsageReader {
    private let fileManager: FileManager

    public init(fileManager: FileManager = .default) {
        self.fileManager = fileManager
    }

    public func read(
        projectIDs: [String],
        threads: [CodexThreadMetadata],
        timelines: [ProjectTaskTimeline],
        trackingStartedAt: Date,
        now: Date = Date(),
        calendar: Calendar = .current
    ) -> [String: ProjectUsageSummary] {
        let timelineByThread = Dictionary(uniqueKeysWithValues: timelines.map { ($0.threadID, $0.transitions) })
        let metadataByID = Dictionary(uniqueKeysWithValues: threads.map { ($0.id, $0) })
        let startOfDay = calendar.startOfDay(for: now)
        var accumulators = Dictionary(uniqueKeysWithValues: projectIDs.map {
            ($0, Accumulator(since: trackingStartedAt))
        })

        for thread in threads {
            guard let transitions = inheritedTimeline(
                for: thread.id,
                timelines: timelineByThread,
                metadata: metadataByID
            ), !transitions.isEmpty else { continue }

            let affectedProjects = Set(transitions.map(\.projectID))
            guard let path = thread.path else {
                markIncomplete(affectedProjects, in: &accumulators)
                continue
            }
            let url = URL(fileURLWithPath: path)
            guard fileManager.fileExists(atPath: url.path), let samples = tokenSamples(in: url) else {
                markIncomplete(affectedProjects, in: &accumulators)
                continue
            }

            var previous: Int64?
            var previousDate: Date?
            for sample in samples {
                let delta: Int64
                if let previous {
                    delta = sample.totalTokens >= previous
                        ? sample.totalTokens - previous
                        : sample.totalTokens
                } else {
                    delta = sample.totalTokens
                }
                let hadBaseline = previousDate != nil
                previous = sample.totalTokens
                previousDate = sample.date
                guard delta > 0, let transition = transitions.last(where: { $0.boundAt <= sample.date }) else {
                    continue
                }
                var accumulator = accumulators[transition.projectID] ?? Accumulator(since: transition.boundAt)
                let threadCreatedAt = Date(timeIntervalSince1970: TimeInterval(thread.createdAt))
                if !hadBaseline && transition.boundAt > threadCreatedAt {
                    accumulator.uncounted += 1
                    accumulator.since = min(accumulator.since, transition.boundAt)
                    accumulators[transition.projectID] = accumulator
                    continue
                }
                accumulator.total += delta
                if sample.date >= startOfDay && sample.date <= now { accumulator.today += delta }
                accumulator.since = min(accumulator.since, transition.boundAt)
                accumulators[transition.projectID] = accumulator
            }
        }

        return accumulators.mapValues {
            ProjectUsageSummary(
                cumulativeTokens: $0.total,
                todayTokens: $0.today,
                trackingStartedAt: $0.since,
                isComplete: $0.uncounted == 0,
                uncountedThreadCount: $0.uncounted
            )
        }
    }

    private func inheritedTimeline(
        for threadID: String,
        timelines: [String: [ProjectBindingTransition]],
        metadata: [String: CodexThreadMetadata]
    ) -> [ProjectBindingTransition]? {
        var current: String? = threadID
        var visited = Set<String>()
        while let id = current, visited.insert(id).inserted {
            if let timeline = timelines[id] { return timeline }
            current = metadata[id]?.parentThreadId
        }
        return nil
    }

    private func markIncomplete(_ projects: Set<String>, in values: inout [String: Accumulator]) {
        for project in projects {
            var accumulator = values[project] ?? Accumulator(since: Date())
            accumulator.uncounted += 1
            values[project] = accumulator
        }
    }

    private func tokenSamples(in url: URL) -> [Sample]? {
        guard let data = try? Data(contentsOf: url, options: .mappedIfSafe) else { return nil }
        var samples: [Sample] = []
        data.enumerateLines { line in
            guard line.contains(#""token_count""#), let sample = tokenSample(from: line) else { return }
            samples.append(sample)
        }
        return samples.sorted { $0.date < $1.date }
    }

    private func tokenSample(from line: String) -> Sample? {
        guard
            let data = line.data(using: .utf8),
            let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let timestamp = object["timestamp"] as? String,
            let payload = object["payload"] as? [String: Any],
            payload["type"] as? String == "token_count",
            let info = payload["info"] as? [String: Any],
            let usage = info["total_token_usage"] as? [String: Any],
            let total = usage["total_tokens"] as? NSNumber,
            let date = Self.parseTimestamp(timestamp)
        else { return nil }
        return Sample(date: date, totalTokens: total.int64Value)
    }

    private static func parseTimestamp(_ value: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = fractional.date(from: value) { return date }
        let standard = ISO8601DateFormatter()
        standard.formatOptions = [.withInternetDateTime]
        return standard.date(from: value)
    }

    private struct Sample {
        let date: Date
        let totalTokens: Int64
    }

    private struct Accumulator {
        var total: Int64 = 0
        var today: Int64 = 0
        var since: Date
        var uncounted: Int = 0
    }
}

private extension Data {
    func enumerateLines(_ body: (String) -> Void) {
        var start = startIndex
        while start < endIndex {
            let end = self[start...].firstIndex(of: 0x0A) ?? endIndex
            if let line = String(data: self[start..<end], encoding: .utf8) { body(line) }
            if end == endIndex { break }
            start = index(after: end)
        }
    }
}
