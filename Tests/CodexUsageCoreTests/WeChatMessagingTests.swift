import Foundation
import XCTest
@testable import CodexUsageCore

final class WeChatMessagingTests: XCTestCase {
    func testSessionReminderBecomesDueOncePerOwnerMessage() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        let lastMessage = now.addingTimeInterval(-WeChatSessionReminderPolicy.reminderInterval)

        XCTAssertTrue(WeChatSessionReminderPolicy.shouldSend(
            lastOwnerMessageAt: lastMessage,
            reminderSentForMessageAt: nil,
            now: now
        ))
        XCTAssertFalse(WeChatSessionReminderPolicy.shouldSend(
            lastOwnerMessageAt: lastMessage,
            reminderSentForMessageAt: lastMessage,
            now: now
        ))

        let newerMessage = now.addingTimeInterval(1)
        XCTAssertTrue(WeChatSessionReminderPolicy.shouldSend(
            lastOwnerMessageAt: newerMessage,
            reminderSentForMessageAt: lastMessage,
            now: newerMessage.addingTimeInterval(WeChatSessionReminderPolicy.reminderInterval)
        ))
    }

    func testSessionReminderIsNotDueBeforeTwentyThreeHoursOrWithoutMessage() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        XCTAssertFalse(WeChatSessionReminderPolicy.shouldSend(
            lastOwnerMessageAt: nil,
            reminderSentForMessageAt: nil,
            now: now
        ))
        XCTAssertFalse(WeChatSessionReminderPolicy.shouldSend(
            lastOwnerMessageAt: now.addingTimeInterval(-WeChatSessionReminderPolicy.reminderInterval + 1),
            reminderSentForMessageAt: nil,
            now: now
        ))
    }

    func testLoginUpdateAndSendProtocol() async throws {
        let transport = WeChatMockTransport(responses: [
            #"{"qrcode":"code-1","qrcode_img_content":"https://example.test/qr"}"#,
            #"{"status":"confirmed","bot_token":"secret","ilink_bot_id":"bot@im.bot","ilink_user_id":"owner@im.wechat","baseurl":"https://service.test"}"#,
            #"{"ret":0,"msgs":[{"message_id":42,"from_user_id":"owner@im.wechat","context_token":"ctx","item_list":[{"type":1,"text_item":{"text":"status"}}]}],"get_updates_buf":"next","longpolling_timeout_ms":35000}"#,
            #"{"ret":0}"#,
        ])
        let client = WeChatILinkClient(transport: transport)

        let login = try await client.beginLogin()
        XCTAssertEqual(login.code, "code-1")
        let result = try await client.pollLogin(code: login.code)
        guard case let .confirmed(token, credentials) = result.status else {
            return XCTFail("Expected confirmed login")
        }
        XCTAssertEqual(token, "secret")
        XCTAssertEqual(credentials.ownerUserID, "owner@im.wechat")

        let batch = try await client.getUpdates(
            baseURL: credentials.baseURL,
            token: token,
            cursor: "previous",
            timeoutMilliseconds: 35_000
        )
        XCTAssertEqual(batch.cursor, "next")
        XCTAssertEqual(batch.messages.first?.body, "status")
        XCTAssertEqual(batch.messages.first?.contextToken, "ctx")

        try await client.sendMessage(
            baseURL: credentials.baseURL,
            token: token,
            to: credentials.ownerUserID,
            text: "reply",
            contextToken: "ctx"
        )

        let requests = await transport.requests
        XCTAssertEqual(requests.map(\.url?.path), [
            "/ilink/bot/get_bot_qrcode",
            "/ilink/bot/get_qrcode_status",
            "/ilink/bot/getupdates",
            "/ilink/bot/sendmessage",
        ])
        let sendBody = try XCTUnwrap(requests.last?.httpBody)
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: sendBody) as? [String: Any])
        let message = try XCTUnwrap(json["msg"] as? [String: Any])
        XCTAssertEqual(message["from_user_id"] as? String, "")
        XCTAssertEqual(message["to_user_id"] as? String, "owner@im.wechat")
        XCTAssertTrue((message["client_id"] as? String)?.hasPrefix("codex-usage-bar-") == true)
        XCTAssertEqual(message["message_type"] as? Int, 2)
        XCTAssertEqual(message["message_state"] as? Int, 2)
        XCTAssertEqual(message["context_token"] as? String, "ctx")
        XCTAssertNotNil(json["base_info"])
        XCTAssertEqual(requests.last?.value(forHTTPHeaderField: "Authorization"), "Bearer secret")
    }

    func testStaleTokenMapsToCredentialsExpired() async throws {
        let transport = WeChatMockTransport(responses: [#"{"ret":-14,"errcode":-14}"#])
        let client = WeChatILinkClient(transport: transport)
        do {
            _ = try await client.getUpdates(
                baseURL: URL(string: "https://service.test")!,
                token: "expired",
                cursor: "",
                timeoutMilliseconds: 1_000
            )
            XCTFail("Expected credentialsExpired")
        } catch let error as WeChatMessagingError {
            XCTAssertEqual(error, .credentialsExpired)
        }
    }

    func testInboundCommandCodableRoundTrip() throws {
        let command = InboundCommand(
            accountID: "account",
            messageID: "message",
            senderID: "owner",
            body: "command"
        )
        let data = try JSONEncoder().encode(command)
        XCTAssertEqual(try JSONDecoder().decode(InboundCommand.self, from: data), command)
    }

    func testQueuePrunesCompletedExpiredAndOldestOverflow() {
        let now = Date()
        var commands = (0 ..< 102).map { index in
            InboundCommand(
                accountID: "account",
                messageID: "\(index)",
                senderID: "owner",
                body: "command",
                receivedAt: now.addingTimeInterval(TimeInterval(index))
            )
        }
        commands.append(InboundCommand(
            accountID: "account",
            messageID: "expired",
            senderID: "owner",
            body: "expired",
            receivedAt: now.addingTimeInterval(-WeChatCommandQueuePolicy.retentionInterval - 1)
        ))
        commands[50].status = .completed

        let result = WeChatCommandQueuePolicy.pruned(commands, now: now)
        XCTAssertEqual(result.count, 100)
        XCTAssertFalse(result.contains { $0.messageID == "expired" || $0.status == .completed })
        XCTAssertEqual(result.last?.messageID, "101")
        XCTAssertFalse(WeChatCommandQueuePolicy.canAccept(result, now: now))
    }
}

private actor WeChatMockTransport: WeChatILinkTransport {
    private var queued: [Data]
    private(set) var requests: [URLRequest] = []

    init(responses: [String]) {
        queued = responses.map { Data($0.utf8) }
    }

    func data(for request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        requests.append(request)
        guard !queued.isEmpty else { throw URLError(.badServerResponse) }
        let data = queued.removeFirst()
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: 200,
            httpVersion: "HTTP/1.1",
            headerFields: nil
        )!
        return (data, response)
    }
}
