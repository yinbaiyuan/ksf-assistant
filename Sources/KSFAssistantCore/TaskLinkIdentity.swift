import Foundation
import CryptoKit

public enum TaskLinkIdentity {
    public static func taskKey(for threadID: String) -> String {
        SHA256.hash(data: Data(threadID.utf8)).prefix(10).map { String(format: "%02x", $0) }.joined()
    }
}
