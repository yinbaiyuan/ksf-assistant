import CodexUsageCore
import Foundation

private actor MockTransport: WeChatILinkTransport {
    private var responses: [Data]
    private(set) var requests: [URLRequest] = []

    init(_ responses: [String]) { self.responses = responses.map { Data($0.utf8) } }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        requests.append(request)
        guard !responses.isEmpty else { throw URLError(.badServerResponse) }
        return (
            responses.removeFirst(),
            HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!
        )
    }
}

@main
struct WeChatStandalone {
    static func main() async throws {
        let transport = MockTransport([
            #"{"qrcode":"code","qrcode_img_content":"https://example.test/qr"}"#,
            #"{"status":"confirmed","bot_token":"token","ilink_bot_id":"bot","ilink_user_id":"owner","baseurl":"https://service.test"}"#,
            #"{"ret":0,"msgs":[{"message_id":1,"from_user_id":"owner","context_token":"ctx","item_list":[{"type":1,"text_item":{"text":"ping"}}]}],"get_updates_buf":"next"}"#,
            #"{"ret":0}"#,
        ])
        let client = WeChatILinkClient(transport: transport)
        let login = try await client.beginLogin()
        precondition(login.code == "code")
        let loginResult = try await client.pollLogin(code: login.code)
        guard case let .confirmed(token, credentials) = loginResult.status else {
            preconditionFailure("login was not confirmed")
        }
        let batch = try await client.getUpdates(
            baseURL: credentials.baseURL,
            token: token,
            cursor: "",
            timeoutMilliseconds: 1_000
        )
        precondition(batch.cursor == "next")
        precondition(batch.messages.first?.body == "ping")
        try await client.sendMessage(
            baseURL: credentials.baseURL,
            token: token,
            to: credentials.ownerUserID,
            text: "pong",
            contextToken: "ctx"
        )
        let paths = await transport.requests.compactMap(\.url?.path)
        precondition(paths == [
            "/ilink/bot/get_bot_qrcode",
            "/ilink/bot/get_qrcode_status",
            "/ilink/bot/getupdates",
            "/ilink/bot/sendmessage",
        ])
        let sendRequest = await transport.requests.last
        let sendBody = try JSONSerialization.jsonObject(with: sendRequest!.httpBody!) as! [String: Any]
        let message = sendBody["msg"] as! [String: Any]
        precondition(message["from_user_id"] as? String == "")
        precondition((message["client_id"] as? String)?.hasPrefix("codex-usage-bar-") == true)
        precondition(message["message_type"] as? Int == 2)
        precondition(message["message_state"] as? Int == 2)
        let reminderNow = Date(timeIntervalSince1970: 100_000)
        let lastOwnerMessage = reminderNow.addingTimeInterval(-WeChatSessionReminderPolicy.reminderInterval)
        precondition(WeChatSessionReminderPolicy.shouldSend(
            lastOwnerMessageAt: lastOwnerMessage,
            reminderSentForMessageAt: nil,
            now: reminderNow
        ))
        precondition(!WeChatSessionReminderPolicy.shouldSend(
            lastOwnerMessageAt: lastOwnerMessage,
            reminderSentForMessageAt: lastOwnerMessage,
            now: reminderNow
        ))
        let now = Date()
        let fullQueue = (0 ..< 101).map { index in
            InboundCommand(
                accountID: "account",
                messageID: "\(index)",
                senderID: "owner",
                body: "command",
                receivedAt: now.addingTimeInterval(TimeInterval(index))
            )
        }
        let pruned = WeChatCommandQueuePolicy.pruned(fullQueue, now: now)
        precondition(pruned.count == 100)
        precondition(pruned.first?.messageID == "1")
        precondition(!WeChatCommandQueuePolicy.canAccept(pruned, now: now))
        print("WeChat protocol tests passed")
    }
}
