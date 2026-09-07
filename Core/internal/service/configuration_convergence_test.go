package service

import (
	"context"
	"ksfassistant/core/internal/feishuprotocol"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigurationPermissionCompletenessAndIdentityAreIndependent(t *testing.T) {
	for _, test := range []struct {
		user, app, want string
		auth            bool
	}{{"present", "present", "已登录", false}, {"missing", "present", "授权未完成", true}, {"present", "missing", "授权未完成", false}, {"unknown", "present", "权限待核验", false}} {
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
		if !strings.Contains(configurationFact(snapshot, "authorizedUser").Value, test.want) || !snapshot.Auth.IdentityValid {
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

func TestSignedOutPresentationOffersLoginWithoutConnectionWarning(t *testing.T) {
	state := configurationReadyState()
	state.data.evidence.Auth.Status = "unauthorized"
	state.data.evidence.Auth.IdentityValid = false
	state.data.evidence.ApplicationPermissions = "present"
	state.data.evidence.UserPermissions = "unknown"
	snapshot := configurationTestSnapshot(&state)
	action, _ := configurationActionByID(snapshot, "start_auth")
	if configurationFact(snapshot, "robot").Value != "登录后显示" {
		t.Fatal("signed-out screen exposed prior bot name")
	}
	if !action.Enabled || action.Title != "登录飞书" || configurationFact(snapshot, "taskConnection").Value != "等待飞书登录" {
		t.Fatal("logout failed to return to login", action)
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

func TestVerifiedLogoutEndsOnlyEarlierSameApplicationAuthorizationWait(t *testing.T) {
	older := ConfigurationReceipt{Action: "start_auth", Outcome: "pending", Stage: "submitted", ApplicationID: "app-a", UpdatedAt: "2026-09-07T16:25:00Z"}
	later := ConfigurationReceipt{Action: "logout", Outcome: "completed", Stage: "verified", ApplicationID: "app-a", UpdatedAt: "2026-09-07T16:26:00Z"}
	if !authorizationEndedByLogout(older, []ConfigurationReceipt{later}) {
		t.Fatal("old login still blocks next login")
	}
	for _, change := range []func(*ConfigurationReceipt){func(r *ConfigurationReceipt) { r.ApplicationID = "app-b" }, func(r *ConfigurationReceipt) { r.Outcome = "unknown" }, func(r *ConfigurationReceipt) { r.Stage = "submitted" }, func(r *ConfigurationReceipt) { r.UpdatedAt = "2026-09-07T16:24:00Z" }} {
		other := later
		change(&other)
		if authorizationEndedByLogout(older, []ConfigurationReceipt{other}) {
			t.Fatal("unverified/unrelated logout cleared wait")
		}
	}
	older.Action = "test_message"
	if authorizationEndedByLogout(older, []ConfigurationReceipt{later}) {
		t.Fatal("logout resolved unknown message write")
	}
}

func TestRestartedReceiptProjectionAllowsLoginAfterVerifiedLogoutWithoutChangingAudit(t *testing.T) {
	s := &Service{feishuDataRoot: t.TempDir()}
	defer s.closeConfiguration()
	old := ConfigurationReceipt{SchemaVersion: 1, RequestID: "old-login", Action: "start_auth", Outcome: "pending", Stage: "submitted", ApplicationID: "same-app"}
	if err := s.saveConfigurationReceipt(old); err != nil {
		t.Fatal(err)
	}
	path := s.receiptPath(old.RequestID)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.configurationFailures()) != 1 {
		t.Fatal("unresolved login not blocked")
	}
	if err := s.saveConfigurationReceipt(ConfigurationReceipt{SchemaVersion: 1, RequestID: "logout", Action: "logout", Outcome: "completed", Stage: "verified", ApplicationID: "same-app"}); err != nil {
		t.Fatal(err)
	}
	restarted := &Service{feishuDataRoot: s.feishuDataRoot}
	defer restarted.closeConfiguration()
	if len(restarted.configurationFailures()) != 0 {
		t.Fatal("verified logout still blocks login after restart")
	}
	result, err := restarted.ConfigurationResult(context.Background(), old.RequestID)
	if err != nil || result.Outcome != "failed" || !strings.Contains(result.Message, "注销结束") {
		t.Fatal(result, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("original audit was rewritten")
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
