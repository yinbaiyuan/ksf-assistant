import Foundation
import XCTest

final class PricingMenuLayoutContractTests: XCTestCase {
    func testPricingSelectionUsesSharedInlineMenuWithoutTopForms() throws {
        let root = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsagePopoverView.swift"))
        XCTAssertEqual(source.components(separatedBy: "pricingPlanMenu").count - 1, 3,
                       "Home and the 30-day estimate must share one menu")
        XCTAssertTrue(source.contains(".menuIndicator(.hidden)"))
        XCTAssertTrue(source.contains("Image(systemName: \"arrowtriangle.down.fill\")"))
        XCTAssertTrue(source.contains("Text(pricingPlanCompactName) + Text(\" \") +"),
                      "The arrow must be an inline text attachment, not a native menu-leading image")
        XCTAssertTrue(source.contains(".font(.system(size: 7))"))
        XCTAssertTrue(source.contains(".accessibilityLabel(\"API 估算模型\")"))
        XCTAssertFalse(source.contains("Text(\"当前方案\")"))
        XCTAssertFalse(source.contains("\"API 价格方案\""))
        let start = try XCTUnwrap(source.range(of: "private var tokenHistoryPage"))
        let end = try XCTUnwrap(source.range(of: "private func localTokenHistoryDashboard"))
        XCTAssertFalse(source[start.lowerBound..<end.lowerBound].contains("Picker("))
        let estimateStart = try XCTUnwrap(source.range(of: "Text(\"30 日 API 估算\")"))
        let estimateEnd = try XCTUnwrap(source.range(of: ".padding(.horizontal, 7)", range: estimateStart.upperBound..<source.endIndex))
        let row = String(source[estimateStart.lowerBound..<estimateEnd.lowerBound])
        let menu = try XCTUnwrap(row.range(of: "pricingPlanMenu"))
        let spacer = try XCTUnwrap(row.range(of: "Spacer(minLength: 4)"))
        let amount = try XCTUnwrap(row.range(of: "Text(formattedCost("))
        XCTAssertLessThan(menu.lowerBound, spacer.lowerBound)
        XCTAssertLessThan(spacer.lowerBound, amount.lowerBound)
    }
}
