import Foundation

public struct FeishuTaskCardAuthorization: Decodable {
    public struct Operation: Decodable {
        public let id: String
        public let capabilityId: String
        public let status: String
    }
    public let operation: Operation
    public let challenge: String
    public let status: String

    public static func from(_ error: Error) -> Self? {
        guard case let JSONRPCPipeConnection.Failure.rpc(code, _, data) = error,
              code == -32063, let data,
              let value = try? JSONDecoder().decode(Self.self, from: data),
              value.status == "authorization_required",
              value.operation.capabilityId == "im.sdk.message.send",
              value.operation.status == "awaiting_confirmation",
              !value.operation.id.isEmpty, !value.challenge.isEmpty else { return nil }
        return value
    }
}
