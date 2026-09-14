import AppKit

// Compile with Sources/KSFAssistant/StatusItemImageRenderer.swift. Uses the
// production renderer without launching Core, Feishu, or the installed app.
@main struct StatusItemPreview {
    static func main() throws {
        _ = NSApplication.shared
        let counts = ["0", "7", "42", "120", "—"]
        let canvas = NSImage(size: NSSize(width: 480, height: 190))
        canvas.lockFocus()
        NSColor.white.setFill()
        NSRect(x: 0, y: 0, width: 480, height: 190).fill()
        var widths: [CGFloat] = []
        for (index, count) in counts.enumerated() {
            let image = StatusItemImageRenderer.render(codexIcon: nil, quotaText: "72%", localTokenText: "19.2M", runningText: "2", waitingText: "1", connectedText: count)
            widths.append(image.size.width)
            image.draw(at: NSPoint(x: 20, y: 155 - index * 32), from: .zero, operation: .sourceOver, fraction: 1)
        }
        canvas.unlockFocus()
        precondition(widths[2] > widths[1] && widths[3] > widths[2], "Count was clipped to a fixed width")
        let bitmap = NSBitmapImageRep(data: canvas.tiffRepresentation!)!
        try bitmap.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
        print("PASS production renderer: zero, single/two/three-digit and unknown counts; widths \(widths)")
    }
}
