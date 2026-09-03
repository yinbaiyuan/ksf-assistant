import Foundation

public enum TokenCostFormatter {
    public static func usd(microUSD: Int64) -> String {
        let value = max(0, microUSD)
        if value == 0 {
            return "$0.00"
        }
        let digits = value < 10_000 ? 6 : value < 1_000_000 ? 4 : 2
        let amount = NSDecimalNumber(mantissa: UInt64(value), exponent: -6, isNegative: false)
        let formatter = NumberFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.numberStyle = .decimal
        formatter.usesGroupingSeparator = true
        formatter.minimumFractionDigits = digits
        formatter.maximumFractionDigits = digits
        formatter.roundingMode = .halfUp
        return "$\(formatter.string(from: amount) ?? "0.00")"
    }
}
