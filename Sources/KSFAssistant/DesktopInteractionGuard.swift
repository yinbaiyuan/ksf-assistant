import AppKit
import CoreGraphics

// Local card/configuration confirmation requires an unlocked interactive desktop.
// This guard neither polls Core nor approves Agent business operations.
@MainActor
enum DesktopInteractionGuard {
    static var allowsConfigurationSubmission: Bool {
        guard NSApp.isRunning, !NSApp.isHidden, NSApp.modalWindow == nil,
              NSScreen.screens.contains(where: { screen in
                  guard let id = screen.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber else { return false }
                  return CGDisplayIsAsleep(id.uint32Value) == 0
              }),
              let session = CGSessionCopyCurrentDictionary() as? [String: Any],
              session[kCGSessionOnConsoleKey as String] as? Bool == true,
              session[kCGSessionLoginDoneKey as String] as? Bool == true else { return false }
        return session["CGSSessionScreenIsLocked"] as? Bool != true
    }
}
