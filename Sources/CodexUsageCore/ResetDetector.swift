import Foundation

public struct ResetEvent: Equatable {
    public let windowDurationMins: Int64?
    public let resetTimestamp: Int64
    public let remainingPercent: Int

    public init(windowDurationMins: Int64?, resetTimestamp: Int64, remainingPercent: Int) {
        self.windowDurationMins = windowDurationMins
        self.resetTimestamp = resetTimestamp
        self.remainingPercent = remainingPercent
    }
}

public enum ResetDetector {
    public static func detect(
        previous: RateLimitBucket?,
        current: RateLimitBucket?,
        previousCapturedAt: Date?,
        currentCapturedAt: Date,
        notifiedResetTimestamps: Set<Int64>
    ) -> [ResetEvent] {
        guard
            let previous,
            let current,
            previous.limitId == "codex",
            current.limitId == "codex",
            let previousCapturedAt
        else {
            return []
        }

        return previous.windows.compactMap { oldWindow in
            guard
                oldWindow.usedPercent > 0,
                let oldReset = oldWindow.resetsAt,
                !notifiedResetTimestamps.contains(oldReset),
                previousCapturedAt.timeIntervalSince1970 < TimeInterval(oldReset),
                currentCapturedAt.timeIntervalSince1970 >= TimeInterval(oldReset),
                let newWindow = matchingWindow(for: oldWindow, in: current.windows),
                let newReset = newWindow.resetsAt,
                newReset > oldReset
            else {
                return nil
            }

            return ResetEvent(
                windowDurationMins: oldWindow.windowDurationMins,
                resetTimestamp: oldReset,
                remainingPercent: newWindow.remainingPercent
            )
        }
    }

    private static func matchingWindow(
        for previous: RateLimitWindow,
        in current: [RateLimitWindow]
    ) -> RateLimitWindow? {
        if let duration = previous.windowDurationMins {
            return current.first { $0.windowDurationMins == duration }
        }
        return current.first { $0.windowDurationMins == nil }
    }
}

