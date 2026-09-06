import Foundation

public struct UserApprovalAttachment: Codable, Equatable {
    public let name: String
    public let size: Int64
    public let sha256: String
}

public struct UserApprovalRequest: Codable, Equatable {
    public let id: String
    public let title: String
    public let user: String
    public let application: String
    public let action: String
    public let target: String
    public let content: String
    public let attachments: [UserApprovalAttachment]
    public let source: String
    public let expiresAt: String

    public var expiration: Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: expiresAt) ?? ISO8601DateFormatter().date(from: expiresAt)
    }

    public var details: String {
        let files = attachments.isEmpty ? "无" : attachments.map { "\($0.name) · \($0.size) 字节\nSHA-256：\($0.sha256)" }.joined(separator: "\n\n")
        return "请求\n\(title)\n\n用户身份\n\(user)\n\n应用\n\(application)\n\n操作\n\(action)\n\n目标\n\(target)\n\n内容 / 修改摘要\n\(content)\n\n附件\n\(files)\n\n来源（由 Core 提供）\n\(source)\n\n失效时间\n\(expiresAt)"
    }
}

public struct UserApprovalPoll: Decodable {
    public let schemaVersion: Int
    public let request: UserApprovalRequest?

    public static func decode(_ data: Data, now: Date = Date()) throws -> Self {
        guard data.count <= 2 * 1_024 * 1_024,
              let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              Set(object.keys) == ["schemaVersion", "request"] else { throw JSONRPCPipeConnection.Failure.invalidResponse }
        let result = try JSONDecoder().decode(Self.self, from: data)
        guard result.schemaVersion == 1 else { throw JSONRPCPipeConnection.Failure.invalidResponse }
        if let request = result.request {
            guard let raw = object["request"] as? [String: Any],
                  Set(raw.keys) == ["id", "title", "user", "application", "action", "target", "content", "attachments", "source", "expiresAt"],
                  let attachments = raw["attachments"] as? [[String: Any]],
                  attachments.allSatisfy({ Set($0.keys) == ["name", "size", "sha256"] }),
                  !request.id.isEmpty, request.id.utf8.count <= 256,
                  [request.title, request.user, request.application, request.action, request.target, request.source].allSatisfy({ !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && $0.utf8.count <= 65_536 }),
                  !request.details.contains("\0"),
                  request.content.utf8.count <= 1_024 * 1_024,
                  request.attachments.count <= 1_000,
                  request.attachments.allSatisfy({ !$0.name.isEmpty && $0.name.utf8.count <= 4_096 && $0.size >= 0 && $0.size <= 9_007_199_254_740_991 && $0.sha256.range(of: "^[a-fA-F0-9]{64}$", options: .regularExpression) != nil }),
                  let expiration = request.expiration, expiration > now, expiration.timeIntervalSince(now) <= 301 else {
                throw JSONRPCPipeConnection.Failure.invalidResponse
            }
        }
        return result
    }
}

public struct UserApprovalDecision: Decodable {
    public let schemaVersion: Int
    public let accepted: Bool

    public static func decode(_ data: Data) throws -> Self {
        guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any], Set(object.keys) == ["schemaVersion", "accepted"] else { throw JSONRPCPipeConnection.Failure.invalidResponse }
        let result = try JSONDecoder().decode(Self.self, from: data)
        guard result.schemaVersion == 1 else { throw JSONRPCPipeConnection.Failure.invalidResponse }
        return result
    }
}
