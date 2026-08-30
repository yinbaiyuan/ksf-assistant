import Foundation

public enum WeChatConnectionState: Equatable, Sendable {
    case disconnected
    case awaitingScan(qrContent: String)
    case connecting
    case connected
    case reconnecting
    case credentialsExpired
    case failed(message: String)
}

public enum InboundCommandStatus: String, Codable, Sendable {
    case pending
    case completed
}

public struct InboundCommand: Codable, Equatable, Identifiable, Sendable {
    public let id: UUID
    public let accountID: String
    public let messageID: String
    public let senderID: String
    public let body: String
    public let receivedAt: Date
    public var status: InboundCommandStatus

    public init(
        id: UUID = UUID(),
        accountID: String,
        messageID: String,
        senderID: String,
        body: String,
        receivedAt: Date = Date(),
        status: InboundCommandStatus = .pending
    ) {
        self.id = id
        self.accountID = accountID
        self.messageID = messageID
        self.senderID = senderID
        self.body = body
        self.receivedAt = receivedAt
        self.status = status
    }
}

public enum WeChatCommandQueuePolicy {
    public static let maximumPendingCount = 100
    public static let retentionInterval: TimeInterval = 30 * 24 * 60 * 60

    public static func pruned(_ commands: [InboundCommand], now: Date = Date()) -> [InboundCommand] {
        let cutoff = now.addingTimeInterval(-retentionInterval)
        let active = commands.filter { $0.receivedAt >= cutoff && $0.status == .pending }
        return Array(active.sorted { $0.receivedAt < $1.receivedAt }.suffix(maximumPendingCount))
    }

    public static func canAccept(_ commands: [InboundCommand], now: Date = Date()) -> Bool {
        pruned(commands, now: now).count < maximumPendingCount
    }
}

public enum WeChatSessionReminderPolicy {
    public static let reminderInterval: TimeInterval = 23 * 60 * 60

    public static func shouldSend(
        lastOwnerMessageAt: Date?,
        reminderSentForMessageAt: Date?,
        now: Date = Date()
    ) -> Bool {
        guard let lastOwnerMessageAt,
              now.timeIntervalSince(lastOwnerMessageAt) >= reminderInterval else {
            return false
        }
        guard let reminderSentForMessageAt else { return true }
        return reminderSentForMessageAt < lastOwnerMessageAt
    }
}

public struct WeChatLoginChallenge: Equatable, Sendable {
    public let sessionID: UUID
    public let qrContent: String

    public init(sessionID: UUID, qrContent: String) {
        self.sessionID = sessionID
        self.qrContent = qrContent
    }
}

public protocol WeChatMessaging: AnyObject, Sendable {
    var stateUpdates: AsyncStream<WeChatConnectionState> { get }
    var commandUpdates: AsyncStream<InboundCommand> { get }

    func start() async
    func stop() async
    func beginLogin() async throws -> WeChatLoginChallenge
    func send(text: String) async throws
    func pendingCommands() async throws -> [InboundCommand]
    func markCompleted(id: UUID) async throws
    func disconnect() async
}

public enum WeChatMessagingError: LocalizedError, Equatable {
    case invalidResponse
    case loginExpired
    case loginRejected(String)
    case notConnected
    case credentialsExpired
    case queueFull
    case unsupportedMessage
    case server(code: Int, message: String?)

    public var errorDescription: String? {
        switch self {
        case .invalidResponse: return "微信服务返回了无法识别的数据。"
        case .loginExpired: return "微信二维码已过期，请重新扫码。"
        case let .loginRejected(message): return message
        case .notConnected: return "微信尚未连接。"
        case .credentialsExpired: return "微信连接已失效，请重新扫码。"
        case .queueFull: return "待处理命令已达到上限。"
        case .unsupportedMessage: return "当前只支持文字消息。"
        case let .server(code, message):
            return message.map { "微信服务错误 \(code)：\($0)" } ?? "微信服务错误 \(code)。"
        }
    }
}

public struct WeChatCredentials: Codable, Equatable, Sendable {
    public let accountID: String
    public let ownerUserID: String
    public let baseURL: URL

    public init(accountID: String, ownerUserID: String, baseURL: URL) {
        self.accountID = accountID
        self.ownerUserID = ownerUserID
        self.baseURL = baseURL
    }
}

public struct WeChatLoginResult: Equatable, Sendable {
    public enum Status: Equatable, Sendable {
        case waiting
        case scanned
        case confirmed(token: String, credentials: WeChatCredentials)
        case expired
    }

    public let status: Status
    public init(status: Status) { self.status = status }
}

public struct WeChatInboundMessage: Equatable, Sendable {
    public let messageID: String
    public let senderID: String
    public let body: String?
    public let contextToken: String?

    public init(messageID: String, senderID: String, body: String?, contextToken: String?) {
        self.messageID = messageID
        self.senderID = senderID
        self.body = body
        self.contextToken = contextToken
    }
}

public struct WeChatUpdateBatch: Equatable, Sendable {
    public let messages: [WeChatInboundMessage]
    public let cursor: String
    public let suggestedTimeoutMilliseconds: Int?

    public init(messages: [WeChatInboundMessage], cursor: String, suggestedTimeoutMilliseconds: Int?) {
        self.messages = messages
        self.cursor = cursor
        self.suggestedTimeoutMilliseconds = suggestedTimeoutMilliseconds
    }
}

public protocol WeChatILinkTransport: Sendable {
    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse)
}

public struct URLSessionWeChatTransport: WeChatILinkTransport {
    private let session: URLSession

    public init(session: URLSession = .shared) {
        self.session = session
    }

    public func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw WeChatMessagingError.invalidResponse }
        return (data, http)
    }
}

public actor WeChatILinkClient {
    public static let defaultBaseURL = URL(string: "https://ilinkai.weixin.qq.com")!

    private let transport: WeChatILinkTransport
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()
    private let clientUIN: String

    public init(transport: WeChatILinkTransport = URLSessionWeChatTransport()) {
        self.transport = transport
        clientUIN = Data(String(UInt32.random(in: UInt32.min ... UInt32.max)).utf8).base64EncodedString()
    }

    public func beginLogin() async throws -> (code: String, qrContent: String) {
        struct Body: Encodable { let local_token_list: [String] = [] }
        struct Response: Decodable { let qrcode: String; let qrcode_img_content: String }
        let response: Response = try await request(
            baseURL: Self.defaultBaseURL,
            path: "ilink/bot/get_bot_qrcode?bot_type=3",
            method: "POST",
            body: Body(),
            token: nil,
            timeout: 30
        )
        return (response.qrcode, response.qrcode_img_content)
    }

    public func pollLogin(code: String, baseURL: URL = WeChatILinkClient.defaultBaseURL) async throws -> WeChatLoginResult {
        struct Response: Decodable {
            let status: String
            let bot_token: String?
            let ilink_bot_id: String?
            let ilink_user_id: String?
            let baseurl: String?
            let redirect_host: String?
        }
        let response: Response = try await request(
            baseURL: baseURL,
            path: "ilink/bot/get_qrcode_status?qrcode=\(percentEncoded(code))",
            method: "GET",
            bodyData: nil,
            token: nil,
            timeout: 40
        )
        switch response.status {
        case "wait": return WeChatLoginResult(status: .waiting)
        case "scaned", "need_verifycode": return WeChatLoginResult(status: .scanned)
        case "expired": return WeChatLoginResult(status: .expired)
        case "scaned_but_redirect":
            guard let host = response.redirect_host, let redirected = normalizedURL(host) else {
                throw WeChatMessagingError.invalidResponse
            }
            return try await pollLogin(code: code, baseURL: redirected)
        case "confirmed":
            guard let token = response.bot_token,
                  let accountID = response.ilink_bot_id,
                  let ownerID = response.ilink_user_id else {
                throw WeChatMessagingError.invalidResponse
            }
            let serviceURL = response.baseurl.flatMap(normalizedURL) ?? baseURL
            return WeChatLoginResult(status: .confirmed(
                token: token,
                credentials: WeChatCredentials(accountID: accountID, ownerUserID: ownerID, baseURL: serviceURL)
            ))
        default:
            throw WeChatMessagingError.loginRejected("微信未完成连接，请重新扫码。")
        }
    }

    public func getUpdates(
        baseURL: URL,
        token: String,
        cursor: String,
        timeoutMilliseconds: Int
    ) async throws -> WeChatUpdateBatch {
        struct BaseInfo: Encodable { let channel_version = "0.3.0"; let bot_agent = "CodexUsageBar/0.3.0" }
        struct Body: Encodable { let get_updates_buf: String; let base_info = BaseInfo() }
        struct TextItem: Decodable { let text: String? }
        struct Item: Decodable { let type: Int; let text_item: TextItem? }
        struct Message: Decodable {
            let message_id: Int64?
            let from_user_id: String?
            let context_token: String?
            let item_list: [Item]?
        }
        struct Response: Decodable {
            let ret: Int?
            let errcode: Int?
            let errmsg: String?
            let msgs: [Message]?
            let get_updates_buf: String?
            let longpolling_timeout_ms: Int?
        }
        let response: Response = try await request(
            baseURL: baseURL,
            path: "ilink/bot/getupdates",
            method: "POST",
            body: Body(get_updates_buf: cursor),
            token: token,
            timeout: TimeInterval(max(timeoutMilliseconds, 1_000)) / 1_000 + 5
        )
        let code = response.errcode ?? response.ret ?? 0
        guard code == 0 else {
            if code == -14 { throw WeChatMessagingError.credentialsExpired }
            throw WeChatMessagingError.server(code: code, message: response.errmsg)
        }
        let messages = (response.msgs ?? []).compactMap { message -> WeChatInboundMessage? in
            guard let sender = message.from_user_id else { return nil }
            let text = message.item_list?.first(where: { $0.type == 1 })?.text_item?.text
            return WeChatInboundMessage(
                messageID: message.message_id.map(String.init) ?? UUID().uuidString,
                senderID: sender,
                body: text,
                contextToken: message.context_token
            )
        }
        return WeChatUpdateBatch(
            messages: messages,
            cursor: response.get_updates_buf ?? cursor,
            suggestedTimeoutMilliseconds: response.longpolling_timeout_ms
        )
    }

    public func sendMessage(
        baseURL: URL,
        token: String,
        to ownerUserID: String,
        text: String,
        contextToken: String?
    ) async throws {
        struct BaseInfo: Encodable { let channel_version = "0.3.0"; let bot_agent = "CodexUsageBar/0.3.0" }
        struct TextItem: Encodable { let text: String }
        struct Item: Encodable { let type = 1; let text_item: TextItem }
        struct Message: Encodable {
            let from_user_id: String
            let to_user_id: String
            let client_id: String
            let message_type: Int
            let message_state: Int
            let context_token: String?
            let item_list: [Item]
        }
        struct Body: Encodable { let msg: Message; let base_info = BaseInfo() }
        struct Response: Decodable { let ret: Int?; let errcode: Int?; let errmsg: String? }
        let response: Response = try await request(
            baseURL: baseURL,
            path: "ilink/bot/sendmessage",
            method: "POST",
            body: Body(msg: Message(
                from_user_id: "",
                to_user_id: ownerUserID,
                client_id: "codex-usage-bar-\(UUID().uuidString.lowercased())",
                message_type: 2,
                message_state: 2,
                context_token: contextToken,
                item_list: [Item(text_item: TextItem(text: text))]
            )),
            token: token,
            timeout: 30
        )
        let code = response.errcode ?? response.ret ?? 0
        guard code == 0 else {
            if code == -14 { throw WeChatMessagingError.credentialsExpired }
            throw WeChatMessagingError.server(code: code, message: response.errmsg)
        }
    }

    private func request<Response: Decodable, Body: Encodable>(
        baseURL: URL,
        path: String,
        method: String,
        body: Body,
        token: String?,
        timeout: TimeInterval
    ) async throws -> Response {
        try await request(
            baseURL: baseURL,
            path: path,
            method: method,
            bodyData: try encoder.encode(body),
            token: token,
            timeout: timeout
        )
    }

    private func request<Response: Decodable>(
        baseURL: URL,
        path: String,
        method: String,
        bodyData: Data?,
        token: String?,
        timeout: TimeInterval
    ) async throws -> Response {
        let baseString = baseURL.absoluteString.hasSuffix("/")
            ? baseURL.absoluteString
            : baseURL.absoluteString + "/"
        guard let normalizedBase = URL(string: baseString),
              let url = URL(string: path, relativeTo: normalizedBase)?.absoluteURL else {
            throw WeChatMessagingError.invalidResponse
        }
        var request = URLRequest(url: url, timeoutInterval: timeout)
        request.httpMethod = method
        request.httpBody = bodyData
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("bot", forHTTPHeaderField: "iLink-App-Id")
        request.setValue("768", forHTTPHeaderField: "iLink-App-ClientVersion")
        request.setValue("ilink_bot_token", forHTTPHeaderField: "AuthorizationType")
        request.setValue(clientUIN, forHTTPHeaderField: "X-WECHAT-UIN")
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let (data, response) = try await transport.data(for: request)
        guard (200 ..< 300).contains(response.statusCode) else {
            throw WeChatMessagingError.server(code: response.statusCode, message: nil)
        }
        do { return try decoder.decode(Response.self, from: data) }
        catch { throw WeChatMessagingError.invalidResponse }
    }

    private func percentEncoded(_ value: String) -> String {
        let unreserved = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-._~"))
        return value.addingPercentEncoding(withAllowedCharacters: unreserved) ?? value
    }

    private func normalizedURL(_ value: String) -> URL? {
        if value.hasPrefix("http://") || value.hasPrefix("https://") { return URL(string: value) }
        return URL(string: "https://\(value)")
    }
}
