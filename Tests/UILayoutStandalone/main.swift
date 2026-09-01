import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

private func expect(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    guard condition() else { throw TestFailure(description: message) }
}

@main
private enum UILayoutTestRunner {
    static func main() {
        do {
            guard CommandLine.arguments.count == 2 else {
                throw TestFailure(description: "expected UsagePopoverView source path")
            }
            let source = try String(
                contentsOfFile: CommandLine.arguments[1],
                encoding: .utf8
            )
            guard
                let start = source.range(of: "private var projectLibraryPage"),
                let end = source.range(
                    of: "private var settingsPage",
                    range: start.upperBound..<source.endIndex
                )
            else {
                throw TestFailure(description: "project-library source section is missing")
            }
            let section = source[start.lowerBound..<end.lowerBound]

            try expect(section.contains("VStack(spacing: 0)"), "project catalog is not one continuous stack")
            try expect(!section.contains("ScrollView"), "project catalog still has an internal ScrollView")
            try expect(!section.contains("LazyVStack"), "project catalog still defers off-screen rows")
            try expect(!section.contains(".frame(height:"), "project catalog still has a height cap")
            try expect(!section.contains("OverlayScrollerConfigurator"), "project catalog still reserves a scroller gutter")
            try expect(!source.contains("projectLibraryViewportHeight"), "project catalog still computes a viewport height")
            try expect(!section.contains("projectLibraryStatus(item)"), "project catalog still shows a status subtitle")
            try expect(
                section.contains("projectActionFooter(project: project, item: item)"),
                "project catalog does not show the shared project action footer"
            )
            let sharedFooterCallCount = source.components(
                separatedBy: "projectActionFooter(project: project, item: item)"
            ).count - 1
            try expect(sharedFooterCallCount == 2, "home and catalog do not share one project action footer")
            guard
                let archiveButton = section.range(of: "projectArchiveTaskIconControl(project: project)"),
                let pinButton = section.range(of: "systemName: item.isPinned ? \"pin.fill\" : \"pin\"")
            else {
                throw TestFailure(description: "project catalog archive or pin control is missing")
            }
            try expect(archiveButton.lowerBound < pinButton.lowerBound, "archive control is not before the pin control")
            let archiveButtonCallCount = source.components(
                separatedBy: "projectArchiveTaskIconControl(project: project)"
            ).count - 1
            try expect(archiveButtonCallCount == 1, "archive control is not scoped to the project catalog")

            print("PASS project catalog layout, shared footer, and catalog-only archive control")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
