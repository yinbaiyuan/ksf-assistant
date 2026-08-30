import AppKit
import Foundation

private struct TestFailure: Error, CustomStringConvertible {
    let description: String
}

@main
private enum StatusItemImageTestRunner {
    static func main() {
        do {
            let image = StatusItemImageRenderer.render(
                codexIcon: nil,
                quotaText: "93%",
                localTokenText: "1234.5M",
                runningText: "2",
                waitingText: "0"
            )
            guard image.size.width >= 139 else {
                throw TestFailure(description: "status image collapsed to \(image.size.width)pt")
            }
            guard image.size.height == 18 else {
                throw TestFailure(description: "unexpected status image height \(image.size.height)pt")
            }
            let indicator = StatusItemImageRenderer.makeConnectionIndicator()
            guard indicator.frame.size == NSSize(width: 5, height: 5) else {
                throw TestFailure(description: "unexpected WeChat indicator size \(indicator.frame.size)")
            }
            guard indicator.layer?.backgroundColor == NSColor.systemGreen.cgColor,
                  indicator.layer?.cornerRadius == 2.5 else {
                throw TestFailure(description: "WeChat indicator is not a solid system-green circle")
            }
            guard let data = image.tiffRepresentation, let bitmap = NSBitmapImageRep(data: data) else {
                throw TestFailure(description: "status image did not rasterize")
            }
            if let outputPath = ProcessInfo.processInfo.environment["STATUS_IMAGE_OUTPUT"],
               let png = bitmap.representation(using: .png, properties: [:])
            {
                try png.write(to: URL(fileURLWithPath: outputPath), options: .atomic)
            }

            let localTokenStart = Int(Double(bitmap.pixelsWide) * 0.32)
            let localTokenEnd = Int(Double(bitmap.pixelsWide) * 0.66)
            var hasLocalTokenInk = false
            for x in localTokenStart..<localTokenEnd where !hasLocalTokenInk {
                for y in 0..<bitmap.pixelsHigh {
                    if (bitmap.colorAt(x: x, y: y)?.alphaComponent ?? 0) > 0.05 {
                        hasLocalTokenInk = true
                        break
                    }
                }
            }
            guard hasLocalTokenInk else {
                throw TestFailure(description: "local-Token group was not rendered")
            }

            let trailingStart = Int(Double(bitmap.pixelsWide) * 0.72)
            var hasTrailingInk = false
            for x in trailingStart..<bitmap.pixelsWide where !hasTrailingInk {
                for y in 0..<bitmap.pixelsHigh {
                    if (bitmap.colorAt(x: x, y: y)?.alphaComponent ?? 0) > 0.05 {
                        hasTrailingInk = true
                        break
                    }
                }
            }
            guard hasTrailingInk else {
                throw TestFailure(description: "waiting-task group was not rendered")
            }

            print("PASS status image includes quota, local Token, running, waiting, and WeChat connection indicator")
        } catch {
            fputs("FAIL \(error)\n", stderr)
            exit(1)
        }
    }
}
