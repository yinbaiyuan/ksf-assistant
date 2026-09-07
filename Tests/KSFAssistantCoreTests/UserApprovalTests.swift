import Foundation
import KSFAssistantCore
import XCTest

final class UserApprovalTests: XCTestCase {
    private func fixture() -> [String: Any] {
        ["schemaVersion": 1, "request": [
            "id": "request-1", "title": "发送消息", "user": "测试用户", "application": "测试应用",
            "action": "发送", "target": "专用测试群", "content": String(repeating: "完整正文\n", count: 1000),
            "attachments": [["name": "test.txt", "size": 4, "sha256": String(repeating: "a", count: 64)]],
            "source": "来源未验证", "expiresAt": ISO8601DateFormatter().string(from: Date().addingTimeInterval(120))
        ]]
    }

    func testCompletePlainTextDetailsAndVersionedNull() throws {
        let request = try XCTUnwrap(UserApprovalPoll.decode(JSONSerialization.data(withJSONObject: fixture())).request)
        XCTAssertTrue(request.details.contains(request.content))
        for field in ["测试用户", "测试应用", "发送", "专用测试群", "test.txt", "来源未验证", String(repeating: "a", count: 64)] {
            XCTAssertTrue(request.details.contains(field))
        }
        XCTAssertNil(try UserApprovalPoll.decode(Data(#"{"schemaVersion":1,"request":null}"#.utf8)).request)
    }

    func testInvalidContractAndExpiredRequestsFailClosed() throws {
        for malformed in [#"{"schemaVersion":2,"request":null}"#, #"{"schemaVersion":1}"#, #"{"schemaVersion":true,"request":null}"#, #"{"schemaVersion":1,"request":null,"approved":true}"#] {
            XCTAssertThrowsError(try UserApprovalPoll.decode(Data(malformed.utf8)))
        }
        for (key, value) in [("expiresAt", "2020-01-01T00:00:00Z"), ("user", ""), ("application", ""), ("id", ""), ("content", "bad\0content")] {
            var payload = fixture()
            var request = try XCTUnwrap(payload["request"] as? [String: Any])
            request[key] = value
            payload["request"] = request
            XCTAssertThrowsError(try UserApprovalPoll.decode(JSONSerialization.data(withJSONObject: payload)))
        }
    }

    func testGoRFC3339NanoAndEmptyAttachments() throws {
        var payload = fixture()
        var request = try XCTUnwrap(payload["request"] as? [String: Any])
        request["expiresAt"] = "2026-09-06T12:00:00.123456789+08:00"
        request["attachments"] = [] as [[String: Any]]
        payload["request"] = request
        let now = try XCTUnwrap(ISO8601DateFormatter().date(from: "2026-09-06T03:59:00Z"))
        let decoded = try XCTUnwrap(UserApprovalPoll.decode(JSONSerialization.data(withJSONObject: payload), now: now).request)
        XCTAssertEqual(decoded.attachments.count, 0)
        XCTAssertEqual(try XCTUnwrap(decoded.expiration).timeIntervalSince(now), 60.123, accuracy: 0.002)
    }

    func testOptionalPreviewContractAndLegacyFallback() throws {
        var payload = fixture()
        var request = try XCTUnwrap(payload["request"] as? [String: Any])
        XCTAssertNil(try UserApprovalPoll.decode(JSONSerialization.data(withJSONObject: payload)).request?.preview)
        request["preview"] = ["content": "完整修改内容", "confirmLabel": "更新", "destructive": true] as [String: Any]
        payload["request"] = request
        let decoded = try XCTUnwrap(UserApprovalPoll.decode(JSONSerialization.data(withJSONObject: payload)).request)
        XCTAssertEqual(decoded.preview?.content, "完整修改内容")
        XCTAssertEqual(decoded.preview?.confirmLabel, "更新")
        XCTAssertEqual(decoded.preview?.destructive, true)
        XCTAssertTrue(decoded.details.contains(decoded.content))
        for preview: Any in [NSNull(), ["content": "x", "confirmLabel": "发送", "destructive": 1], ["content": "x", "confirmLabel": "发\n送", "destructive": false], ["content": "x", "confirmLabel": "发送", "destructive": false, "approved": true]] {
            request["preview"] = preview
            payload["request"] = request
            XCTAssertThrowsError(try UserApprovalPoll.decode(JSONSerialization.data(withJSONObject: payload)), "bad preview: \(preview)")
        }
    }

    func testDecisionRequiresExplicitVersionedBoolean() throws {
        XCTAssertTrue(try UserApprovalDecision.decode(Data(#"{"schemaVersion":1,"accepted":true}"#.utf8)).accepted)
        XCTAssertFalse(try UserApprovalDecision.decode(Data(#"{"schemaVersion":1,"accepted":false}"#.utf8)).accepted)
        for malformed in [#"{"schemaVersion":1}"#, #"{"schemaVersion":1,"accepted":1}"#, #"{"schemaVersion":2,"accepted":true}"#, #"{"schemaVersion":1,"accepted":true,"token":"unexpected"}"#] {
            XCTAssertThrowsError(try UserApprovalDecision.decode(Data(malformed.utf8)))
        }
    }

    func testNativeHostIsolationAndLifecycleWiring() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let host = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UserApprovalController.swift"))
        let model = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UsageViewModel.swift"))
        XCTAssertTrue(host.contains("NSPanel"))
        XCTAssertTrue(host.contains("UserApprovalContentView"))
        XCTAssertFalse(host.contains("Codex 请求"))
        let content = try String(contentsOf: root.appendingPathComponent("Sources/KSFAssistant/UserApprovalContentView.swift"))
        XCTAssertTrue(content.contains("NSScrollView"))
        XCTAssertTrue(content.contains("textBlock(request.details"))
        XCTAssertTrue(host.contains("panel.defaultButtonCell = nil"))
        XCTAssertTrue(content.contains("event.keyCode == 36 || event.keyCode == 76"))
        XCTAssertTrue(content.contains("cancelOperation"))
        XCTAssertTrue(host.contains("CGSessionCopyCurrentDictionary"))
        XCTAssertTrue(host.contains("interactive: false"))
        XCTAssertFalse(host.contains("runModal"))
        XCTAssertTrue(model.contains("userApprovalController.start()"))
        XCTAssertTrue(model.contains("await userApprovalController.stop()"))
    }
}
