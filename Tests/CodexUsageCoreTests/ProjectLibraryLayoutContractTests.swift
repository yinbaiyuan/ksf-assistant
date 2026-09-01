import Foundation
import XCTest

final class ProjectLibraryLayoutContractTests: XCTestCase {
    func testProjectLibraryUsesNaturalHeightWithoutInternalScrolling() throws {
        let repositoryRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let sourceURL = repositoryRoot
            .appendingPathComponent("Sources/CodexUsageBar/UsagePopoverView.swift")
        let source = try String(contentsOf: sourceURL, encoding: .utf8)
        let section = try projectLibrarySection(in: source)

        XCTAssertTrue(section.contains("VStack(spacing: 0)"))
        XCTAssertFalse(section.contains("ScrollView"))
        XCTAssertFalse(section.contains("LazyVStack"))
        XCTAssertFalse(section.contains(".frame(height:"))
        XCTAssertFalse(section.contains("OverlayScrollerConfigurator"))
        XCTAssertFalse(source.contains("projectLibraryViewportHeight"))
        XCTAssertFalse(section.contains("projectLibraryStatus(item)"))
        XCTAssertTrue(section.contains("projectActionFooter(project: project, item: item)"))
        XCTAssertEqual(
            source.components(separatedBy: "projectActionFooter(project: project, item: item)").count - 1,
            2
        )
        let archiveButton = try XCTUnwrap(
            section.range(of: "projectArchiveTaskIconControl(project: project)")
        )
        let pinButton = try XCTUnwrap(
            section.range(of: "systemName: item.isPinned ? \"pin.fill\" : \"pin\"")
        )
        XCTAssertLessThan(archiveButton.lowerBound, pinButton.lowerBound)
        XCTAssertEqual(
            source.components(separatedBy: "projectArchiveTaskIconControl(project: project)").count - 1,
            1
        )
    }

    private func projectLibrarySection(in source: String) throws -> Substring {
        let start = try XCTUnwrap(source.range(of: "private var projectLibraryPage"))
        let end = try XCTUnwrap(
            source.range(of: "private var settingsPage", range: start.upperBound..<source.endIndex)
        )
        return source[start.lowerBound..<end.lowerBound]
    }
}
