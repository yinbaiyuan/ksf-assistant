import Foundation
import KSFAssistantCore
import XCTest

final class FeishuConfigurationPresentationTests: XCTestCase {
    func testFailedIdentityCheckDoesNotRecommendReauthorization() throws {
        let data = try fixture { object in
            var auth = object["auth"] as! [String: Any]
            auth["status"] = "failed"
            auth["identityValid"] = false
            object["auth"] = auth
        }
        let auth = try XCTUnwrap(decode(data).auth)
        XCTAssertFalse(auth.isAuthorized)
        XCTAssertEqual(auth.statusText, "授权状态待复核")
    }

    @MainActor
    func testEarlyUserCheckThenAuthorizationClearsPendingUI() async throws {
        let waiting = try fixture { object in
            object["flow"] = ["id": "oauth", "kind": "user", "state": "pending", "expiresAt": "2099-01-01T00:00:00Z"]
            var actions = object["actions"] as! [[String: Any]]
            for i in actions.indices where actions[i]["id"] as? String == "finish_auth" { actions[i]["enabled"] = true }
            object["actions"] = actions
        }
        let completed = try fixture { object in
            object["revision"] = 100
            object.removeValue(forKey: "flow")
            var auth = object["auth"] as! [String: Any]
            auth["status"] = "authorized"; auth["identityValid"] = true; auth["profileValid"] = true
            object["auth"] = auth
        }
        let early = try result(waiting, outcome: "pending")
        let session = FeishuConfigurationSession { method, _ in method == "feishu/configuration/action" ? early : completed }
        try session.restore(decode(waiting))
        await session.perform("finish_auth", confirm: true, flowID: "oauth")
        XCTAssertEqual(session.state.outcome, "pending")
        await session.read()
        XCTAssertNil(session.state.actionFlow)
        XCTAssertNil(session.state.message)
        XCTAssertEqual(session.state.outcome, "completed")
    }

    @MainActor
    func testSilentPollingDoesNotPublishTimestampOnlyChangesOrHideQRCode() async throws {
        let initial = try fixture { object in
            object["flow"] = ["id": "oauth", "kind": "user", "state": "pending", "expiresAt": "2099-01-01T00:00:00Z", "qrDataURL": "fixture-qr"]
        }
        let changed = try fixture { object in
            object["flow"] = ["id": "oauth", "kind": "user", "state": "pending", "expiresAt": "2099-01-01T00:00:00Z", "qrDataURL": "fixture-qr"]
            object["observedAt"] = "2099-01-01T00:00:00Z"
            object["refreshing"] = true
        }
        let session = FeishuConfigurationSession { _, _ in changed }
        try session.restore(decode(initial))
        var publications = 0
        session.onChange = { _ in publications += 1 }
        await session.read()
        XCTAssertEqual(publications, 0)
        XCTAssertFalse(session.state.reading)
        XCTAssertEqual(session.state.flow?.qrDataURL, "fixture-qr")
    }

    @MainActor
    func testReadFailureRetainsDisplayButDisablesGesturesUntilRecovery() async throws {
        let data = try fixture()
        var fail = true
        let session = FeishuConfigurationSession { _, _ in
            if fail { throw ConfigurationProbe.Failure.offline }
            return data
        }
        try session.restore(decode(data))
        let name = session.state.snapshot?.fact("robot")?.value
        await session.read()
        XCTAssertEqual(session.state.phase, .current)
        XCTAssertEqual(session.state.snapshot?.fact("robot")?.value, name)
        XCTAssertNotNil(session.state.refreshError)
        XCTAssertFalse(session.state.allows("start_auth"))
        fail = false
        await session.read()
        XCTAssertNil(session.state.refreshError)
        XCTAssertTrue(session.state.allows("start_auth"))
    }

    func testConnectionIndicatorDoesNotDependOnOptionalUserAuthorization() throws {
        var state = FeishuConfigurationState()
        state.snapshot = try decode(fixture { object in
            var auth = object["auth"] as! [String: Any]
            auth["status"] = "unauthorized"; auth["identityValid"] = false
            object["auth"] = auth
        })
        XCTAssertTrue(state.showsConnectionIndicator(transportReady: true, taskLinkReady: true))
        state.snapshot = try decode(fixture())
        XCTAssertTrue(state.showsConnectionIndicator(transportReady: true, taskLinkReady: true))
        XCTAssertFalse(state.showsConnectionIndicator(transportReady: true, taskLinkReady: false))
        XCTAssertFalse(state.showsConnectionIndicator(transportReady: false, taskLinkReady: true))
    }

    private func fixture(_ mutate: (inout [String: Any]) -> Void = { _ in }) throws -> Data {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let data = try Data(contentsOf: root.appendingPathComponent("Fixtures/FeishuConfiguration/snapshot.json"))
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        // Session transport fixtures explicitly enable recoverable gestures.
        // Production affordances remain covered by the shared Core wire fixture.
        var actions=object["actions"] as! [[String:Any]]
        for i in actions.indices where ["start_auth","restart","test_message"].contains(actions[i]["id"] as! String) { actions[i]["enabled"]=true }
        object["actions"]=actions
        mutate(&object)
        return try JSONSerialization.data(withJSONObject: object)
    }

    private func decode(_ data: Data) throws -> FeishuConfigurationSnapshot {
        try JSONDecoder().decode(FeishuConfigurationSnapshot.self, from: data)
    }

    private func result(_ snapshot: Data, outcome: String = "completed") throws -> Data {
        try JSONSerialization.data(withJSONObject: [
            "outcome": outcome, "snapshot": JSONSerialization.jsonObject(with: snapshot), "message": "Core 返回的结果"
        ])
    }

    private func pending(_ id: String = "flow-1", state: String = "pending", expiry: String = "2099-01-01T00:00:00.123456Z") throws -> Data {
        try fixture { object in
            object["flow"] = ["id": id, "kind": "app", "state": state, "expiresAt": expiry,
                "qrDataURL": "data:image/png;base64,fixture-\(id)", "verificationURL": "https://open.feishu.cn/fixture", "userCode": "fixture-code"]
            var actions = object["actions"] as! [[String: Any]]
            for index in actions.indices where ["finish_app", "cancel_flow"].contains(actions[index]["id"] as! String) {
                actions[index]["enabled"] = true
            }
            object["actions"] = actions
        }
    }

    @MainActor
    private func waitForCalls(_ count: Int, _ probe: ConfigurationProbe) async {
        if probe.requests.count >= count { return }
        let event = expectation(description: "configuration request \(count)")
        probe.onRequest = { if probe.requests.count == count { event.fulfill() } }
        await fulfillment(of: [event], timeout: 2)
        probe.onRequest = nil
    }

    @MainActor
    func testStartupRestoresCoreFactsDespiteAbsentSetupWithoutMutation() async throws {
        let data = try fixture()
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        XCTAssertEqual(session.state.phase, .loading)
        XCTAssertNil(session.state.snapshot)
        let read = Task { await session.read() }
        await waitForCalls(1, probe)
        XCTAssertEqual(probe.requests[0].method, "feishu/configuration/read")
        XCTAssertEqual(try probe.parameters(0)["refresh"] as? Bool, false)
        try probe.succeed(0, data)
        await read.value
        XCTAssertEqual(session.state.summaryTitle, "飞书消息连接正常")
        XCTAssertEqual(session.state.snapshot?.setup.stage, "not_started")
        XCTAssertEqual(session.state.snapshot?.fact("bot")?.state, "present")
        XCTAssertEqual(session.state.snapshot?.fact("user")?.state, "present")
        XCTAssertEqual(session.state.snapshot?.fact("permissions")?.state, "unknown")
        XCTAssertFalse(session.state.allows("create_app"))
        XCTAssertEqual(probe.requests.count, 1)
    }

    @MainActor
    func testEmptyContextCheckingSnapshotDisplaysAndDisabledActionsStayReadOnly() async throws {
        let initial = try fixture { object in
            object["contextRevision"] = ""
            object["refreshing"] = true
            object["summary"] = ["state": "checking", "title": "正在检查飞书配置", "detail": "正在读取身份与权限证据。", "tone": "neutral"]
            var actions = object["actions"] as! [[String: Any]]
            for index in actions.indices { actions[index]["enabled"] = false }
            object["actions"] = actions
        }
        let completed = try fixture { $0["revision"] = 8 }
        var methods: [String] = []
        let session = FeishuConfigurationSession { method, _ in
            methods.append(method)
            return methods.count == 1 ? initial : completed
        }
        await session.read()
        XCTAssertEqual(session.state.phase, .current)
        XCTAssertEqual(session.state.summaryTitle, "正在检查飞书配置")
        XCTAssertEqual(session.state.snapshot?.summary.state, "checking")
        XCTAssertEqual(session.state.snapshot?.context.contextRevision, "")
        XCTAssertEqual(session.state.snapshot?.refreshing, true)
        XCTAssertNil(session.state.message)
        XCTAssertFalse(session.state.allows("create_app"))
        for action in ["create_app", "start_auth", "bind_operator", "enable_outbound", "test_message"] {
            await session.perform(action, confirm: true)
        }
        XCTAssertEqual(methods, ["feishu/configuration/read"])
        await session.read()
        XCTAssertEqual(session.state.phase, .current)
        XCTAssertEqual(session.state.snapshot?.context.contextRevision, "fixture-context")
        XCTAssertEqual(session.state.snapshot?.refreshing, false)
        XCTAssertEqual(methods, ["feishu/configuration/read", "feishu/configuration/read"])
    }

    @MainActor
    func testInitialReadFailureAndIncompatibleSchemaStayUnknown() async throws {
        for invalid in [Data("{}".utf8), try fixture { $0["schemaVersion"] = 1 }] {
            let session = FeishuConfigurationSession { _, _ in invalid }
            await session.read()
            XCTAssertEqual(session.state.phase, .unknown)
            XCTAssertNil(session.state.snapshot)
            XCTAssertFalse(session.state.allows("create_app"))
            XCTAssertFalse(session.state.summaryTitle.contains("未配置"))
        }
        let session = FeishuConfigurationSession { _, _ in throw ConfigurationProbe.Failure.offline }
        await session.read()
        XCTAssertEqual(session.state.phase, .unknown)
    }

    @MainActor
    func testActionCarriesExactContextIdentityAndSeparateConfirmedWrites() async throws {
        let data = try fixture { object in
 var actions=object["actions"] as! [[String:Any]]
 for i in actions.indices where actions[i]["id"] as? String == "test_message" {actions[i]["enabled"]=true}
 object["actions"]=actions
}
        var requests: [(String, [String: Any])] = []
        let response = try result(data)
        let session = FeishuConfigurationSession { method, payload in
            requests.append((method, try XCTUnwrap(JSONSerialization.jsonObject(with: payload) as? [String: Any])))
            return response
        }
        try session.restore(decode(data))
        for action in ["logout", "enable_outbound", "test_message"] {
            await session.perform(action)
        }
        XCTAssertTrue(requests.isEmpty)
        await session.perform("enable_outbound", confirm: true)
        XCTAssertTrue(requests.isEmpty)
        await session.perform("logout", confirm: true)
        XCTAssertEqual(requests.count, 1)
        XCTAssertEqual(requests[0].0, "feishu/configuration/action")
        let parameters = requests[0].1
        XCTAssertEqual(parameters["action"] as? String, "logout")
        XCTAssertEqual(parameters["epoch"] as? String, "fixture-epoch")
        XCTAssertEqual(parameters["revision"] as? Int, 7)
        XCTAssertEqual(parameters["contextRevision"] as? String, "fixture-context")
        XCTAssertEqual(parameters["confirm"] as? Bool, true)
        XCTAssertNil(parameters["targetAlias"])
        XCTAssertNil(parameters["appSecret"])
        XCTAssertNotNil(UUID(uuidString: try XCTUnwrap(parameters["requestId"] as? String)))
        await session.perform("test_message", confirm: true, targetAlias: "fixture")
        XCTAssertEqual(requests.count, 2)
        XCTAssertEqual(requests[1].1["targetAlias"] as? String, "fixture")
        XCTAssertNotEqual(parameters["requestId"] as? String, requests[1].1["requestId"] as? String)
    }

    @MainActor
    func testNativeConfirmationDescribesExactBotSendNotOverallReadiness() throws {
        let snapshot = try decode(fixture())
        let message = try XCTUnwrap(snapshot.action("test_message")?.confirmationText(targetAlias: "fixture"))
        XCTAssertTrue(message.contains("身份：机器人（bot）"))
        XCTAssertTrue(message.contains("目标：fixture"))
        XCTAssertTrue(message.contains("正文：【KSFAssistant 接入验收】"))
        XCTAssertTrue(message.contains("仅验证本次发送，不代表整体配置已就绪"))
        XCTAssertEqual(snapshot.action("enable_outbound")?.confirmationText(), snapshot.action("enable_outbound")?.confirmation)
        XCTAssertNil(snapshot.action("finish_app")?.confirmationText())
        XCTAssertNil(snapshot.action("finish_auth")?.confirmationText())
    }

    func testConfirmationDetailsAppendActualFeatureModeAndAppIDWithoutSecret() throws {
        let snapshot = try decode(fixture())
        XCTAssertNil(snapshot.action("set_feature"))
        XCTAssertNil(snapshot.action("enable_outbound"))
        XCTAssertNil(snapshot.action("connect_app"))
        XCTAssertNil(snapshot.action("bind_operator"))
        let logout = try XCTUnwrap(snapshot.action("logout"))
        XCTAssertEqual(logout.confirmationText(), logout.confirmation)
    }

    @MainActor
    func testMissingCoreConfirmationRejectsMutationsButAllowsEmptyFinishText() async throws {
        let initial = try fixture { object in
            object["flow"] = ["id": "flow-1", "kind": "app", "state": "pending"]
            var actions = object["actions"] as! [[String: Any]]
            for index in actions.indices {
                actions[index]["confirmation"] = "  "
                actions[index]["enabled"] = true
            }
            object["actions"] = actions
        }
        let response = try result(fixture())
        var requests: [[String: Any]] = []
        let session = FeishuConfigurationSession { _, payload in
            requests.append(try XCTUnwrap(JSONSerialization.jsonObject(with: payload) as? [String: Any]))
            return response
        }
        try session.restore(decode(initial))
        for action in ["create_app", "start_auth", "logout", "set_feature", "enable_outbound", "test_message", "restart", "cancel_flow"] {
            XCTAssertNil(session.state.snapshot?.action(action)?.confirmationText(targetAlias: "fixture", mode: "live", featureTitle: "文档与知识库", appID: "fixture-app"))
            await session.perform(action, confirm: true, flowID: "flow-1")
        }
        XCTAssertTrue(requests.isEmpty)
        await session.perform("finish_app", flowID: "flow-1")
        XCTAssertEqual(requests.count, 1)
        XCTAssertEqual(requests.first?["action"] as? String, "finish_app")
        XCTAssertEqual(requests.first?["confirm"] as? Bool, false)
    }

    @MainActor
    func testSubmissionRechecksHostAvailabilityWithoutBlockingRead() async throws {
        let data = try fixture()
        let response = try result(data)
        var blockedReason: String? = "locked"
        var methods: [String] = []
        let session = FeishuConfigurationSession(canSubmit: { blockedReason == nil }) { method, _ in
            methods.append(method)
            return method.hasSuffix("/read") ? data : response
        }
        await session.read()
        for reason in ["locked", "hidden", "quit", "business-approval"] {
            blockedReason = reason
            await session.perform("logout", confirm: true)
            XCTAssertEqual(methods, ["feishu/configuration/read"], reason)
        }
        blockedReason = nil
        await session.perform("logout", confirm: true)
        XCTAssertEqual(methods, ["feishu/configuration/read", "feishu/configuration/action"])
    }

    func testMainActionsRespectCoreAffordancesAndUnknownIsNotMissing() throws {
        var state = FeishuConfigurationState()
        state.phase = .current
        state.snapshot = try decode(fixture { object in
 var actions=object["actions"] as! [[String:Any]]
 for i in actions.indices where actions[i]["id"] as? String == "start_auth" {actions[i]["enabled"]=false}
 object["actions"]=actions
})
        XCTAssertNil(state.priorityAction)
        XCTAssertTrue(state.pendingSetupActions.isEmpty)

        state.snapshot = try decode(fixture { object in
            var facts = object["facts"] as! [[String: Any]]
            for index in facts.indices where facts[index]["id"] as? String == "authorizedUser" {
                facts[index]["state"] = "missing"
                facts[index]["value"] = "按需授权（不影响消息和卡片）"
            }
            object["facts"] = facts
            var actions = object["actions"] as! [[String: Any]]
            for index in actions.indices where actions[index]["id"] as? String == "start_auth" {
                actions[index]["enabled"] = false
            }
            object["actions"] = actions
        })
        XCTAssertNil(state.priorityAction, "Optional user OAuth must not create a desktop action unless Core enables it")

        for userState in ["missing", "unknown", "present"] {
            state.snapshot = try decode(fixture { object in
                var facts = object["facts"] as! [[String: Any]]
                let index = facts.firstIndex { $0["id"] as? String == "user" }!
                facts[index]["state"] = userState
                object["facts"] = facts
                var actions = object["actions"] as! [[String: Any]]
                for actionIndex in actions.indices where ["enable_outbound", "start_auth"].contains(actions[actionIndex]["id"] as! String) {
                    actions[actionIndex]["enabled"] = actions[actionIndex]["id"] as? String == "start_auth" && userState == "missing"
                }
                object["actions"] = actions
            })
            XCTAssertEqual(state.priorityAction, userState == "missing" ? "start_auth" : nil)
            XCTAssertTrue(state.pendingSetupActions.isEmpty)
        }
    }

    func testFactHelpIncludesCoreSourceAndCheckTime() throws {
        let fact = try XCTUnwrap(decode(fixture()).fact("user"))
        XCTAssertTrue(fact.evidenceHelp.contains(fact.source))
        XCTAssertTrue(fact.evidenceHelp.contains("检查时间：2026-09-06T10:00:00Z"))
        let unknown = try JSONDecoder().decode(FeishuConfigurationSnapshot.Fact.self, from: Data("""
        {"id":"user","title":"用户授权","state":"unknown","value":"尚未验证","source":"官方 CLI","stale":false}
        """.utf8))
        XCTAssertTrue(unknown.evidenceHelp.contains("检查时间：未知"))
    }

    @MainActor
    func testDelayedReadCannotOverwriteNewActionOrRestoreOldQR() async throws {
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        try session.restore(decode(fixture()))
        let read = Task { await session.read() }
        await waitForCalls(1, probe)
        let action = Task { await session.perform("start_auth", confirm: true) }
        await waitForCalls(2, probe)
        let latest = try fixture { $0["revision"] = 8; $0["contextRevision"] = "new-context" }
        try probe.succeed(1, result(latest))
        await action.value
        try probe.succeed(0, pending("old-flow"))
        await read.value
        XCTAssertEqual(session.state.snapshot?.revision, 8)
        XCTAssertNil(session.state.flow)
        XCTAssertEqual(session.state.outcome, "completed")
    }

    @MainActor
    func testNewerReadAndEpochInvalidateOlderResponses() async throws {
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        try session.restore(decode(fixture()))
        let older = Task { await session.read() }
        await waitForCalls(1, probe)
        let newer = Task { await session.read() }
        await waitForCalls(2, probe)
        try probe.succeed(1, fixture { $0["epoch"] = "new-epoch"; $0["revision"] = 1 })
        await newer.value
        try probe.succeed(0, fixture { $0["revision"] = 1000 })
        await older.value
        XCTAssertEqual(session.state.snapshot?.epoch, "new-epoch")
        XCTAssertEqual(session.state.snapshot?.revision, 1)
    }

    @MainActor
    func testCancelledFlowCannotReturnFromLateObservation() async throws {
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        try session.restore(decode(pending()))
        await session.perform("cancel_flow", confirm: true, flowID: "wrong-flow")
        XCTAssertTrue(probe.requests.isEmpty)
        let read = Task { await session.read() }
        await waitForCalls(1, probe)
        let cancel = Task { await session.perform("cancel_flow", confirm: true, flowID: "flow-1") }
        await waitForCalls(2, probe)
        XCTAssertEqual(try probe.parameters(1)["flowId"] as? String, "flow-1")
        // Keep the QR region stable until cancellation is confirmed, with controls disabled.
        XCTAssertEqual(session.state.flow?.id, "flow-1")
        XCTAssertFalse(session.state.allows("cancel_flow"))
        try probe.succeed(1, result(fixture { $0["revision"] = 8 }))
        await cancel.value
        try probe.succeed(0, pending())
        await read.value
        XCTAssertNil(session.state.flow)
        XCTAssertEqual(session.state.snapshot?.fact("application")?.state, "present")
    }

    @MainActor
    func testOnlyPendingMatchingUnexpiredFlowProvidesQR() throws {
        let session = FeishuConfigurationSession { _, _ in throw ConfigurationProbe.Failure.offline }
        try session.restore(decode(pending()))
        XCTAssertEqual(session.state.flow?.id, "flow-1")
        for state in ["completed", "failed", "expired", "cancelled", "future"] {
            try session.restore(decode(pending(state: state)))
            XCTAssertNil(session.state.flow, state)
        }
        for expiry in ["2000-01-01T00:00:00Z", "not-a-date"] {
            try session.restore(decode(pending(expiry: expiry)))
            XCTAssertNil(session.state.flow)
        }
        try session.restore(decode(fixture { object in
            object["setup"] = ["version": 1, "stage": "app_pending", "verificationURL": "https://open.feishu.cn/old", "userCode": "old"]
            object["auth"] = ["schemaVersion": 1, "status": "pending", "identity": "user", "profile": "default",
                "identityValid": true, "profileValid": true, "grantedScopeCount": 0, "qrDataURL": "old-auth-qr"]
        }))
        XCTAssertNil(session.state.flow)
    }

    @MainActor
    func testCompletedFlowCanBeFinishedWithoutRestoringQRCode() async throws {
        var parameters: [String: Any] = [:]
        let response = try result(fixture())
        let session = FeishuConfigurationSession { _, payload in
            parameters = try XCTUnwrap(JSONSerialization.jsonObject(with: payload) as? [String: Any])
            return response
        }
        try session.restore(decode(pending(state: "completed")))
        XCTAssertNil(session.state.flow)
        XCTAssertEqual(session.state.actionFlow?.id, "flow-1")
        await session.perform("finish_app", flowID: "flow-1")
        XCTAssertEqual(parameters["action"] as? String, "finish_app")
        XCTAssertEqual(parameters["flowId"] as? String, "flow-1")
        XCTAssertNil(session.state.flow)
    }

    @MainActor
    func testFailedActionDoesNotReplayAndPreservesPriorEvidenceAsUnconfirmed() async throws {
        var count = 0
        let session = FeishuConfigurationSession { _, _ in count += 1; throw ConfigurationProbe.Failure.offline }
        try session.restore(decode(pending()))
        await session.perform("finish_app", flowID: "flow-1")
        XCTAssertEqual(session.state.outcome, "unknown")
        XCTAssertEqual(session.state.phase, .unknown)
        XCTAssertEqual(session.state.snapshot?.fact("bot")?.state, "present")
        XCTAssertNil(session.state.flow)
        await session.perform("finish_app", flowID: "flow-1")
        XCTAssertEqual(count, 2) // One mutation and one read-only result query.
        XCTAssertFalse(session.state.acting)
    }

    @MainActor
    func testInFlightActionsAreSerializedAndConfirmationCannotChangeContext() async throws {
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        let snapshot = try decode(fixture())
        try session.restore(snapshot)
        let first = Task { await session.perform("logout", confirm: true, expectedContext: snapshot.context) }
        await waitForCalls(1, probe)
        await session.perform("logout", confirm: true)
        XCTAssertEqual(probe.requests.count, 1)
        try probe.succeed(0, result(fixture { $0["revision"] = 8; $0["contextRevision"] = "changed" }))
        await first.value
        await session.perform("logout", confirm: true, expectedContext: snapshot.context)
        XCTAssertEqual(probe.requests.count, 1)
        XCTAssertEqual(session.state.message, "配置已更新，请重新确认本次操作。")
    }

    @MainActor
    func testConfirmationSurvivesPresentationRevisionAndSendsLatestRevision() async throws {
        let original = try decode(fixture())
        let refreshed = try fixture { object in
            object["revision"] = 8
            var summary = object["summary"] as! [String: Any]
            summary["detail"] = "连接观察已刷新；配置上下文不变。"
            object["summary"] = summary
        }
        let response = try result(refreshed)
        var payloads: [[String: Any]] = []
        let session = FeishuConfigurationSession { method, payload in
            if method == "feishu/configuration/read" { return refreshed }
            payloads.append(try XCTUnwrap(JSONSerialization.jsonObject(with: payload) as? [String: Any]))
            return response
        }
        try session.restore(original)
        await session.read()
        XCTAssertEqual(session.state.snapshot?.revision, 8)
        await session.perform("logout", confirm: true, expectedContext: original.context)
        XCTAssertEqual(payloads.count, 1)
        let payload = try XCTUnwrap(payloads.first)
        XCTAssertEqual(payload["revision"] as? Int, 8)
        XCTAssertEqual(payload["epoch"] as? String, original.epoch)
        XCTAssertEqual(payload["contextRevision"] as? String, original.contextRevision)
        XCTAssertEqual(session.state.outcome, "completed")
    }

    @MainActor
    func testConfirmationRejectsEpochOrContextChangesIndependently() async throws {
        let original = try decode(fixture())
        for changedField in ["epoch", "contextRevision"] {
            let refreshed = try fixture { $0[changedField] = "changed" }
            var actionCount = 0
            let session = FeishuConfigurationSession { method, _ in
                if method == "feishu/configuration/action" { actionCount += 1 }
                return refreshed
            }
            try session.restore(original)
            await session.read()
            await session.perform("logout", confirm: true, expectedContext: original.context)
            XCTAssertEqual(actionCount, 0, changedField)
            XCTAssertEqual(session.state.message, "配置已更新，请重新确认本次操作。", changedField)
        }
    }

    @MainActor
    func testConfirmationRechecksCurrentActionAndFlowAfterPresentationRefresh() async throws {
        let original = try decode(pending())
        let disabled = try fixture { object in
            object["revision"] = 8
            var actions = object["actions"] as! [[String: Any]]
            for index in actions.indices where actions[index]["id"] as? String == "logout" {
                actions[index]["enabled"] = false
            }
            object["actions"] = actions
        }
        let replacementFlow = try pending("flow-2")
        var refreshed = disabled
        var payloads: [[String: Any]] = []
        var response = try result(disabled)
        let session = FeishuConfigurationSession { method, payload in
            if method == "feishu/configuration/read" { return refreshed }
            payloads.append(try XCTUnwrap(JSONSerialization.jsonObject(with: payload) as? [String: Any]))
            return response
        }
        try session.restore(original)
        await session.read()
        await session.perform("logout", confirm: true, expectedContext: original.context)
        XCTAssertTrue(payloads.isEmpty)

        var flowObject = try XCTUnwrap(JSONSerialization.jsonObject(with: replacementFlow) as? [String: Any])
        flowObject["revision"] = 9
        refreshed = try JSONSerialization.data(withJSONObject: flowObject)
        response = try result(refreshed)
        await session.read()
        await session.perform("cancel_flow", confirm: true, flowID: "flow-1", expectedContext: original.context)
        XCTAssertTrue(payloads.isEmpty)
        await session.perform("cancel_flow", confirm: true, flowID: "flow-2", expectedContext: original.context)
        XCTAssertEqual(payloads.count, 1)
        XCTAssertEqual(payloads.first?["flowId"] as? String, "flow-2")
        XCTAssertEqual(payloads.first?["revision"] as? Int, 9)
        XCTAssertEqual(session.state.outcome, "completed")
    }

    @MainActor
    func testRetiredConnectAndFeatureActionsNeverReachTransport() async throws {
        let data = try fixture()
        var payloads: [[String: Any]] = []
        let response = try result(data)
        let session = FeishuConfigurationSession { _, payload in
            payloads.append(try XCTUnwrap(JSONSerialization.jsonObject(with: payload) as? [String: Any]))
            return response
        }
        try session.restore(decode(data))
        await session.perform("connect_app", confirm: true, appID: "fixture-app", appSecret: "fixture-secret")
        XCTAssertTrue(payloads.isEmpty)
        await session.perform("set_feature", feature: "actionbox", mode: "live")
        XCTAssertTrue(payloads.isEmpty)
        await session.perform("set_feature", confirm: true, feature: "actionbox", mode: "live")
        XCTAssertTrue(payloads.isEmpty) // Retired actions never reach transport.
        XCTAssertFalse(String(describing: session.state).contains("fixture-secret"))
    }

    @MainActor
    func testApplicationObserverCompletesLoginWithoutPageReadOrAction() async throws {
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        defer { session.shutdown() }
        session.startPolling(sleep: { try await Task.sleep(nanoseconds: 1_000_000) })
        await waitForCalls(1, probe)
        try probe.succeed(0, pending("background-flow"))
        await waitForCalls(2, probe)
        try probe.succeed(1, fixture { object in
            object["revision"] = 100
            object.removeValue(forKey: "flow")
            var auth = object["auth"] as! [String: Any]
            auth["status"] = "authorized"; auth["identityValid"] = true; auth["profileValid"] = true
            object["auth"] = auth
        })
        await waitForCalls(3, probe)
        XCTAssertEqual(session.state.snapshot?.auth?.isAuthorized, true)
        XCTAssertNil(session.state.flow)
        XCTAssertTrue(probe.requests.allSatisfy { $0.method == "feishu/configuration/read" })
        session.shutdown()
        try probe.succeed(2, fixture())
    }

    @MainActor
    func testPollingIsReadOnlyAndCanRestartAfterExplicitStop() async throws {
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        let sleepReached = expectation(description: "poll interval")
        session.startPolling(sleep: {
            sleepReached.fulfill()
            try await Task.sleep(nanoseconds: 60_000_000_000)
        })
        session.startPolling()
        await waitForCalls(1, probe)
        try probe.succeed(0, fixture { $0["refreshing"] = true })
        await fulfillment(of: [sleepReached], timeout: 2)
        XCTAssertEqual(session.state.snapshot?.refreshing, true)
        session.stopPolling()
        XCTAssertFalse(session.state.reading)
        session.startPolling()
        await waitForCalls(2, probe)
        session.stopPolling()
        try probe.succeed(1, pending("hidden-flow"))
        await Task.yield()
        XCTAssertNil(session.state.flow)
        XCTAssertEqual(probe.requests.map(\.method), ["feishu/configuration/read", "feishu/configuration/read"])
        XCTAssertEqual(try probe.parameters(1)["refresh"] as? Bool, false)
    }

    @MainActor
    func testShutdownDiscardsLateActionAndDoesNotSendRemoteCancellation() async throws {
        let probe = ConfigurationProbe()
        let session = FeishuConfigurationSession(transport: probe.request)
        try session.restore(decode(fixture()))
        let action = Task { await session.perform("start_auth", confirm: true) }
        await waitForCalls(1, probe)
        session.shutdown()
        try probe.succeed(0, result(pending("after-exit"), outcome: "pending"))
        await action.value
        await session.read()
        await session.perform("logout", confirm: true)
        session.startPolling()
        await Task.yield()
        XCTAssertEqual(probe.requests.count, 1)
        XCTAssertNil(session.state.snapshot)
        XCTAssertNil(session.state.flow)
        XCTAssertFalse(session.state.acting)
    }

    @MainActor
    func testCheckRefreshAndActionOutcomesRemainCoreSupplied() async throws {
        for outcome in ["pending", "failed", "unknown", "completed"] {
            let data = try fixture()
            let response = try result(data, outcome: outcome)
            var reads: [Bool] = []
            let session = FeishuConfigurationSession { method, payload in
                if method.hasSuffix("/read") {
                    reads.append((try JSONSerialization.jsonObject(with: payload) as! [String: Bool])["refresh"]!)
                    return data
                }
                return response
            }
            await session.read(refresh: true)
            await session.perform("start_auth", confirm: true)
            XCTAssertEqual(reads, [true])
            XCTAssertEqual(session.state.outcome, outcome)
            XCTAssertEqual(session.state.message, "Core 返回的结果")
            XCTAssertEqual(session.state.summaryTitle, "飞书消息连接正常")
        }
    }

    @MainActor
    func testMissingPermissionEvidenceNeverDecodesAsVerified() throws {
        let snapshot = try decode(fixture())
        XCTAssertEqual(snapshot.overview?.permissions.application, "unknown")
        XCTAssertEqual(snapshot.overview?.permissions.user, "unknown")
        XCTAssertEqual(snapshot.overview?.permissions.missing, [])
        let summary = snapshot.summary
        XCTAssertEqual(summary.state, "connected")
        XCTAssertEqual(summary.tone, "success")
    }
}

@MainActor
private final class ConfigurationProbe {
    enum Failure: Error { case offline }
    struct Request { let method: String; let payload: Data }
    var requests: [Request] = []
    var onRequest: (() -> Void)?
    private var pending: [Int: CheckedContinuation<Data, Error>] = [:]

    func request(_ method: String, _ payload: Data) async throws -> Data {
        let index = requests.count
        requests.append(Request(method: method, payload: payload))
        return try await withCheckedThrowingContinuation { continuation in
            pending[index] = continuation
            onRequest?()
        }
    }

    func parameters(_ index: Int) throws -> [String: Any] {
        try XCTUnwrap(JSONSerialization.jsonObject(with: requests[index].payload) as? [String: Any])
    }

    func succeed(_ index: Int, _ data: Data) throws {
        let continuation = try XCTUnwrap(pending.removeValue(forKey: index))
        continuation.resume(returning: data)
    }
}
