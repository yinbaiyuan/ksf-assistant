import Foundation

public enum TokenCountFormatter {
    public static func millions(_ tokens: Int64) -> String {
        let nonnegativeTokens = max(0, tokens)
        return String(
            format: "%.1fM",
            locale: Locale(identifier: "en_US_POSIX"),
            Double(nonnegativeTokens) / 1_000_000
        )
    }
}
