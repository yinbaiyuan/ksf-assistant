import Foundation
import XCTest

final class ActivityRefreshContractTests: XCTestCase {
    func testActivityMonitorIsIndependentOfPopoverAndCancelledOnShutdown() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"), encoding: .utf8)
        let start = try XCTUnwrap(source.range(of: "private func startCoreServiceTimers()"))
        let end = try XCTUnwrap(source.range(of: "private func startCoreServicePolling()"))
        let monitor = source[start.lowerBound..<end.lowerBound]
        XCTAssertTrue(monitor.contains("activityRevision()"))
        XCTAssertTrue(monitor.contains("1_000_000_000"))
        XCTAssertTrue(monitor.contains("revision != lastRevision"))
        XCTAssertFalse(monitor.contains("popoverIsOpen"))
        XCTAssertTrue(source.contains("activityMonitorTask?.cancel()"))
        XCTAssertTrue(source.contains("pendingActivityRefresh = true"))
    }
}
