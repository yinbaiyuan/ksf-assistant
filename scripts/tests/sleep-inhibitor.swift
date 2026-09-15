import Foundation

@main
struct SleepInhibitorTests {
    static func main() throws {
        var acquired = 0
        var released: [UInt32] = []
        enum Failure: Error { case denied }
        var fail = false
        let inhibitor = SleepInhibitor(acquire: {
            if fail { throw Failure.denied }
            acquired += 1
            return 42
        }, release: { released.append($0) })
        try inhibitor.setEnabled(false)
        try inhibitor.setEnabled(true)
        try inhibitor.setEnabled(true)
        precondition(acquired == 1)
        try inhibitor.setEnabled(false)
        try inhibitor.setEnabled(false)
        precondition(released == [42])
        fail = true
        do { try inhibitor.setEnabled(true); fatalError("expected rejection") }
        catch Failure.denied {}
        fail = false
        try inhibitor.setEnabled(true)
        precondition(acquired == 2)
        try inhibitor.setEnabled(false)
        if CommandLine.arguments.contains("--live") {
            let live = SleepInhibitor()
            try live.setEnabled(true)
            try live.setEnabled(true)
            try live.setEnabled(false)
        }
        print("PASS sleep inhibitor acquisition, idempotence, release and retry")
    }
}
