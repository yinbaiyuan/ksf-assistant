package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/privateipc"
	"ksfassistant/core/internal/privatestore"
)

func newAppSetupFixture(t *testing.T, executables ...string) (*Service, <-chan bridgeFixtureConnection) {
	t.Helper()
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if len(executables) != 0 {
		executable = executables[0]
	}
	service := &Service{home: root, feishuDataRoot: root, hostContextStore: integration.NewHostContextStore(root)}
	connected := make(chan bridgeFixtureConnection, 16)
	service.managedFeishuSupervisor = managedfeishu.NewSupervisor(managedfeishu.SupervisorOptions{
		Executable: executable, DataRoot: root, Arguments: []string{"-test.run=^TestAppSetupBridgeProcess$"},
		Environment: []string{"KSF_APP_SETUP_FIXTURE=1", "KSF_APP_SETUP_ROOT=" + root}, Handler: service,
		OnConnect: func(ctx context.Context, generation uint64) error {
			err := service.connectManagedBridge(ctx, generation)
			connected <- bridgeFixtureConnection{ctx: ctx, generation: generation, err: err}
			return err
		},
	})
	t.Cleanup(func() { _ = service.managedFeishuSupervisor.Stop(context.Background()) })
	if err := service.managedFeishuSupervisor.Start(); err != nil {
		t.Fatal(err)
	}
	if connection := fixtureConnection(t, connected); connection.err != nil {
		t.Fatal(connection.err)
	}
	return service, connected
}

func TestAppSetupBridgeProcess(t *testing.T) {
	if os.Getenv("KSF_APP_SETUP_FIXTURE") != "1" {
		return
	}
	root := os.Getenv("KSF_APP_SETUP_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("fixture requires an absolute temporary data root")
	}
	var mutex sync.Mutex
	var revision uint64
	active := false
	activeKind := "app"
	outcome, flow := "pending", "app-create"
	startError := false
	registrationBound := func() bool {
		_, err := os.Stat(filepath.Join(root, "fixture-registration-bound"))
		return err == nil
	}
	markRegistrationBound := func() error {
		return os.WriteFile(filepath.Join(root, "fixture-registration-bound"), nil, 0o600)
	}
	readEvidence := func() feishuprotocol.ConfigurationEvidence {
		evidence := feishuprotocol.ConfigurationEvidence{
			SchemaVersion: 1, ContextRevision: "fixture", IdentityRevision: "fixture-identity", ApplicationID: "cli_fixture", ApplicationState: "present", BotState: "present", BotPermissions: "unknown",
			ApplicationPermissions: "present", UserPermissions: "present", OperatorState: "missing", CheckedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Auth: &feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "authorized", Identity: "user", Profile: "default", ProfileValid: true, IdentityValid: true, MissingCapabilities: []string{}},
		}
		if data, err := os.ReadFile(filepath.Join(root, "fixture-evidence.json")); err == nil {
			_ = json.Unmarshal(data, &evidence)
		}
		if registrationBound() {
			evidence.OperatorState, evidence.OperatorAlias = "present", "我"
			evidence.Auth = &feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "unauthorized", Identity: "user", Profile: "default", ProfileValid: true, MissingCapabilities: []string{}}
			evidence.UserPermissions = "unknown"
		}
		if active {
			evidence.Flow = &feishuprotocol.ConfigurationFlow{ID: "fixture-flow", Kind: activeKind, State: outcome}
		}
		return evidence
	}
	peer := privateipc.NewPeer(os.Stdin, os.Stdout, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		mutex.Lock()
		defer mutex.Unlock()
		trace, err := os.OpenFile(filepath.Join(root, "calls.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = trace.WriteString(method + " " + string(params) + "\n")
		_ = trace.Close()
		if err != nil {
			return nil, err
		}
		switch method {
		case feishuprotocol.Initialize:
			return feishuprotocol.InitializeResult{Protocol: feishuprotocol.Protocol, Version: "fixture"}, nil
		case feishuprotocol.SnapshotRead:
			revision++
			settings, err := managedfeishu.NewSettingsStore(root).Load()
			if err != nil {
				return nil, err
			}
			availability := "unavailable"
			if settings.Outbound.Enabled {
				availability = "ready"
				if settings.Outbound.DryRun {
					availability = "dryRun"
				}
			}
			if _, err := os.Stat(filepath.Join(root, "fixture-not-ready")); err == nil {
				availability = "degraded"
			}
			return feishuprotocol.Snapshot{Revision: revision, Availability: availability, RuntimeKind: "fixture", ProcessState: "running", ProcessRunning: true, Configured: true, InboundConnection: true, TargetAliases: []string{"fixture"}, Capabilities: map[string]feishuprotocol.CapabilityHealth{"feishuOutbound": {State: "ready"}}}, nil
		case feishuprotocol.SettingsRead:
			return managedfeishu.NewSettingsStore(root).Load()
		case feishuprotocol.SettingsWrite, feishuprotocol.MethodSettingsCompareAndSwap:
			var settings managedfeishu.Settings
			var expected *managedfeishu.Settings
			if method == feishuprotocol.MethodSettingsCompareAndSwap {
				var request struct {
					Expected *managedfeishu.Settings `json:"expected"`
					Settings *managedfeishu.Settings `json:"settings"`
				}
				if err := privateipc.DecodeStrict(params, &request, true); err != nil {
					return nil, err
				}
				if request.Expected == nil || request.Settings == nil {
					return nil, privateipc.NewError(-32602, "missing CAS settings")
				}
				expected, settings = request.Expected, *request.Settings
			} else if err := privateipc.DecodeStrict(params, &settings, true); err != nil {
				return nil, err
			}
			store := managedfeishu.NewSettingsStore(root)
			if expected != nil {
				if _, markerErr := os.Stat(filepath.Join(root, "fixture-cas-conflict")); markerErr == nil {
					current, loadErr := store.Load()
					if loadErr != nil {
						return nil, loadErr
					}
					current.Codex.DefaultThreadTitle = "external-fixture"
					if err := store.Save(current); err != nil {
						return nil, err
					}
				}
				err = store.CompareAndSwap(*expected, settings)
			} else {
				err = store.Save(settings)
			}
			if errors.Is(err, managedfeishu.ErrSettingsConflict) {
				return nil, privateipc.NewError(-32064, "feishu_settings_changed")
			}
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(filepath.Join(root, "fixture-write-unknown")); err == nil {
				return nil, errors.New("fixture_write_ack_lost")
			}
			return settings, nil
		case feishuprotocol.MethodConfigurationEvidence:
			return readEvidence(), nil
		case "fixture/configuration-evidence":
			var evidence feishuprotocol.ConfigurationEvidence
			if err := privateipc.DecodeStrict(params, &evidence, true); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(root, "fixture-evidence.json"), params, 0o600); err != nil {
				return nil, err
			}
			return map[string]bool{"accepted": true}, nil
		case feishuprotocol.MethodConfigurationFlow:
			return readEvidence().Flow, nil
		case feishuprotocol.MethodConfigurationCancel:
			var request feishuprotocol.ConfigurationCancelRequest
			if err := privateipc.DecodeStrict(params, &request, true); err != nil {
				return nil, err
			}
			if !active || request.FlowID != "fixture-flow" || request.Kind != activeKind {
				return nil, errors.New("fixture_session_changed")
			}
			active = false
			return feishuprotocol.ConfigurationFlow{ID: request.FlowID, Kind: request.Kind, State: "cancelled"}, nil
		case feishuprotocol.SetupRead:
			return managedfeishu.NewSetupStore(root).Load()
		case feishuprotocol.SetupWrite:
			var state managedfeishu.SetupState
			if err := privateipc.DecodeStrict(params, &state, true); err != nil {
				return nil, err
			}
			return state, managedfeishu.NewSetupStore(root).Save(state)
		case "fixture/app-result":
			var request struct {
				Status     string `json:"status"`
				Flow       string `json:"flow"`
				StartError bool   `json:"startError"`
			}
			if err := privateipc.DecodeStrict(params, &request, true); err != nil {
				return nil, err
			}
			outcome, flow, startError = request.Status, request.Flow, request.StartError
			return map[string]bool{"accepted": true}, nil
		case feishuprotocol.MethodAuthStart:
			var request map[string]any
			if err := json.Unmarshal(params, &request); err != nil {
				return nil, err
			}
			if request["kind"] == "user" {
				active, activeKind = true, "user"
				return map[string]any{"status": "pending", "flow": "user", "verificationUrl": "https://example.test/user-oauth"}, nil
			}
			if startError {
				return nil, errors.New("official_cli_config_exists")
			}
			active, activeKind = request["createNew"] == true, "app"
			if request["createNew"] == true && outcome == "completed" && flow == "app-create" {
				if err := markRegistrationBound(); err != nil {
					return nil, err
				}
			}
			return map[string]any{"status": outcome, "flow": flow, "verificationUrl": "https://example.test/create-only", "userCode": "CREATE-CODE", "qrDataURL": "data:image/png;base64,fixture"}, nil
		case feishuprotocol.MethodAuthConfigFinish:
			if !active || outcome == "expired" {
				return nil, errors.New("app_configuration_session_missing")
			}
			if outcome == "completed" && flow == "app-create" {
				if err := markRegistrationBound(); err != nil {
					return nil, err
				}
			}
			return map[string]any{"status": outcome, "flow": flow, "operatorBound": registrationBound(), "verificationUrl": "https://example.test/create-only", "userCode": "CREATE-CODE"}, nil
		case feishuprotocol.MethodAuthCancel:
			return nil, errors.New("unscoped_cancel_forbidden")
		case feishuprotocol.MethodAuthStatus:
			return readEvidence().Auth, nil
		case feishuprotocol.MethodAuthFinish:
			auth := readEvidence().Auth
			if confirmedFeishuUserAuth(auth) {
				return auth, nil
			}
			return feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "pending", Flow: "user-oauth", VerificationURL: "https://example.test/user-oauth"}, nil
		case feishuprotocol.MethodAuthEnsureUser:
			var request struct {
				IdentityRevision string `json:"identityRevision"`
				ContextRevision  string `json:"contextRevision"`
				ApplicationID    string `json:"applicationId"`
			}
			if err := privateipc.DecodeStrict(params, &request, true); err != nil {
				return nil, err
			}
			evidence := readEvidence()
			if _, err := os.Stat(filepath.Join(root, "fixture-binding-conflict")); err == nil {
				return nil, privateipc.NewError(-32065, "feishu_operator_context_changed")
			}
			if request.IdentityRevision == "" || request.IdentityRevision != evidence.IdentityRevision || request.ContextRevision != evidence.ContextRevision || request.ApplicationID != evidence.ApplicationID || !confirmedFeishuUserAuth(evidence.Auth) {
				return nil, privateipc.NewError(-32065, "feishu_operator_context_changed")
			}
			return map[string]string{"status": "configured", "targetAlias": "我"}, nil
		case feishuprotocol.MethodAuthConfigure:
			return map[string]string{"status": "configured", "flow": "existing-app"}, nil
		case feishuprotocol.MethodMessageTest:
			var request struct {
				TargetAlias string `json:"targetAlias"`
				RequestID   string `json:"requestId"`
				Confirm     bool   `json:"confirm"`
			}
			if err := privateipc.DecodeStrict(params, &request, true); err != nil {
				return nil, err
			}
			if request.TargetAlias != "fixture" {
				return nil, errors.New("fixture_target_not_found")
			}
			if _, err := os.Stat(filepath.Join(root, "fixture-test-unknown")); err == nil {
				return nil, errors.New("fixture_test_result_unknown")
			}
			return map[string]string{"messageId": "om_fixture"}, nil
		case feishuprotocol.MethodPermissionsRead:
			if data, err := os.ReadFile(filepath.Join(root, "fixture-permissions.json")); err == nil {
				var result map[string]any
				if err := json.Unmarshal(data, &result); err != nil {
					return nil, err
				}
				return result, nil
			}
			return completeSetupPermissions(), nil
		}
		return nil, privateipc.ErrMethodNotFound
	}))
	_ = peer.Serve(context.Background())
	os.Exit(0)
}

func setAppSetupResult(t *testing.T, service *Service, status, flow string, startError bool) {
	t.Helper()
	if err := service.managedFeishuSupervisor.Call(context.Background(), "fixture/app-result", map[string]any{"status": status, "flow": flow, "startError": startError}, nil); err != nil {
		t.Fatal(err)
	}
}

func appSetupTrace(t *testing.T, service *Service) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(service.feishuDataRoot, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNewAppSetupUsesRegistrationIdentityWithoutSecondOAuth(t *testing.T) {
	service, connected := newAppSetupFixture(t)
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"auth": unauthorizedSetupFixture()})
	ctx := context.Background()
	result, err := service.BeginFeishuSetup(ctx, managedfeishu.SetupModeNew, "", "")
	if err != nil || result["status"] != "pending" {
		t.Fatalf("start: %+v %v", result, err)
	}
	assertPendingAppSetupProjection(t, service, result)
	state, err := service.FeishuSetup()
	if err != nil || state.Stage != managedfeishu.SetupAppPending || state.VerificationURL != "" || state.UserCode != "" {
		t.Fatalf("creation QR leaked into durable setup: %+v %v", state, err)
	}
	generation := service.managedFeishuSupervisor.Generation()
	result, err = service.ContinueFeishuSetup(ctx)
	if err != nil || result["status"] != "pending" || service.managedFeishuSupervisor.Generation() != generation {
		t.Fatalf("pending must not restart or advance: %+v %v", result, err)
	}
	assertPendingAppSetupProjection(t, service, result)
	if strings.Contains(appSetupTrace(t, service), `"kind":"user"`) {
		t.Fatal("pending creation started OAuth")
	}
	setAppSetupResult(t, service, "completed", "app-create", false)
	result, err = service.ContinueFeishuSetup(ctx)
	if err != nil || result["status"] != "completed" || result["setup"].(managedfeishu.SetupState).Stage != managedfeishu.SetupAppConfigured {
		t.Fatalf("completed creation: %+v %v", result, err)
	}
	publicState := result["setup"].(managedfeishu.SetupState)
	if publicState.VerificationURL != "" || publicState.UserCode != "" {
		t.Fatalf("completed setup retained creation projection: %+v", publicState)
	}
	if connection := fixtureConnection(t, connected); connection.err != nil || connection.generation <= generation {
		t.Fatalf("creation did not reload bridge: %+v", connection)
	}
	if strings.Contains(appSetupTrace(t, service), `"kind":"user"`) {
		t.Fatal("creation completion automatically started OAuth")
	}
	result, err = service.ContinueFeishuSetup(ctx)
	if err != nil || result["status"] != "connected" || result["setup"].(managedfeishu.SetupState).Stage != managedfeishu.SetupPlatformPending {
		t.Fatalf("registration identity did not complete connection: %+v %v", result, err)
	}
	trace := appSetupTrace(t, service)
	for _, forbidden := range []string{feishuprotocol.MethodMessageTest, feishuprotocol.MethodAuthEnsureUser, feishuprotocol.SettingsWrite, feishuprotocol.MethodSettingsCompareAndSwap} {
		if strings.Contains(trace, forbidden) {
			t.Fatalf("creation caused unrelated side effect: %s", forbidden)
		}
	}
}

func TestConfigurationCreateAndFinishSettleTheSameDurableFlow(t *testing.T) {
	service, connected := newAppSetupFixture(t)
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{
		"schemaVersion": 1, "contextRevision": "missing", "applicationState": "missing", "botState": "missing",
		"applicationPermissions": "unknown", "userPermissions": "unknown", "botPermissions": "unknown", "operatorState": "missing",
		"checkedAt": time.Now().UTC().Format(time.RFC3339Nano), "problems": []string{}, "auth": unauthorizedSetupFixture(),
	})
	snapshot := awaitConfiguration(t, service)
	create := configurationRequest(snapshot, "create_app")
	result, err := service.ApplyFeishuConfiguration(context.Background(), create)
	if err != nil || result.Outcome != "pending" || result.receiptFlowID != "fixture-flow" {
		t.Fatalf("create did not capture its submitted flow: %+v %v", result, err)
	}
	var rootReceipt ConfigurationReceipt
	if missing, err := privatestore.ReadJSON(service.receiptPath(create.RequestID), &rootReceipt); err != nil || missing || rootReceipt.FlowID != "fixture-flow" || rootReceipt.Outcome != "pending" {
		t.Fatalf("root connection receipt lacks its flow: %+v %v", rootReceipt, err)
	}

	// Let the action-triggered refresh settle before changing the fixture result;
	// the following forced refresh must observe the completed state, not race the
	// earlier pending observation.
	_ = awaitConfiguration(t, service)
	setAppSetupResult(t, service, "completed", "app-create", false)
	service.ReadFeishuConfiguration(context.Background(), true)
	snapshot = awaitConfiguration(t, service)
	finish := configurationRequest(snapshot, "finish_app")
	if snapshot.Flow == nil {
		t.Fatal("completed configuration flow was not projected")
	}
	finish.FlowID = snapshot.Flow.ID
	result, err = service.ApplyFeishuConfiguration(context.Background(), finish)
	if err != nil || result.Outcome != "completed" {
		t.Fatalf("finish failed: %+v %v", result, err)
	}
	if connection := fixtureConnection(t, connected); connection.err != nil {
		t.Fatalf("finish did not reconnect the bridge: %+v", connection)
	}
	if missing, err := privatestore.ReadJSON(service.receiptPath(create.RequestID), &rootReceipt); err != nil || missing || rootReceipt.FlowID != "fixture-flow" || rootReceipt.Outcome != "completed" || rootReceipt.Stage != "verified" {
		t.Fatalf("finish did not settle its root receipt: %+v %v", rootReceipt, err)
	}
}

func assertPendingAppSetupProjection(t *testing.T, service *Service, result map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(result["setup"])
	if err != nil {
		t.Fatal(err)
	}
	var publicState map[string]any
	if err := json.Unmarshal(encoded, &publicState); err != nil {
		t.Fatal(err)
	}
	if publicState["verificationURL"] != "https://example.test/create-only" || publicState["userCode"] != "CREATE-CODE" {
		t.Fatalf("pending setup lacks host-compatible temporary projection: %s", encoded)
	}
	stored, err := service.FeishuSetup()
	if err != nil || stored.VerificationURL != "" || stored.UserCode != "" {
		t.Fatalf("temporary projection persisted: %+v %v", stored, err)
	}
}

func TestRejectedNewAppSetupPreservesPreviousState(t *testing.T) {
	for _, mode := range []string{"config-exists", "unknown-status", "wrong-flow"} {
		t.Run(mode, func(t *testing.T) {
			service, _ := newAppSetupFixture(t)
			previous := managedfeishu.SetupState{Version: 1, Mode: managedfeishu.SetupModeExisting, Stage: managedfeishu.SetupReady}
			if err := (remoteSetupStore{service}).Save(previous); err != nil {
				t.Fatal(err)
			}
			status, flow := "pending", "app-create"
			if mode == "unknown-status" {
				status = "authorized"
			}
			if mode == "wrong-flow" {
				flow = "existing-app"
			}
			setAppSetupResult(t, service, status, flow, mode == "config-exists")
			if _, err := service.BeginFeishuSetup(context.Background(), managedfeishu.SetupModeNew, "", ""); err == nil {
				t.Fatal("failed or ambiguous creation was accepted")
			}
			state, err := service.FeishuSetup()
			if err != nil || state != previous {
				t.Fatalf("failed creation overwrote previous setup: %+v %v", state, err)
			}
		})
	}
}

func TestLostCreationSessionCannotUseOldAuthorization(t *testing.T) {
	service, connected := newAppSetupFixture(t)
	if _, err := service.BeginFeishuSetup(context.Background(), managedfeishu.SetupModeNew, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.restartFeishuSupervisor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if connection := fixtureConnection(t, connected); connection.err != nil {
		t.Fatal(connection.err)
	}
	if _, err := service.ContinueFeishuSetup(context.Background()); err == nil {
		t.Fatal("lost session claimed creation completed")
	}
	if _, err := service.VerifyFeishuSetup(context.Background()); err == nil {
		t.Fatal("verify bypassed missing creation session")
	}
	state, err := service.FeishuSetup()
	if err != nil || state.Stage != managedfeishu.SetupAppPending || state.LastError == "" || state.VerificationURL != "" {
		t.Fatalf("lost session state: %+v %v", state, err)
	}
	trace := appSetupTrace(t, service)
	for _, forbidden := range []string{`"kind":"user"`, feishuprotocol.MethodAuthEnsureUser, feishuprotocol.MethodAuthStatus, feishuprotocol.MethodPermissionsRead} {
		if strings.Contains(trace, forbidden) {
			t.Fatalf("lost session used old authorization: %s", forbidden)
		}
	}
}

func TestReuseAppSetupValidatesExistingDefaultWithoutAuthorization(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	setAppSetupResult(t, service, "configured", "existing-config", false)
	generation := service.managedFeishuSupervisor.Generation()
	result, err := service.BeginFeishuSetup(context.Background(), "reuse", "", "")
	if err != nil || result["status"] != "configured" {
		t.Fatalf("reuse: %+v %v", result, err)
	}
	state, err := service.FeishuSetup()
	if err != nil || state.Mode != managedfeishu.SetupModeExisting || state.Stage != managedfeishu.SetupAppConfigured || state.VerificationURL != "" || state.ReadyToActivate {
		t.Fatalf("reuse setup: %+v %v", state, err)
	}
	if generation != service.managedFeishuSupervisor.Generation() {
		t.Fatal("reuse unexpectedly restarted bridge")
	}
	trace := appSetupTrace(t, service)
	if !strings.Contains(trace, `bridge/auth/start {"createNew":false,"kind":"config","profile":"default"}`) {
		t.Fatalf("reuse did not validate existing default: %s", trace)
	}
	for _, forbidden := range []string{`"kind":"user"`, feishuprotocol.MethodMessageTest, feishuprotocol.MethodAuthEnsureUser, feishuprotocol.SettingsWrite, feishuprotocol.MethodSettingsCompareAndSwap, feishuprotocol.MethodPermissionsRead} {
		if strings.Contains(trace, forbidden) {
			t.Fatalf("reuse caused an unrelated action: %s", forbidden)
		}
	}
}

func TestRejectedReuseAppSetupPreservesPreviousState(t *testing.T) {
	for _, outcome := range []string{"missing-config", "pending", "wrong-flow"} {
		t.Run(outcome, func(t *testing.T) {
			service, _ := newAppSetupFixture(t)
			previous := managedfeishu.SetupState{Version: 1, Mode: managedfeishu.SetupModeExisting, Stage: managedfeishu.SetupReady}
			if err := (remoteSetupStore{service}).Save(previous); err != nil {
				t.Fatal(err)
			}
			status, flow := "configured", "existing-config"
			if outcome == "pending" {
				status = "pending"
			}
			if outcome == "wrong-flow" {
				flow = "app-create"
			}
			setAppSetupResult(t, service, status, flow, outcome == "missing-config")
			if _, err := service.BeginFeishuSetup(context.Background(), "reuse", "", ""); err == nil {
				t.Fatal("unverified existing configuration was accepted")
			}
			state, err := service.FeishuSetup()
			if err != nil || state != previous {
				t.Fatalf("rejected reuse overwrote previous setup: %+v %v", state, err)
			}
		})
	}
}

func TestNewAppSetupAlreadyCompletedNeedsNoSecondOAuth(t *testing.T) {
	service, connected := newAppSetupFixture(t)
	setAppSetupResult(t, service, "completed", "app-create", false)
	generation := service.managedFeishuSupervisor.Generation()
	result, err := service.BeginFeishuSetup(context.Background(), managedfeishu.SetupModeNew, "", "")
	if err != nil || result["setup"].(managedfeishu.SetupState).Stage != managedfeishu.SetupAppConfigured {
		t.Fatalf("completed start: %+v %v", result, err)
	}
	if connection := fixtureConnection(t, connected); connection.err != nil || connection.generation <= generation {
		t.Fatalf("completed start did not reload bridge: %+v", connection)
	}
	verified, err := service.VerifyFeishuSetup(context.Background())
	if err != nil || verified["status"] != "verified" {
		t.Fatalf("completed creation did not become ready: %+v %v", verified, err)
	}
	if strings.Contains(appSetupTrace(t, service), `"kind":"user"`) {
		t.Fatal("completed start automatically authorized user")
	}
}

func TestInvalidAppCreationFinishNeverAdvances(t *testing.T) {
	for _, outcome := range []string{"expired", "authorized", "wrong-flow"} {
		t.Run(outcome, func(t *testing.T) {
			service, _ := newAppSetupFixture(t)
			if _, err := service.BeginFeishuSetup(context.Background(), managedfeishu.SetupModeNew, "", ""); err != nil {
				t.Fatal(err)
			}
			status, flow := outcome, "app-create"
			if outcome == "wrong-flow" {
				status, flow = "completed", "user-oauth"
			}
			setAppSetupResult(t, service, status, flow, false)
			if _, err := service.ContinueFeishuSetup(context.Background()); err == nil {
				t.Fatal("invalid creation result accepted")
			}
			state, err := service.FeishuSetup()
			if err != nil || state.Stage != managedfeishu.SetupAppPending || state.ReadyToActivate || state.VerificationURL != "" {
				t.Fatalf("invalid creation advanced setup: %+v %v", state, err)
			}
			if strings.Contains(appSetupTrace(t, service), `"kind":"user"`) {
				t.Fatal("invalid creation triggered OAuth")
			}
		})
	}
}

func TestCancelAppCreationStopsOnlySessionAndPreservesSetup(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	if _, err := service.BeginFeishuSetup(context.Background(), managedfeishu.SetupModeNew, "", ""); err != nil {
		t.Fatal(err)
	}
	previous, err := service.FeishuSetup()
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.CancelFeishuSetup()
	if err != nil || state != previous {
		t.Fatalf("cancel setup: %+v %v", state, err)
	}
	if err := service.managedFeishuSupervisor.Call(context.Background(), feishuprotocol.MethodAuthConfigFinish, map[string]any{}, nil); err == nil {
		t.Fatal("cancel left creation session active")
	}
	trace := appSetupTrace(t, service)
	cancelIndex := strings.Index(trace, feishuprotocol.MethodConfigurationCancel)
	resetIndex := strings.LastIndex(trace, feishuprotocol.SetupWrite)
	if cancelIndex < 0 || resetIndex >= cancelIndex || strings.Contains(trace, `"kind":"user"`) {
		t.Fatalf("unexpected cancel sequence: %s", trace)
	}
}
