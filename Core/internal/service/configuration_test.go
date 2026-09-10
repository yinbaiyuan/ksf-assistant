package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/domain"
	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/feishuprotocol"
)

func configurationReadyState() configurationRuntime {
	now := time.Now()
	settings := managedfeishu.DefaultSettings()
	settings.Outbound = managedfeishu.DryRunSwitch{Enabled: true}
	return configurationRuntime{data: configurationData{
		setup: managedfeishu.SetupState{Version: 1, Stage: "not_started"}, settings: &settings, quickAt: now, evidenceAt: now,
		connection: domain.FeishuSnapshot{InboundConnection: true, ProcessRunning: true, Availability: "ready", TargetAliases: []string{"fixture"}, Capabilities: map[string]domain.CapabilityHealth{"desktopIPC": {State: "ready"}}},
		evidence:   feishuprotocol.ConfigurationEvidence{SchemaVersion: 1, ContextRevision: "fixture", ApplicationState: "present", BotState: "present", OperatorState: "missing", UserPermissions: "unknown", ApplicationPermissions: "unknown", Auth: &feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "authorized", Identity: "user", Profile: "default", IdentityValid: true, ProfileValid: true}},
	}}
}

func configurationTestSnapshot(state *configurationRuntime) ConfigurationSnapshot {
	data := state.data
	state.initialize()
	state.data = data
	return state.snapshot(time.Now())
}

func configurationFact(snapshot ConfigurationSnapshot, id string) ConfigurationFact {
	for _, fact := range snapshot.Facts {
		if fact.ID == id {
			return fact
		}
	}
	return ConfigurationFact{}
}

func TestConfigurationLegacyStageCannotOverrideFacts(t *testing.T) {
	for _, stage := range []string{"not_started", "platform_pending", "failed", "app_pending", "ready"} {
		state := configurationReadyState()
		state.data.setup.Stage = stage
		snapshot := configurationTestSnapshot(&state)
		if snapshot.Summary.State != "connected" || configurationFact(snapshot, "application").State != "present" {
			t.Fatalf("legacy stage %s replaced facts: %+v", stage, snapshot)
		}
		if action, _ := configurationActionByID(snapshot, "create_app"); action.Enabled {
			t.Fatal("existing app offered overwrite")
		}
		if configurationFact(snapshot, "outbound").ID != "" {
			t.Fatal("legacy setup downgraded live mode")
		}
	}
}

func TestConfigurationUserBotOperatorAndPermissionAreIndependent(t *testing.T) {
	state := configurationReadyState()
	state.data.evidence.Auth.Status = "unauthorized"
	state.data.evidence.Auth.IdentityValid = false
	snapshot := configurationTestSnapshot(&state)
	if snapshot.Summary.State != "connected" || configurationFact(snapshot, "user").State != "missing" || configurationFact(snapshot, "bot").State != "present" || configurationFact(snapshot, "permissions").State != "unknown" {
		t.Fatalf("identities conflated: %+v", snapshot)
	}
	if action, _ := configurationActionByID(snapshot, "test_message"); action.Enabled {
		t.Fatal("unverified self offered test send")
	}
	if action, _ := configurationActionByID(snapshot, "bind_operator"); action.Enabled {
		t.Fatal("unverified user offered operator binding")
	}
}

func TestConfigurationMissingApplicationOffersCleanupBeforeReconnectWhenOldBindingsRemain(t *testing.T) {
	state := configurationReadyState()
	state.data.connection = normalizedFeishuSnapshot(domain.FeishuSnapshot{
		Availability: "notConfigured", ProcessRunning: true, ProcessState: "idle_unconfigured",
		Capabilities: map[string]domain.CapabilityHealth{"desktopIPC": {State: "ready"}},
	})
	state.data.evidence = feishuprotocol.ConfigurationEvidence{
		SchemaVersion: 1, ContextRevision: "missing", ApplicationState: "missing", BotState: "unknown",
		OperatorState: "unknown", UserPermissions: "unknown", ApplicationPermissions: "unknown", CreationBlocked: true,
	}
	snapshot := configurationTestSnapshot(&state)
	if snapshot.Summary.State != "cleanup_required" {
		t.Fatalf("residual connection was not explained: %+v", snapshot.Summary)
	}
	create, _ := configurationActionByID(snapshot, "create_app")
	if create.Enabled || !strings.Contains(create.Reason, "活动飞书连接") {
		t.Fatalf("unsafe reconnect was offered: %+v", create)
	}
	cleanup, _ := configurationActionByID(snapshot, "logout")
	if !cleanup.Enabled || cleanup.Title != "清理旧连接数据" {
		t.Fatalf("residual connection has no recovery action: %+v", cleanup)
	}
}

func TestConfigurationExposesOnlyCoreGeneratedAuthorizationRequestID(t *testing.T) {
	state := configurationReadyState()
	state.data.evidence.OperatorState = "present"
	state.data.evidence.AuthorizationRequest = &feishuprotocol.AuthorizationRequest{ID: "0123456789abcdef0123456789abcdef", Purpose: "docs.fixture.read", Scopes: []string{"docx:document:readonly"}}
	snapshot := configurationTestSnapshot(&state)
	action, ok := configurationActionByID(snapshot, "start_auth")
	if !ok || !action.Enabled || action.AuthorizationRequestID != state.data.evidence.AuthorizationRequest.ID || len(action.Scopes) != 1 || !strings.Contains(action.Confirmation, "不会自动重放") {
		t.Fatalf("progressive authorization affordance invalid: %#v", action)
	}
	request := configurationRequest(snapshot, "start_auth")
	request.AuthorizationRequestID = "desktop-invented"
	if request.AuthorizationRequestID == action.AuthorizationRequestID {
		t.Fatal("invalid fixture")
	}
}

func TestConfigurationWaitsForApplicationScopeBeforeProgressiveOAuth(t *testing.T) {
	state := configurationReadyState()
	state.data.evidence.OperatorState = "present"
	state.data.evidence.AuthorizationRequest = &feishuprotocol.AuthorizationRequest{ID: "0123456789abcdef0123456789abcdef", Purpose: "docs.fixture.read", Scopes: []string{"docx:document:readonly"}}
	state.data.evidence.MissingApplicationScopes = []string{"docx:document:readonly"}
	snapshot := configurationTestSnapshot(&state)
	action, ok := configurationActionByID(snapshot, "start_auth")
	if !ok || action.Enabled || !strings.Contains(action.Reason, "开放平台") {
		t.Fatalf("progressive OAuth was offered before the application scope existed: %#v", action)
	}
	if len(snapshot.Issues) == 0 || snapshot.Issues[len(snapshot.Issues)-1].Code != "application_permissions_missing" {
		t.Fatalf("missing application scope was not explained: %#v", snapshot.Issues)
	}
}

func TestConfigurationFailedRefreshPreservesPreviousFactsButDisablesWrites(t *testing.T) {
	state := configurationReadyState()
	state.data.evidenceFailed = true
	snapshot := configurationTestSnapshot(&state)
	user := configurationFact(snapshot, "user")
	if user.State != "present" || !user.Stale || user.Value != "已授权" || snapshot.Summary.State != "unknown" {
		t.Fatalf("lost last observation: %+v", snapshot)
	}
	for _, id := range []string{"connect_app", "create_app", "bind_operator", "test_message", "logout", "set_feature"} {
		if action, _ := configurationActionByID(snapshot, id); action.Enabled {
			t.Fatalf("stale action admitted: %s", id)
		}
	}
}

func TestConfigurationRevisionIgnoresReadClockButTracksMeaning(t *testing.T) {
	state := configurationReadyState()
	first := configurationTestSnapshot(&state)
	state.data.quickAt = time.Now()
	state.data.evidenceAt = time.Now()
	second := state.snapshot(time.Now().Add(time.Second))
	if first.Revision != second.Revision {
		t.Fatalf("timestamps invalidate gestures: %d -> %d", first.Revision, second.Revision)
	}
	state.data.evidence.OperatorState = "present"
	third := state.snapshot(time.Now())
	if third.Revision <= second.Revision {
		t.Fatal("changed fact retained revision")
	}
}

func TestConfigurationFlowDoesNotHideExistingAuthorization(t *testing.T) {
	state := configurationReadyState()
	state.data.flow = &feishuprotocol.ConfigurationFlow{ID: "fixture", Kind: "user", State: "pending", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano), UserCode: "fixture"}
	snapshot := configurationTestSnapshot(&state)
	if configurationFact(snapshot, "user").State != "present" || snapshot.Summary.State != "connected" {
		t.Fatal("flow replaced working config")
	}
	if action, _ := configurationActionByID(snapshot, "start_auth"); action.Enabled {
		t.Fatal("duplicate OAuth admitted")
	}
	if action, _ := configurationActionByID(snapshot, "cancel_flow"); !action.Enabled {
		t.Fatal("flow cannot cancel")
	}
	state.data.flow.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	expired := state.snapshot(time.Now())
	if expired.Flow.State != "expired" || expired.Flow.UserCode != "" {
		t.Fatal("expired QR exposed")
	}
}

func TestConfigurationReadReturnsLoadingImmediatelyAndCloses(t *testing.T) {
	service := &Service{}
	start := time.Now()
	snapshot := service.ReadFeishuConfiguration(context.Background(), false)
	if time.Since(start) > time.Second || snapshot.Summary.State != "checking" || snapshot.Setup.Stage != "unknown" {
		t.Fatalf("not loading: %+v", snapshot)
	}
	service.closeConfiguration()
	if snapshot := service.ReadFeishuConfiguration(context.Background(), true); snapshot.Summary.State != "unavailable" {
		t.Fatal("closed service looks connected")
	}
}

func TestConfigurationRejectsUnconfirmedStaleAndUnexpectedActions(t *testing.T) {
	service := &Service{feishuDataRoot: t.TempDir(), configuration: configurationReadyState()}
	snapshot := configurationTestSnapshot(&service.configuration)
	request := ConfigurationActionRequest{Action: "test_message", RequestID: "fixture", Epoch: snapshot.Epoch, Revision: snapshot.Revision, ContextRevision: snapshot.ContextRevision, TargetAlias: "fixture"}
	if result, err := service.ApplyFeishuConfiguration(context.Background(), request); err != nil || result.Outcome != "failed" {
		t.Fatal("unconfirmed write accepted")
	}
	request.Confirm = true
	request.ContextRevision = "stale-context"
	result, err := service.ApplyFeishuConfiguration(context.Background(), request)
	if err != nil || result.Outcome != "failed" {
		t.Fatalf("stale action: %+v %v", result, err)
	}
	request.ContextRevision = snapshot.ContextRevision
	request.AppSecret = "fixture-secret"
	if result, err := service.ApplyFeishuConfiguration(context.Background(), request); err != nil || result.Outcome != "failed" || strings.Contains(result.Message, "fixture-secret") {
		t.Fatal("unexpected secret accepted or leaked")
	}
	if len(service.configuration.requests) != 0 {
		t.Fatal("rejected action started")
	}
}

func TestConfigurationConsumedRequestsAreNotReplayed(t *testing.T) {
	service := &Service{feishuDataRoot: t.TempDir(), configuration: configurationReadyState()}
	snapshot := configurationTestSnapshot(&service.configuration)
	request := ConfigurationActionRequest{Action: "test_message", RequestID: "fixture", Epoch: snapshot.Epoch, Revision: snapshot.Revision, ContextRevision: snapshot.ContextRevision, TargetAlias: "fixture", Confirm: true}
	service.configuration.requests[request.RequestID] = configurationRequestRecord{digest: configurationHash(request), outcome: "unknown", message: "fixture-unknown"}
	result, err := service.ApplyFeishuConfiguration(context.Background(), request)
	if err != nil || result.Outcome != "unknown" || result.Message != "fixture-unknown" {
		t.Fatalf("replayed or lost unknown result: %+v %v", result, err)
	}
	request.TargetAlias = "changed"
	if result, err := service.ApplyFeishuConfiguration(context.Background(), request); err != nil || result.Outcome != "failed" {
		t.Fatal("same request changed target")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "digest") {
		t.Fatal("request internals leaked")
	}
}

func awaitConfiguration(t *testing.T, service *Service) ConfigurationSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := service.ReadFeishuConfiguration(context.Background(), false)
		if !snapshot.Refreshing && snapshot.ContextRevision != "" {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("configuration refresh did not complete")
	return ConfigurationSnapshot{}
}

func configurationRequest(snapshot ConfigurationSnapshot, action string) ConfigurationActionRequest {
	return ConfigurationActionRequest{Action: action, RequestID: "fixture-" + action, Epoch: snapshot.Epoch, Revision: snapshot.Revision, ContextRevision: snapshot.ContextRevision, Confirm: true}
}

func TestConfigurationActualReadChainHasZeroConfigurationMutations(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	t.Cleanup(service.closeConfiguration)
	before := appSetupTrace(t, service)
	service.ReadFeishuConfiguration(context.Background(), true)
	snapshot := awaitConfiguration(t, service)
	if snapshot.Summary.State != "connected" || configurationFact(snapshot, "operator").State != "missing" {
		t.Fatalf("wrong aggregated state: %+v", snapshot)
	}
	assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
	initialRevision := snapshot.Revision
	service.ReadFeishuConfiguration(context.Background(), true)
	snapshot = awaitConfiguration(t, service)
	if snapshot.Revision != initialRevision {
		t.Fatalf("refresh timestamps invalidated context %d -> %d", initialRevision, snapshot.Revision)
	}
}

func TestConfigurationRoutinePollingDoesNotLeaveVisibleRefreshPending(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	t.Cleanup(service.closeConfiguration)
	awaitConfiguration(t, service)
	for attempt := 0; attempt < 3; attempt++ {
		service.configuration.mu.Lock()
		service.configuration.lastQuick = time.Time{}
		service.configuration.mu.Unlock()
		snapshot := service.ReadFeishuConfiguration(context.Background(), false)
		if snapshot.Refreshing || snapshot.Summary.State != "connected" {
			t.Fatalf("routine poll replaced settled presentation: %+v", snapshot)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			service.configuration.mu.Lock()
			active := service.configuration.refreshing
			service.configuration.mu.Unlock()
			if !active {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("routine observation did not complete")
			}
			time.Sleep(time.Millisecond)
		}
	}
	if snapshot := service.ReadFeishuConfiguration(context.Background(), true); !snapshot.Refreshing {
		t.Fatal("explicit slow check did not show progress")
	}
	if snapshot := awaitConfiguration(t, service); snapshot.Refreshing {
		t.Fatal("completed slow check retained progress")
	}
}

func TestConfigurationBindingActionIsRetired(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	t.Cleanup(service.closeConfiguration)
	snapshot := awaitConfiguration(t, service)
	request := configurationRequest(snapshot, "bind_operator")
	before := appSetupTrace(t, service)
	result, err := service.ApplyFeishuConfiguration(context.Background(), request)
	if err != nil || result.Outcome != "failed" || !strings.Contains(result.Message, "configuration_action_retired") {
		t.Fatalf("retired binding action executed: %+v %v", result, err)
	}
	trace := appSetupTrace(t, service)[len(before):]
	for _, forbidden := range []string{feishuprotocol.MethodAuthEnsureUser, feishuprotocol.MethodAuthStart, feishuprotocol.SettingsWrite, feishuprotocol.MethodMessageTest} {
		if strings.Contains(trace, forbidden) {
			t.Fatalf("binding also did %s", forbidden)
		}
	}
}

func TestConfigurationExternalSettingsChangeStopsGestureBeforeWrite(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	t.Cleanup(service.closeConfiguration)
	snapshot := awaitConfiguration(t, service)
	request := configurationRequest(snapshot, "bind_operator")
	settings := managedfeishu.DefaultSettings()
	settings.Group.Enabled = true
	if err := managedfeishu.NewSettingsStore(service.feishuDataRoot).Save(settings); err != nil {
		t.Fatal(err)
	}
	before := appSetupTrace(t, service)
	result, err := service.ApplyFeishuConfiguration(context.Background(), request)
	if err != nil || result.Outcome != "failed" {
		t.Fatalf("external change overwritten: %+v %v", result, err)
	}
	assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
}

func TestConfigurationUnknownMessageIsNotReplayed(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	t.Cleanup(service.closeConfiguration)
	settings := managedfeishu.DefaultSettings()
	settings.Outbound = managedfeishu.DryRunSwitch{Enabled: true}
	if err := managedfeishu.NewSettingsStore(service.feishuDataRoot).Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service.feishuDataRoot, "fixture-test-unknown"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"operatorState": "present", "operatorAlias": "fixture"})
	snapshot := awaitConfiguration(t, service)
	request := configurationRequest(snapshot, "test_message")
	request.TargetAlias = "fixture"
	before := appSetupTrace(t, service)
	result, err := service.ApplyFeishuConfiguration(context.Background(), request)
	if err != nil || result.Outcome != "unknown" {
		t.Fatalf("unknown send lost: %+v %v", result, err)
	}
	if _, err := service.ApplyFeishuConfiguration(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	trace := appSetupTrace(t, service)[len(before):]
	if strings.Count(trace, feishuprotocol.MethodMessageTest+" ") != 1 {
		t.Fatalf("unknown send replayed: %s", trace)
	}
	if strings.Contains(trace, feishuprotocol.SettingsWrite) {
		t.Fatal("test message changed mode")
	}
}

func TestConfigurationGatewayDoesNotExposeDesktopActions(t *testing.T) {
	service := &Service{}
	for _, method := range []string{"feishu/configuration/read", "feishu/configuration/action", "feishu/auth/ensureCurrentUser"} {
		if _, err := service.handleLocalRPC(context.Background(), method, json.RawMessage(`{"confirm":true}`)); err == nil {
			t.Fatalf("CLI accessed host control: %s", method)
		}
	}
	params, err := json.Marshal(feishucli.Request{Command: "auth", Action: "ensure-current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.handleLocalRPC(context.Background(), feishucli.MethodExecute, params); err == nil || !strings.Contains(err.Error(), "configuration_desktop_required") {
		t.Fatalf("compat auth bypass: %v", err)
	}
}

func TestConfigurationOldRefreshCannotOverwriteNewGeneration(t *testing.T) {
	service := &Service{feishuDataRoot: t.TempDir(), configuration: configurationReadyState()}
	snapshot := configurationTestSnapshot(&service.configuration)
	service.configuration.mutation = 2
	service.configuration.refreshing = true
	service.refreshConfiguration(context.Background(), func() {}, 1, true)
	if service.configuration.data.evidence.ContextRevision != "fixture" || !service.configuration.refreshing {
		t.Fatal("old read overwrote current operation")
	}
	if current := service.configuration.snapshot(time.Now()); current.Revision != snapshot.Revision {
		t.Fatal("old read changed semantic revision")
	}
}

func TestConfigurationDesktopWireFixture(t *testing.T) {
	state := configurationReadyState()
	expectedState := "connected"
	if os.Getenv("KSF_CONFIGURATION_FIXTURE_STATE") == "not_configured" {
		expectedState = "not_configured"
		settings := managedfeishu.DefaultSettings()
		state.data.settings = &settings
		state.data.evidence = feishuprotocol.ConfigurationEvidence{SchemaVersion: 1, ContextRevision: "missing", ApplicationState: "missing", BotState: "unknown", UserPermissions: "unknown", ApplicationPermissions: "unknown", OperatorState: "unknown"}
		state.data.connection = normalizedFeishuSnapshot(domain.FeishuSnapshot{Availability: "notConfigured", ProcessRunning: true, ProcessState: "idle_unconfigured"})
	}
	snapshot := configurationTestSnapshot(&state)
	snapshot.Epoch, snapshot.ContextRevision = "fixture-epoch", "fixture-context"
	snapshot.Revision = 7
	snapshot.ObservedAt = "2026-09-06T10:00:00Z"
	for index := range snapshot.Facts {
		snapshot.Facts[index].CheckedAt = snapshot.ObservedAt
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip ConfigurationSnapshot
	if err := json.Unmarshal(data, &roundTrip); err != nil || roundTrip.Summary.State != expectedState {
		t.Fatalf("invalid wire contract: %v", err)
	}
	if destination := os.Getenv("KSF_CONFIGURATION_FIXTURE_OUTPUT"); destination != "" {
		if !filepath.IsAbs(destination) || filepath.Base(destination) != "ksfas-configuration-fixture.json" {
			t.Fatal("unsafe fixture destination")
		}
		if err := os.WriteFile(destination, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigurationCoordinatorCarriesSettingsCASAndClassifiesConflict(t *testing.T) {
	for _, action := range []string{"set_feature", "enable_outbound"} {
		service, _ := newAppSetupFixture(t)
		t.Cleanup(service.closeConfiguration)
		snapshot := awaitConfiguration(t, service)
		before := appSetupTrace(t, service)
		result, err := service.ApplyFeishuConfiguration(context.Background(), configurationRequest(snapshot, action))
		if err != nil || result.Outcome != "failed" || !strings.Contains(result.Message, "configuration_action_retired") {
			t.Fatalf("retired action: %v %s", err, result.Message)
		}
		assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
	}
}

func TestConfigurationExistingAppActionIsRetired(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	t.Cleanup(service.closeConfiguration)
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"applicationState": "missing", "contextRevision": "missing", "auth": nil, "botState": "unknown"})
	snapshot := awaitConfiguration(t, service)
	request := configurationRequest(snapshot, "connect_app")
	request.AppID, request.AppSecret = "cli_fixture", "fixture-only-secret"
	before := appSetupTrace(t, service)
	result, err := service.ApplyFeishuConfiguration(context.Background(), request)
	if err != nil || result.Outcome != "failed" || !strings.Contains(result.Message, "configuration_action_retired") {
		t.Fatalf("retired existing-app action executed: %+v %v", result, err)
	}
	trace := appSetupTrace(t, service)[len(before):]
	if strings.Contains(trace, feishuprotocol.Initialize+" ") || strings.Contains(trace, feishuprotocol.MethodAuthConfigure+" ") || strings.Contains(trace, feishuprotocol.MethodAuthStart+" ") || strings.Contains(trace, feishuprotocol.MethodAuthEnsureUser+" ") || strings.Contains(trace, feishuprotocol.MethodMessageTest+" ") {
		t.Fatalf("connect side effects incorrect: %s", trace)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), request.AppSecret) {
		t.Fatal("application secret leaked")
	}
}

func TestConfigurationInvalidationIsNotAnAuthorizationFailure(t *testing.T) {
	state := configurationReadyState()
	state.data.invalidated = true
	snapshot := configurationTestSnapshot(&state)
	if snapshot.Summary.State != "checking" || !configurationFact(snapshot, "user").Stale {
		t.Fatalf("invalidation lost facts: %+v", snapshot)
	}
	for _, issue := range snapshot.Issues {
		if issue.Code == "authorization_check_failed" {
			t.Fatal("normal mutation falsely reported failed authorization")
		}
	}
}

func TestCompletedOAuthQuickPollRefreshesIdentityOnceWithoutManualRefresh(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	t.Cleanup(service.closeConfiguration)
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"auth": unauthorizedSetupFixture(), "flow": feishuprotocol.ConfigurationFlow{ID: "oauth-completion", Kind: "user", State: "pending"}})
	first := awaitConfiguration(t, service)
	if first.Auth == nil || first.Auth.IdentityValid {
		t.Fatal("fixture should start signed out")
	}
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"flow": feishuprotocol.ConfigurationFlow{ID: "oauth-completion", Kind: "user", State: "completed"}})
	poll := func() ConfigurationSnapshot {
		service.configuration.mu.Lock()
		service.configuration.lastQuick = time.Time{}
		service.configuration.lastSlow = time.Now() // Slow calibration is not due.
		service.configuration.mu.Unlock()
		service.ReadFeishuConfiguration(context.Background(), false)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			service.configuration.mu.Lock()
			busy := service.configuration.refreshing
			service.configuration.mu.Unlock()
			if !busy {
				return service.ReadFeishuConfiguration(context.Background(), false)
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("poll did not settle")
		return ConfigurationSnapshot{}
	}
	before := appSetupTrace(t, service)
	completed := poll()
	if completed.Auth == nil || !completed.Auth.IdentityValid || completed.Flow != nil {
		t.Fatal("quick OAuth completion retained stale signed-out state")
	}
	trace := appSetupTrace(t, service)[len(before):]
	if strings.Count(trace, feishuprotocol.MethodConfigurationEvidence+" ") != 1 {
		t.Fatalf("expected one authoritative read on completion: %s", trace)
	}
	assertNoSetupMutations(t, trace)
	before = appSetupTrace(t, service)
	poll()
	trace = appSetupTrace(t, service)[len(before):]
	if strings.Contains(trace, feishuprotocol.MethodConfigurationEvidence+" ") {
		t.Fatal("completed flow caused repeated full reads")
	}
}
