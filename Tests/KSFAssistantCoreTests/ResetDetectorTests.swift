import XCTest
@testable import KSFAssistantCore

final class ResetDetectorTests: XCTestCase {
    func testCrossedGeneralResetNotifiesOnce() {
        let previous = bucket(id: "codex", used: 80, duration: 300, reset: 1_000)
        let current = bucket(id: "codex", used: 5, duration: 300, reset: 2_000)

        let first = ResetDetector.detect(
            previous: previous,
            current: current,
            previousCapturedAt: Date(timeIntervalSince1970: 900),
            currentCapturedAt: Date(timeIntervalSince1970: 1_010),
            notifiedResetTimestamps: []
        )
        XCTAssertEqual(first.map(\.resetTimestamp), [1_000])

        let duplicate = ResetDetector.detect(
            previous: previous,
            current: current,
            previousCapturedAt: Date(timeIntervalSince1970: 900),
            currentCapturedAt: Date(timeIntervalSince1970: 1_010),
            notifiedResetTimestamps: [1_000]
        )
        XCTAssertTrue(duplicate.isEmpty)
    }

    func testServerRescheduleBeforeResetDoesNotNotify() {
        let previous = bucket(id: "codex", used: 80, duration: 300, reset: 1_000)
        let current = bucket(id: "codex", used: 40, duration: 300, reset: 2_000)

        let events = ResetDetector.detect(
            previous: previous,
            current: current,
            previousCapturedAt: Date(timeIntervalSince1970: 900),
            currentCapturedAt: Date(timeIntervalSince1970: 950),
            notifiedResetTimestamps: []
        )
        XCTAssertTrue(events.isEmpty)
    }

    func testModelSpecificResetNeverNotifies() {
        let events = ResetDetector.detect(
            previous: bucket(id: "codex_model", used: 80, duration: 300, reset: 1_000),
            current: bucket(id: "codex_model", used: 0, duration: 300, reset: 2_000),
            previousCapturedAt: Date(timeIntervalSince1970: 900),
            currentCapturedAt: Date(timeIntervalSince1970: 1_010),
            notifiedResetTimestamps: []
        )
        XCTAssertTrue(events.isEmpty)
    }

    private func bucket(id: String, used: Int, duration: Int64, reset: Int64) -> RateLimitBucket {
        RateLimitBucket(
            limitId: id,
            primary: RateLimitWindow(
                usedPercent: used,
                windowDurationMins: duration,
                resetsAt: reset
            )
        )
    }
}
