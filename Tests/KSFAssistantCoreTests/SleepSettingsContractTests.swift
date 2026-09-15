import Foundation
import XCTest

final class SleepSettingsContractTests: XCTestCase {
    func testHostRestoresAndReleasesSleepRequestWithoutPopoverOwnership() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let model = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"))
        XCTAssertTrue(model.contains("setPreventSleep(defaults.bool(forKey: \"preventSleepEnabled\"))"))
        let shutdown = try XCTUnwrap(model.range(of: "func shutdown() async"))
        XCTAssertTrue(model[shutdown.lowerBound...].contains("try? sleepInhibitor.setEnabled(false)"))
        let view = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"))
        XCTAssertTrue(view.contains("title: \"禁止电脑睡眠\""))
        XCTAssertTrue(view.contains("viewModel.setPreventSleep($0)"))
    }
}
