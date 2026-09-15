import Foundation
import IOKit.pwr_mgt

// A host-owned assertion, not a persistent change to system power settings.
// Call from the application main thread. Display sleep and locking stay enabled.
final class SleepInhibitor {
    private var assertion: UInt32?
    private let acquire: () throws -> UInt32
    private let release: (UInt32) throws -> Void

    init(acquire: @escaping () throws -> UInt32 = SleepInhibitor.acquireAssertion,
         release: @escaping (UInt32) throws -> Void = SleepInhibitor.releaseAssertion) {
        self.acquire = acquire
        self.release = release
    }

    func setEnabled(_ enabled: Bool) throws {
        if enabled {
            if assertion == nil { assertion = try acquire() }
        } else if let id = assertion {
            try release(id)
            assertion = nil
        }
    }

    deinit { if let id = assertion { try? release(id) } }

    private static func acquireAssertion() throws -> UInt32 {
        var id: IOPMAssertionID = 0
        let result = IOPMAssertionCreateWithName(
            kIOPMAssertionTypePreventUserIdleSystemSleep as CFString,
            IOPMAssertionLevel(kIOPMAssertionLevelOn),
            "KSFAssistant remote task access" as CFString, &id
        )
        guard result == kIOReturnSuccess else { throw powerError(result) }
        return id
    }

    private static func releaseAssertion(_ id: UInt32) throws {
        let result = IOPMAssertionRelease(id)
        guard result == kIOReturnSuccess else { throw powerError(result) }
    }

    private static func powerError(_ code: IOReturn) -> NSError {
        NSError(domain: "KSFAssistant.Power", code: Int(code),
                userInfo: [NSLocalizedDescriptionKey: "无法更改防睡眠状态，请重试（\(code)）。"])
    }
}
