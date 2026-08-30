import CodexUsageCore
import Foundation

@main
private enum LiveProbe {
    static func main() async {
        guard let executable = CodexLocator.locate() else {
            fputs("FAIL Codex executable not found\n", stderr)
            exit(1)
        }

        let client = CodexAppServerClient {
            ProcessAppServerTransport(executableURL: executable)
        }

        do {
            let limits = try await client.fetchRateLimits()
            guard limits.generalBucket?.headlineRemainingPercent != nil else {
                throw CodexUsageError.unsupportedProtocol("The general codex bucket has no window.")
            }
            print("PASS live rate-limit read and normalized general bucket")

            let usage = try await client.fetchTokenUsage()
            let hasSummary = usage.summary.lifetimeTokens != nil
                || usage.summary.peakDailyTokens != nil
                || usage.summary.currentStreakDays != nil
            guard hasSummary else {
                throw CodexUsageError.protocolFailure("The token summary was empty.")
            }
            print("PASS live token-usage read and normalized summary")

            let threads = try await client.fetchThreads()
            guard !threads.isEmpty else {
                throw CodexUsageError.protocolFailure("The thread catalog was empty.")
            }
            print("PASS live read-only thread catalog (\(threads.count) tasks)")
            await client.stop()
        } catch {
            fputs("FAIL live App Server probe: \(error.localizedDescription)\n", stderr)
            await client.stop()
            exit(1)
        }
    }
}
