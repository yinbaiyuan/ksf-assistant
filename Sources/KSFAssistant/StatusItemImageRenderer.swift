import AppKit

enum StatusItemImageRenderer {
    static let height: CGFloat = 18
    static let connectionIndicatorDiameter: CGFloat = 5

    static func makeConnectionIndicator() -> NSView {
        let indicator = StatusItemConnectionIndicatorView(frame: NSRect(
            x: 14,
            y: 2,
            width: connectionIndicatorDiameter,
            height: connectionIndicatorDiameter
        ))
        indicator.wantsLayer = true
        indicator.layer?.backgroundColor = NSColor.systemGreen.cgColor
        indicator.layer?.cornerRadius = connectionIndicatorDiameter / 2
        indicator.isHidden = true
        indicator.setAccessibilityElement(false)
        return indicator
    }

    static func render(
        codexIcon: NSImage?,
        quotaText: String,
        localTokenText: String,
        runningText: String,
        waitingText: String,
        connectedText: String = "—"
    ) -> NSImage {
        let valueFont = NSFont.monospacedDigitSystemFont(ofSize: 12, weight: .medium)
        let textAttributes: [NSAttributedString.Key: Any] = [
            .font: valueFont,
            .foregroundColor: NSColor.black,
        ]
        let quotaWidth = textWidth(quotaText, attributes: textAttributes)
        let localTokenWidth = textWidth(localTokenText, attributes: textAttributes)
        let runningWidth = textWidth(runningText, attributes: textAttributes)
        let waitingWidth = textWidth(waitingText, attributes: textAttributes)
        let connectedWidth = textWidth(connectedText, attributes: textAttributes)

        let iconWidth: CGFloat = 16
        let runningSymbolWidth: CGFloat = 10
        let waitingSymbolWidth: CGFloat = 14
        let iconTextGap: CGFloat = 4
        let groupGap: CGFloat = 5
        let symbolTextGap: CGFloat = 2
        let width = ceil(
            iconWidth + iconTextGap + quotaWidth
                + groupGap + localTokenWidth
                + groupGap + runningSymbolWidth + symbolTextGap + runningWidth
                + groupGap + waitingSymbolWidth + symbolTextGap + waitingWidth
                + groupGap + 12 + symbolTextGap + connectedWidth
        )

        let image = NSImage(size: NSSize(width: width, height: height))
        image.lockFocus()
        NSGraphicsContext.current?.imageInterpolation = .high

        var x: CGFloat = 0
        let primaryIcon = codexIcon
            ?? NSImage(systemSymbolName: "terminal.fill", accessibilityDescription: nil)
        draw(primaryIcon, in: NSRect(x: x, y: 1, width: iconWidth, height: iconWidth))
        x += iconWidth + iconTextGap

        drawText(quotaText, x: x, width: quotaWidth, attributes: textAttributes)
        x += quotaWidth + groupGap

        drawText(localTokenText, x: x, width: localTokenWidth, attributes: textAttributes)
        x += localTokenWidth + groupGap

        let runningSymbolConfiguration = NSImage.SymbolConfiguration(pointSize: 9, weight: .semibold)
        let runningSymbol = NSImage(
            systemSymbolName: "play.fill",
            accessibilityDescription: nil
        )?.withSymbolConfiguration(runningSymbolConfiguration)
        draw(runningSymbol, in: NSRect(x: x, y: 4, width: runningSymbolWidth, height: runningSymbolWidth))
        x += runningSymbolWidth + symbolTextGap
        drawText(runningText, x: x, width: runningWidth, attributes: textAttributes)
        x += runningWidth + groupGap

        let waitingSymbolConfiguration = NSImage.SymbolConfiguration(pointSize: 13, weight: .semibold)
        let waitingSymbol = NSImage(
            systemSymbolName: "person.fill.questionmark",
            accessibilityDescription: nil
        )?.withSymbolConfiguration(waitingSymbolConfiguration)
        draw(waitingSymbol, in: NSRect(x: x, y: 2, width: waitingSymbolWidth, height: waitingSymbolWidth))
        x += waitingSymbolWidth + symbolTextGap
        drawText(waitingText, x: x, width: waitingWidth, attributes: textAttributes)
        x += waitingWidth + groupGap
        let connectedSymbol = NSImage(systemSymbolName: "paperplane.fill", accessibilityDescription: nil)
        draw(connectedSymbol, in: NSRect(x: x, y: 3, width: 12, height: 12))
        x += 12 + symbolTextGap
        drawText(connectedText, x: x, width: connectedWidth, attributes: textAttributes)

        image.unlockFocus()

        guard
            let tiff = image.tiffRepresentation,
            let bitmap = NSBitmapImageRep(data: tiff),
            let png = bitmap.representation(using: .png, properties: [:]),
            let canonicalImage = NSImage(data: png)
        else {
            image.isTemplate = false
            return image
        }
        canonicalImage.size = NSSize(width: width, height: height)
        canonicalImage.isTemplate = false
        return canonicalImage
    }

    static func codexIcon(in bundle: Bundle = .main) -> NSImage? {
        guard
            let url = bundle.url(forResource: "CodexStatusIcon", withExtension: "svg"),
            let image = NSImage(contentsOf: url)
        else {
            return nil
        }
        image.size = NSSize(width: 16, height: 16)
        return image
    }

    private static func textWidth(
        _ value: String,
        attributes: [NSAttributedString.Key: Any]
    ) -> CGFloat {
        ceil((value as NSString).size(withAttributes: attributes).width)
    }

    private static func drawText(
        _ value: String,
        x: CGFloat,
        width: CGFloat,
        attributes: [NSAttributedString.Key: Any]
    ) {
        (value as NSString).draw(
            in: NSRect(x: x, y: 2, width: width, height: 14),
            withAttributes: attributes
        )
    }

    private static func draw(_ source: NSImage?, in bounds: NSRect) {
        guard let source, source.size.width > 0, source.size.height > 0 else { return }
        let scale = min(bounds.width / source.size.width, bounds.height / source.size.height)
        let size = NSSize(width: source.size.width * scale, height: source.size.height * scale)
        let rect = NSRect(
            x: bounds.midX - size.width / 2,
            y: bounds.midY - size.height / 2,
            width: size.width,
            height: size.height
        )
        let drawable = (source.copy() as? NSImage) ?? source
        drawable.isTemplate = false
        drawable.draw(in: rect, from: .zero, operation: .sourceOver, fraction: 1)
    }
}

private final class StatusItemConnectionIndicatorView: NSView {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}
