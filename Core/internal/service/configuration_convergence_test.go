package service

import (
	"context"
	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privatestore"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigurationPermissionCompletenessAndIdentityAreIndependent(t *testing.T) {
	for _, test := range []struct {
		user, app, want string
		auth            bool
	}{{"present", "present", "已授权", false}, {"missing", "present", "本次功能尚缺权限", false}, {"present", "missing", "已授权", false}, {"unknown", "present", "已授权，权限待核验", false}} {
		state := configurationReadyState()
		state.data.evidence.UserPermissions = test.user
		state.data.evidence.ApplicationPermissions = test.app
		state.data.evidence.UserName = "测试用户"
		state.data.evidence.BotName = "测试机器人"
		state.data.evidence.OperatorState = "present"
		state.data.evidence.OperatorAlias = "fixture"
		state.data.evidence.ServiceVersion = "actual-service"
		state.data.evidence.CLIVersion = "actual-cli"
		snapshot := configurationTestSnapshot(&state)
		if !strings.Contains(configurationFact(snapshot, "authorizedUser").Value, "fixture · 已绑定") || !snapshot.Auth.IdentityValid {
			t.Fatal("identity and completeness conflated")
		}
		a, _ := configurationActionByID(snapshot, "start_auth")
		if a.Enabled != test.auth {
			t.Fatal("wrong recovery affordance", test)
		}
		if snapshot.Diagnostics.ServiceVersion != "actual-service" || snapshot.Diagnostics.CLIVersion != "actual-cli" || configurationFact(snapshot, "robot").Value != "测试机器人" {
			t.Fatal("runtime evidence lost")
		}
		for _, id := range []string{"set_feature", "enable_outbound"} {
			if _, ok := configurationActionByID(snapshot, id); ok {
				t.Fatal("retired action exposed")
			}
		}
	}
}
func TestConfigurationUnknownReceiptSurvivesRestartAndCannotBeReplayed(t *testing.T) {
	root := t.TempDir()
	s := &Service{feishuDataRoot: root}
	id := "stable-original-request"
	request := ConfigurationActionRequest{RequestID: id, Action: "test_message", Confirm: true, TargetAlias: "fixture"}
	if err := s.saveConfigurationReceipt(ConfigurationReceipt{SchemaVersion: 1, RequestID: id, Action: "test_message", Digest: configurationHash(request), Outcome: "unknown", Stage: "submitted", Message: "原请求待核实"}); err != nil {
		t.Fatal(err)
	}
	restarted := &Service{feishuDataRoot: root}
	defer restarted.closeConfiguration()
	for i := 0; i < 2; i++ {
		result, err := restarted.ConfigurationResult(context.Background(), id)
		if err != nil || result.Outcome != "unknown" {
			t.Fatal("restart lost unknown result", err)
		}
	}
	result, err := restarted.ApplyFeishuConfiguration(context.Background(), request)
	if err != nil || result.Outcome != "unknown" || result.Message != "原请求待核实" {
		t.Fatal("unknown request replayed", err)
	}
}

func TestConfigurationVersionAndOwnerChangesAdvancePresentation(t *testing.T) {
	state := configurationReadyState()
	a := configurationTestSnapshot(&state)
	state.data.evidence.BotName = "new name"
	state.data.evidence.ServiceVersion = "new-version"
	b := state.snapshot(time.Now())
	if b.Revision <= a.Revision {
		t.Fatal("name/version change not projected")
	}
	state.data.evidence = feishuprotocol.ConfigurationEvidence{ApplicationState: "missing"}
	c := state.snapshot(time.Now())
	if configurationFact(c, "robot").Value != "名称暂不可用" {
		t.Fatal("name leaked across application context")
	}
}

func TestMissingUserOAuthDoesNotDisableBotConnection(t *testing.T) {
	state := configurationReadyState()
	state.data.evidence.Auth.Status = "unauthorized"
	state.data.evidence.Auth.IdentityValid = false
	state.data.evidence.ApplicationPermissions = "present"
	state.data.evidence.UserPermissions = "unknown"
	state.data.evidence.BotName = "测试机器人"
	state.data.evidence.OperatorState = "present"
	state.data.evidence.OperatorAlias = "我"
	snapshot := configurationTestSnapshot(&state)
	action, _ := configurationActionByID(snapshot, "start_auth")
	user := configurationFact(snapshot, "authorizedUser")
	if configurationFact(snapshot, "robot").Value != state.data.evidence.BotName {
		t.Fatal("missing user OAuth hid the verified bot")
	}
	if user.Title != "远程操作者" || user.Value != "我 · 已绑定" {
		t.Fatal("optional user OAuth presented as a connection fault", user)
	}
	if action.Enabled || configurationFact(snapshot, "taskConnection").Value != "正常" {
		t.Fatal("missing user OAuth disabled the bot connection", action)
	}
	for _, issue := range snapshot.Issues {
		if issue.Code == "task_connection_unavailable" {
			t.Fatal("logout displayed as a fault")
		}
	}
}

func TestCompletedVerifiedUserFlowRestoresLogoutAndRemovesDeadStep(t *testing.T) {
	state := configurationReadyState()
	state.data.evidence.UserPermissions = "present"
	state.data.evidence.ApplicationPermissions = "present"
	state.data.flow = &feishuprotocol.ConfigurationFlow{ID: "done-user", Kind: "user", State: "completed"}
	snapshot := configurationTestSnapshot(&state)
	if snapshot.Flow != nil {
		t.Fatal("completed OAuth kept dead QR step")
	}
	action, _ := configurationActionByID(snapshot, "logout")
	if !action.Enabled {
		t.Fatal("verified OAuth lost logout")
	}
}

func TestObservationFailureDoesNotReplaceSuccessfulDisplayWithConnectionFault(t *testing.T) {
	state := configurationReadyState()
	state.data.evidence.BotName = "Fixture Bot"
	state.data.evidence.OperatorState = "present"
	state.data.evidence.UserPermissions = "present"
	state.data.evidence.ApplicationPermissions = "present"
	before := configurationTestSnapshot(&state)
	state.data.evidenceFailed = true
	state.data.quickFailed = true
	after := configurationTestSnapshot(&state)
	for _, id := range []string{"robot", "authorizedUser", "taskConnection"} {
		if configurationFact(before, id).Value != configurationFact(after, id).Value {
			t.Fatalf("observation failure changed display: %s", id)
		}
	}
	for _, issue := range after.Issues {
		if issue.Code == "task_connection_unavailable" {
			t.Fatal("failed read presented as disconnected")
		}
	}
	if action, _ := configurationActionByID(after, "test_message"); action.Enabled {
		t.Fatal("stale evidence allowed write")
	}
	state.data.connection.InboundConnection = false
	state.data.quickFailed = false
	disconnected := configurationTestSnapshot(&state)
	if configurationFact(disconnected, "taskConnection").Value != "需要处理" {
		t.Fatal("real disconnection hidden")
	}
}

func TestVerifiedLogoutEndsEveryEarlierConnectionFlowWithoutResolvingOtherWrites(t *testing.T) {
	later := ConfigurationReceipt{Action: "logout", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-07T16:26:00Z"}
	for _, action := range []string{"create_app", "finish_app", "start_auth", "finish_auth", "cancel_flow"} {
		older := ConfigurationReceipt{Action: action, Outcome: "pending", Stage: "submitted", UpdatedAt: "2026-09-07T16:25:00Z"}
		if !configurationFlowEndedByLogout(older, []ConfigurationReceipt{later}) {
			t.Fatalf("old %s flow still blocks a new connection", action)
		}
	}
	older := ConfigurationReceipt{Action: "create_app", Outcome: "pending", Stage: "submitted", UpdatedAt: "2026-09-07T16:25:00Z"}
	for _, change := range []func(*ConfigurationReceipt){func(r *ConfigurationReceipt) { r.Outcome = "unknown" }, func(r *ConfigurationReceipt) { r.Stage = "submitted" }, func(r *ConfigurationReceipt) { r.UpdatedAt = "2026-09-07T16:24:00Z" }} {
		other := later
		change(&other)
		if configurationFlowEndedByLogout(older, []ConfigurationReceipt{other}) {
			t.Fatal("unverified or earlier logout cleared the flow")
		}
	}
	older.Action = "test_message"
	if configurationFlowEndedByLogout(older, []ConfigurationReceipt{later}) {
		t.Fatal("logout resolved an unknown message write")
	}
}

func TestLaterVerifiedConnectionSupersedesUnknownLogoutWithoutClaimingItSucceeded(t *testing.T) {
	s := &Service{feishuDataRoot: t.TempDir()}
	defer s.closeConfiguration()
	logout := ConfigurationReceipt{SchemaVersion: 1, RequestID: "old-logout", Action: "logout", ApplicationID: "old-app", Outcome: "unknown", Stage: "submitted", UpdatedAt: "2026-09-18T03:29:27Z"}
	testMessage := ConfigurationReceipt{SchemaVersion: 1, RequestID: "later-message", Action: "test_message", ApplicationID: "connected-app", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-18T04:15:22Z"}
	for _, receipt := range []ConfigurationReceipt{logout, testMessage} {
		if err := privatestore.WriteJSON(s.receiptPath(receipt.RequestID), receipt); err != nil {
			t.Fatal(err)
		}
	}
	if !configurationLogoutSupersededByConnection(logout, s.loadConfigurationReceipts()) {
		t.Fatal("later verified connection did not release the old logout lock")
	}
	for _, failure := range s.configurationFailures() {
		if failure.Action == "logout" {
			t.Fatal("superseded logout still blocks the current action")
		}
	}
	s.persistSupersededLogoutOutcomes()
	var resolved ConfigurationReceipt
	if missing, err := privatestore.ReadJSON(s.receiptPath(logout.RequestID), &resolved); err != nil || missing {
		t.Fatal(err)
	}
	if resolved.Outcome != "resolved" || resolved.Stage != "verified" || resolved.Code != "superseded_by_connection" || resolved.ApplicationID != "" {
		t.Fatalf("superseded logout was not preserved as a scrubbed terminal result: %+v", resolved)
	}
}

func TestUnknownLogoutIsNotSupersededByUnverifiedOrEarlierConnection(t *testing.T) {
	logout := ConfigurationReceipt{Action: "logout", Outcome: "unknown", Stage: "submitted", UpdatedAt: "2026-09-18T03:29:27Z"}
	for _, later := range []ConfigurationReceipt{
		{Action: "test_message", ApplicationID: "app", Outcome: "pending", Stage: "submitted", UpdatedAt: "2026-09-18T04:15:22Z"},
		{Action: "test_message", ApplicationID: "", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-18T04:15:22Z"},
		{Action: "test_message", ApplicationID: "app", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-18T03:15:22Z"},
		{Action: "restart", ApplicationID: "app", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-18T04:15:22Z"},
	} {
		if configurationLogoutSupersededByConnection(logout, []ConfigurationReceipt{later}) {
			t.Fatalf("insufficient connection evidence released logout: %+v", later)
		}
	}
}

func TestLogoutScrubsAndCancelsEarlierAuthorizationReceipt(t *testing.T) {
	s := &Service{feishuDataRoot: t.TempDir()}
	defer s.closeConfiguration()
	old := ConfigurationReceipt{SchemaVersion: 1, RequestID: "old-login", Action: "start_auth", FlowID: "auth-flow", Outcome: "pending", Stage: "submitted", ApplicationID: "same-app"}
	oldApplication := ConfigurationReceipt{SchemaVersion: 1, RequestID: "old-app", Action: "create_app", FlowID: "app-flow", Outcome: "pending", Stage: "submitted", ApplicationID: "same-app"}
	for _, receipt := range []ConfigurationReceipt{old, oldApplication} {
		if err := s.saveConfigurationReceipt(receipt); err != nil {
			t.Fatal(err)
		}
	}
	path := s.receiptPath(old.RequestID)
	if len(s.configurationFailures()) != 2 {
		t.Fatal("unresolved connection flows not blocked")
	}
	if err := s.scrubConfigurationReceiptsAfterLogout("logout"); err != nil {
		t.Fatal(err)
	}
	restarted := &Service{feishuDataRoot: s.feishuDataRoot}
	defer restarted.closeConfiguration()
	if len(restarted.configurationFailures()) != 2 {
		t.Fatal("cancelled connection results should remain visible as non-replayable failures")
	}
	result, err := restarted.ConfigurationResult(context.Background(), old.RequestID)
	if err != nil || result.Outcome != "failed" || !strings.Contains(result.Message, "不会重放") {
		t.Fatal(result, err)
	}
	var scrubbed ConfigurationReceipt
	if missing, err := privatestore.ReadJSON(path, &scrubbed); err != nil || missing || scrubbed.ApplicationID != "" || scrubbed.FlowID != "" || scrubbed.ContextRevision != "" || scrubbed.Digest != "" {
		t.Fatal("raw authorization identifiers survived logout")
	}
	result, err = restarted.ConfigurationResult(context.Background(), oldApplication.RequestID)
	if err != nil || result.Outcome != "failed" || !strings.Contains(result.Message, "不会重放") {
		t.Fatal(result, err)
	}
}

func TestLoginLogoutHistoryCannotDisableNextScan(t *testing.T) {
	s := &Service{feishuDataRoot: t.TempDir(), configuration: configurationReadyState()}
	defer s.closeConfiguration()
	for _, receipt := range []ConfigurationReceipt{
		{SchemaVersion: 1, RequestID: "create", Action: "create_app", Outcome: "pending", Stage: "submitted", UpdatedAt: "2026-09-10T10:30:45Z"},
		{SchemaVersion: 1, RequestID: "finish", Action: "finish_app", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-10T10:31:45Z"},
		{SchemaVersion: 1, RequestID: "logout", Action: "logout", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-10T10:32:48Z"},
	} {
		if err := privatestore.WriteJSON(s.receiptPath(receipt.RequestID), receipt); err != nil {
			t.Fatal(err)
		}
	}
	s.configuration.data.evidence = feishuprotocol.ConfigurationEvidence{SchemaVersion: 1, ContextRevision: "missing", ApplicationState: "missing", BotState: "missing", OperatorState: "missing", Auth: &feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "unauthorized"}}
	s.configuration.data.connection = normalizedFeishuSnapshot(domain.FeishuSnapshot{Availability: "notConfigured", ProcessRunning: true, ProcessState: "idle_unconfigured"})
	s.persistLegacyConfigurationFlowOutcomes()
	var migrated ConfigurationReceipt
	if missing, err := privatestore.ReadJSON(s.receiptPath("create"), &migrated); err != nil || missing || migrated.Outcome != "failed" || migrated.Stage != "verified" || migrated.Code != "cancelled_by_logout" {
		t.Fatalf("legacy create receipt was not durably migrated: %+v %v", migrated, err)
	}
	snapshot := s.ReadFeishuConfiguration(context.Background(), false)
	create, _ := configurationActionByID(snapshot, "create_app")
	if !create.Enabled || strings.Contains(create.Reason, "原请求") {
		t.Fatalf("verified logout history disabled a new scan: %+v", create)
	}
}

func TestBridgeRestartRetiresLostConfigurationFlowWithoutReplayingIt(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		action   string
		evidence feishuprotocol.ConfigurationEvidence
	}{
		{name: "application", action: "create_app", evidence: feishuprotocol.ConfigurationEvidence{ApplicationState: "missing", OperatorState: "missing"}},
		{name: "operator", action: "start_auth", evidence: feishuprotocol.ConfigurationEvidence{ApplicationState: "present", OperatorState: "missing"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := &Service{feishuDataRoot: t.TempDir()}
			receipt := ConfigurationReceipt{SchemaVersion: 1, RequestID: "interrupted", Action: scenario.action, FlowID: "lost-flow", Outcome: "pending", Stage: "submitted"}
			if err := s.saveConfigurationReceipt(receipt); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			s.retireInterruptedConfigurationFlowReceipts(configurationData{quickAt: now, evidenceAt: now, evidence: scenario.evidence})
			var retired ConfigurationReceipt
			if missing, err := privatestore.ReadJSON(s.receiptPath(receipt.RequestID), &retired); err != nil || missing {
				t.Fatal(err)
			}
			if retired.Outcome != "failed" || retired.Stage != "verified" || retired.Code != "configuration_flow_interrupted" || retired.FlowID != "" || !strings.Contains(retired.Message, "重新扫码") {
				t.Fatalf("interrupted flow was not made safely retryable: %+v", retired)
			}
		})
	}
}

func TestLiveOrUnverifiedConfigurationFlowIsNeverRetired(t *testing.T) {
	for _, scenario := range []struct {
		name string
		data configurationData
	}{
		{name: "live", data: configurationData{quickAt: time.Now(), evidenceAt: time.Now(), flow: &feishuprotocol.ConfigurationFlow{ID: "live", State: "pending"}, evidence: feishuprotocol.ConfigurationEvidence{ApplicationState: "missing"}}},
		{name: "failed flow read", data: configurationData{quickAt: time.Now(), evidenceAt: time.Now(), flowFailed: true, evidence: feishuprotocol.ConfigurationEvidence{ApplicationState: "missing"}}},
		{name: "unverified evidence", data: configurationData{quickAt: time.Now(), evidenceAt: time.Now(), evidenceFailed: true, evidence: feishuprotocol.ConfigurationEvidence{ApplicationState: "missing"}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := &Service{feishuDataRoot: t.TempDir()}
			receipt := ConfigurationReceipt{SchemaVersion: 1, RequestID: "preserved", Action: "create_app", FlowID: "flow", Outcome: "pending", Stage: "submitted"}
			if err := s.saveConfigurationReceipt(receipt); err != nil {
				t.Fatal(err)
			}
			s.retireInterruptedConfigurationFlowReceipts(scenario.data)
			var preserved ConfigurationReceipt
			if missing, err := privatestore.ReadJSON(s.receiptPath(receipt.RequestID), &preserved); err != nil || missing || preserved.Outcome != "pending" {
				t.Fatalf("unverified flow was retired: %+v %v", preserved, err)
			}
		})
	}
}

func TestCompletedFlowCheckSettlesOnlyItsOriginReceipt(t *testing.T) {
	s := &Service{feishuDataRoot: t.TempDir()}
	defer s.closeConfiguration()
	for _, receipt := range []ConfigurationReceipt{
		{SchemaVersion: 1, RequestID: "matching", Action: "create_app", FlowID: "flow-a", Outcome: "pending", Stage: "submitted"},
		{SchemaVersion: 1, RequestID: "other-flow", Action: "create_app", FlowID: "flow-b", Outcome: "pending", Stage: "submitted"},
		{SchemaVersion: 1, RequestID: "other-action", Action: "start_auth", FlowID: "flow-a", Outcome: "pending", Stage: "submitted"},
	} {
		if err := s.saveConfigurationReceipt(receipt); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.completeConfigurationFlowReceipt("create_app", "flow-a"); err != nil {
		t.Fatal(err)
	}
	for id, expected := range map[string]string{"matching": "completed", "other-flow": "pending", "other-action": "pending"} {
		var receipt ConfigurationReceipt
		if missing, err := privatestore.ReadJSON(s.receiptPath(id), &receipt); err != nil || missing || receipt.Outcome != expected {
			t.Fatalf("%s receipt was not settled precisely: %+v %v", id, receipt, err)
		}
	}
}

func TestLegacyRootWithoutFlowIDMigratesOnlyFromOneTimelyVerifiedFinish(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		finishes   []ConfigurationReceipt
		want       string
		wantFlowID string
	}{
		{name: "unique", finishes: []ConfigurationReceipt{{SchemaVersion: 1, RequestID: "finish", Action: "finish_app", FlowID: "flow-a", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-10T11:03:42Z"}}, want: "completed", wantFlowID: "flow-a"},
		{name: "ambiguous", finishes: []ConfigurationReceipt{{SchemaVersion: 1, RequestID: "finish-a", Action: "finish_app", FlowID: "flow-a", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-10T11:03:42Z"}, {SchemaVersion: 1, RequestID: "finish-b", Action: "finish_app", FlowID: "flow-b", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-10T11:04:42Z"}}, want: "pending"},
		{name: "expired", finishes: []ConfigurationReceipt{{SchemaVersion: 1, RequestID: "finish", Action: "finish_app", FlowID: "flow-a", Outcome: "completed", Stage: "verified", UpdatedAt: "2026-09-10T11:14:01Z"}}, want: "pending"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := &Service{feishuDataRoot: t.TempDir()}
			root := ConfigurationReceipt{SchemaVersion: 1, RequestID: "create", Action: "create_app", Outcome: "pending", Stage: "submitted", UpdatedAt: "2026-09-10T11:03:00Z"}
			if err := privatestore.WriteJSON(s.receiptPath(root.RequestID), root); err != nil {
				t.Fatal(err)
			}
			for _, finish := range scenario.finishes {
				if err := privatestore.WriteJSON(s.receiptPath(finish.RequestID), finish); err != nil {
					t.Fatal(err)
				}
			}
			s.persistLegacyConfigurationFlowOutcomes()
			var migrated ConfigurationReceipt
			if missing, err := privatestore.ReadJSON(s.receiptPath(root.RequestID), &migrated); err != nil || missing || migrated.Outcome != scenario.want || migrated.FlowID != scenario.wantFlowID {
				t.Fatalf("legacy finish migration was not conservative: %+v %v", migrated, err)
			}
		})
	}
}

func TestConnectionRecoveryClosesIncidentWithoutRewritingOriginalResult(t *testing.T) {
	for _, scenario := range []string{"healthy", "wrong_app", "stale", "failed_read", "disconnected", "before_request"} {
		t.Run(scenario, func(t *testing.T) {
			s := &Service{feishuDataRoot: t.TempDir()}
			r := ConfigurationReceipt{SchemaVersion: 1, RequestID: "restart-original", Action: "restart", ApplicationID: "app", Outcome: "unknown", Stage: "submitted", Code: "configuration_receipt_unverified", Message: "original"}
			if err := s.saveConfigurationReceipt(r); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(s.receiptPath(r.RequestID))
			if err != nil {
				t.Fatal(err)
			}
			state := configurationReadyState()
			state.data.evidence.ApplicationID = "app"
			state.data.evidence.OperatorState = "present"
			state.data.evidence.UserPermissions, state.data.evidence.ApplicationPermissions = "present", "present"
			switch scenario {
			case "wrong_app":
				state.data.evidence.ApplicationID = "other"
			case "stale":
				state.data.quickAt = time.Now().Add(-time.Minute)
			case "failed_read":
				state.data.quickFailed = true
			case "disconnected":
				state.data.connection.InboundConnection = false
			case "before_request":
				state.data.evidenceAt = time.Now().Add(-time.Minute)
			}
			snapshot := configurationTestSnapshot(&state)
			s.reconcileConnectionRecovery(state.data, snapshot)
			after, _ := os.ReadFile(s.receiptPath(r.RequestID))
			if scenario != "healthy" {
				if string(after) != string(original) {
					t.Fatal("invalid evidence resolved original request")
				}
				return
			}
			receipts := s.loadConfigurationReceipts()
			if len(receipts) != 1 || receipts[0].RecoveryVerifiedAt == "" || receipts[0].Outcome != "unknown" || receipts[0].Code != r.Code || receipts[0].Message != r.Message {
				t.Fatal("original audit lost", receipts)
			}
			s.reconcileConnectionRecovery(state.data, snapshot)
			repeated, _ := os.ReadFile(s.receiptPath(r.RequestID))
			if string(after) != string(repeated) {
				t.Fatal("repeated observation rewrote receipt")
			}
			restarted := &Service{feishuDataRoot: s.feishuDataRoot}
			projected := restarted.configurationFailures()
			if len(projected) != 1 || projected[0].Outcome != "resolved" {
				t.Fatal("recovery did not survive restart")
			}
		})
	}
}
