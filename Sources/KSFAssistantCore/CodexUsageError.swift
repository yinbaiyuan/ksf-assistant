import Foundation

public enum CodexUsageError: Error, Equatable, LocalizedError {
    case codexMissing
    case authenticationRequired
    case unsupportedProtocol(String)
    case offline(String)
    case protocolFailure(String)

    public var errorDescription: String? {
        switch self {
        case .codexMissing: return "Codex executable was not found."
        case .authenticationRequired: return "Sign in to Codex with ChatGPT to read account usage."
        case let .unsupportedProtocol(message): return "This Codex build does not support the required account methods. \(message)"
        case let .offline(message): return "Codex usage is temporarily unavailable. \(message)"
        case let .protocolFailure(message): return "Codex App Server returned an invalid response. \(message)"
        }
    }
}
