package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
)

func completeSetupPermissions() map[string]any {
	return map[string]any{"permissions": map[string]any{
		"verified": true,
		"identities": map[string]any{
			"bot": map[string]any{"ready": true, "scopeVerification": "verified_by_lark_cli_auth_scopes", "application": map[string]any{"complete": true, "missing": []string{}}},
			"user": map[string]any{
				"ready": true, "complete": true, "missing": []string{},
				"application": map[string]any{"complete": true, "missing": []string{}},
				"oauth":       map[string]any{"complete": true, "missing": []string{}},
			},
		},
	}}
}

func writeSetupFixture(t *testing.T, service *Service, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service.feishuDataRoot, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func authorizedSetupFixture() feishuprotocol.AuthStatus {
	return feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "authorized", Identity: "user", Profile: "default", IdentityValid: true, ProfileValid: true, MissingCapabilities: []string{}}
}

func unauthorizedSetupFixture() feishuprotocol.AuthStatus {
	return feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "unauthorized", Identity: "user", Profile: "default", ProfileValid: true, MissingCapabilities: []string{}}
}

func TestAppSetupFixtureSupportsConfigurationEvidenceAndMessageTrace(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	evidence, err := service.readConfigurationEvidence(context.Background())
	if err != nil || evidence.ApplicationState != "present" || !confirmedFeishuUserAuth(evidence.Auth) || evidence.BotState != "present" || evidence.OperatorState != "missing" || evidence.ContextRevision != "fixture" {
		t.Fatalf("default evidence: %+v %v", evidence, err)
	}
	var flow *feishuprotocol.ConfigurationFlow
	if err := service.managedFeishuSupervisor.Call(context.Background(), feishuprotocol.MethodConfigurationFlow, map[string]any{}, &flow); err != nil || flow != nil {
		t.Fatalf("default flow: %+v %v", flow, err)
	}
	if err := service.managedFeishuSupervisor.Call(context.Background(), "fixture/configuration-evidence", map[string]any{"applicationState": "missing", "contextRevision": "changed", "auth": nil}, nil); err != nil {
		t.Fatal(err)
	}
	evidence, err = service.readConfigurationEvidence(context.Background())
	if err != nil || evidence.ApplicationState != "missing" || evidence.ContextRevision != "changed" || evidence.Auth != nil {
		t.Fatalf("configured evidence not returned: %+v %v", evidence, err)
	}
	snapshot, err := service.fetchFeishuSnapshot(context.Background())
	if err != nil || len(snapshot.TargetAliases) != 1 || snapshot.TargetAliases[0] != "fixture" {
		t.Fatalf("fixture target absent: %+v %v", snapshot, err)
	}
	before := appSetupTrace(t, service)
	if err := service.sendConfigurationTest(context.Background(), "fixture", "test-fixture"); err != nil {
		t.Fatal(err)
	}
	trace := appSetupTrace(t, service)[len(before):]
	if strings.Count(trace, feishuprotocol.MethodMessageTest) != 1 || !strings.Contains(trace, `"targetAlias":"fixture"`) {
		t.Fatalf("test message not traced exactly once: %s", trace)
	}
}

func assertNoSetupMutations(t *testing.T, trace string) {
	t.Helper()
	for _, forbidden := range []string{feishuprotocol.SettingsWrite, feishuprotocol.MethodSettingsCompareAndSwap, feishuprotocol.SetupWrite, feishuprotocol.MethodAuthEnsureUser, feishuprotocol.MethodAuthConfigure, feishuprotocol.MethodAuthStart, feishuprotocol.MethodMessageTest, feishuprotocol.Initialize} {
		if strings.Contains(trace, forbidden) {
			t.Fatalf("read caused a mutation: %s\n%s", forbidden, trace)
		}
	}
}

func TestSettingsCASConflictsNeverRestartOrOverwrite(t *testing.T) {
	for _, action := range []string{"legacy-internal"} {
		for _, timing := range []string{"before-read", "before-write"} {
			if action == "legacy-internal" && timing == "before-read" {
				continue
			}
			t.Run(action+"/"+timing, func(t *testing.T) {
				service, _ := newAppSetupFixture(t)
				expected := managedfeishu.DefaultSettings()
				store := managedfeishu.NewSettingsStore(service.feishuDataRoot)
				if timing == "before-read" {
					changed := expected
					changed.Codex.DefaultThreadTitle = "external-fixture"
					if err := store.Save(changed); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(filepath.Join(service.feishuDataRoot, "fixture-cas-conflict"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				before := appSetupTrace(t, service)
				var err error
				switch action {
				case "activate":
					_, err = service.ActivateFeishuSetupWithExpected(context.Background(), expected)
				case "feature":
					_, err = service.UpdateFeishuFeature(context.Background(), FeishuFeatureUpdateRequest{Feature: "peopleDirectory", Mode: "enabled", ExpectedSettings: &expected})
				default:
					next := expected
					next.Group.Enabled = true
					_, err = service.UpdateFeishuSettings(next)
				}
				if !errors.Is(err, ErrFeishuSettingsConflict) {
					t.Fatalf("conflict was obscured or ignored: %v", err)
				}
				actual, err := store.Load()
				if err != nil || actual.Codex.DefaultThreadTitle != "external-fixture" || actual.Group.Enabled {
					t.Fatalf("external settings overwritten: %+v %v", actual, err)
				}
				trace := appSetupTrace(t, service)[len(before):]
				for _, forbidden := range []string{feishuprotocol.SettingsWrite, feishuprotocol.Initialize, feishuprotocol.MethodMessageTest} {
					if strings.Contains(trace, forbidden) {
						t.Fatalf("conflict caused side effects: %s", trace)
					}
				}
				wantCAS := 0
				if timing == "before-write" {
					wantCAS = 1
				}
				if strings.Count(trace, feishuprotocol.MethodSettingsCompareAndSwap) != wantCAS {
					t.Fatalf("CAS retried or bypassed: %s", trace)
				}
			})
		}
	}
}

func TestVerifySetupPreservesLiveSettingsAndEveryLegacyStage(t *testing.T) {
	for _, stage := range []string{"not_started", "app_configured", "authorization_pending", "platform_pending", "verifying", "failed", "ready"} {
		t.Run(stage, func(t *testing.T) {
			service, _ := newAppSetupFixture(t)
			settings := managedfeishu.DefaultSettings()
			settings.Outbound = managedfeishu.DryRunSwitch{Enabled: true, DryRun: false}
			settings.Actionbox = managedfeishu.DryRunSwitch{Enabled: true, DryRun: false}
			if err := managedfeishu.NewSettingsStore(service.feishuDataRoot).Save(settings); err != nil {
				t.Fatal(err)
			}
			state := managedfeishu.SetupState{Version: 1, Mode: "existing", Stage: stage, ReadyToActivate: true, LastError: "previous check"}
			if err := managedfeishu.NewSetupStore(service.feishuDataRoot).Save(state); err != nil {
				t.Fatal(err)
			}
			writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"operatorState": "present", "operatorAlias": "我"})
			before := appSetupTrace(t, service)
			result, err := service.VerifyFeishuSetup(context.Background())
			if err != nil || result["status"] != "verified" || result["setup"] != state {
				t.Fatalf("verify: %+v %v", result, err)
			}
			stored, err := managedfeishu.NewSettingsStore(service.feishuDataRoot).Load()
			if err != nil || stored != settings {
				t.Fatalf("verification changed live settings: %+v %v", stored, err)
			}
			storedState, err := managedfeishu.NewSetupStore(service.feishuDataRoot).Load()
			if err != nil || storedState != state {
				t.Fatalf("verification rewrote legacy setup: %+v %v", storedState, err)
			}
			assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
		})
	}
}

func TestVerifySetupUnknownEvidenceDoesNotManufactureReadiness(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	writeSetupFixture(t, service, "fixture-permissions.json", map[string]any{})
	before := appSetupTrace(t, service)
	result, err := service.VerifyFeishuSetup(context.Background())
	if err != nil || result["status"] != "incomplete" {
		t.Fatalf("missing permission evidence accepted: %+v %v", result, err)
	}
	assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
}

func TestAppBoundOperatorContinuesWithoutOAuth(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	state := managedfeishu.SetupState{Version: 1, Mode: "existing", Stage: "app_configured"}
	if err := managedfeishu.NewSetupStore(service.feishuDataRoot).Save(state); err != nil {
		t.Fatal(err)
	}
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"auth": unauthorizedSetupFixture(), "operatorState": "present", "operatorAlias": "我"})
	before := appSetupTrace(t, service)
	result, err := service.ContinueFeishuSetup(context.Background())
	if err != nil || result["status"] != "connected" || result["setup"].(managedfeishu.SetupState).Stage != "platform_pending" {
		t.Fatalf("existing auth continuation: %+v %v", result, err)
	}
	trace := appSetupTrace(t, service)[len(before):]
	for _, forbidden := range []string{feishuprotocol.MethodAuthStart, feishuprotocol.MethodAuthEnsureUser, feishuprotocol.SettingsWrite, feishuprotocol.MethodSettingsCompareAndSwap, feishuprotocol.MethodMessageTest} {
		if strings.Contains(trace, forbidden) {
			t.Fatalf("existing authorization caused side effect: %s", trace)
		}
	}
}

func TestOperatorBindingRequiresConfirmationAndVerifiedIdentity(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	expected, err := service.readConfigurationEvidence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"auth": unauthorizedSetupFixture()})
	before := appSetupTrace(t, service)
	if err := service.BindFeishuOperatorWithExpected(context.Background(), false, expected); err == nil {
		t.Fatal("operator binding accepted without confirmation")
	}
	if appSetupTrace(t, service) != before {
		t.Fatal("unconfirmed binding called bridge")
	}
	if err := service.BindFeishuOperatorWithExpected(context.Background(), true, expected); err == nil {
		t.Fatal("unverified user was bound")
	}
	if strings.Contains(appSetupTrace(t, service), feishuprotocol.MethodAuthEnsureUser) {
		t.Fatal("unverified identity expanded inbound authorization")
	}
	writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"auth": authorizedSetupFixture()})
	if err := service.BindFeishuOperatorWithExpected(context.Background(), true, expected); err != nil {
		t.Fatal(err)
	}
	if strings.Count(appSetupTrace(t, service), feishuprotocol.MethodAuthEnsureUser) != 1 {
		t.Fatal("confirmed binding did not execute exactly once")
	}
}

func TestOperatorBindingRejectsChangedConfirmationContext(t *testing.T) {
	for _, field := range []string{"identityRevision", "contextRevision", "applicationId"} {
		t.Run(field, func(t *testing.T) {
			service, _ := newAppSetupFixture(t)
			expected, err := service.readConfigurationEvidence(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{field: "changed"})
			before := appSetupTrace(t, service)
			if err := service.BindFeishuOperatorWithExpected(context.Background(), true, expected); !errors.Is(err, ErrFeishuOperatorContextConflict) {
				t.Fatalf("changed confirmation context not classified: %v", err)
			}
			trace := appSetupTrace(t, service)[len(before):]
			assertNoSetupMutations(t, trace)
		})
	}
	service, _ := newAppSetupFixture(t)
	before := appSetupTrace(t, service)
	if err := service.BindFeishuOperator(context.Background(), true); err == nil {
		t.Fatal("legacy binding bypassed expected context")
	}
	if appSetupTrace(t, service) != before {
		t.Fatal("legacy binding called bridge")
	}
	expected, err := service.readConfigurationEvidence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service.feishuDataRoot, "fixture-binding-conflict"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.BindFeishuOperatorWithExpected(context.Background(), true, expected); !errors.Is(err, ErrFeishuOperatorContextConflict) {
		t.Fatalf("private conflict code lost: %v", err)
	}
}

func TestActivationAllowsBotWithoutUserAuthorizationOrGlobalScopes(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	before := appSetupTrace(t, service)
	_, err := service.ActivateFeishuSetup(context.Background(), "")
	if err == nil || err.Error() != "configuration_action_retired" {
		t.Fatalf("retired operation not rejected: %v", err)
	}
	assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
}

func TestActivationRejectsUnknownOrMissingBotIdentityBeforeWriting(t *testing.T) {
	for _, bot := range []string{"unknown", "missing", "failed", "stale", ""} {
		t.Run(bot, func(t *testing.T) {
			service, _ := newAppSetupFixture(t)
			writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"botState": bot})
			before := appSetupTrace(t, service)
			if _, err := service.ActivateFeishuSetup(context.Background(), ""); err == nil {
				t.Fatal("unverified bot enabled outbound")
			}
			assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
		})
	}
}

func TestActivationUnknownOutcomeDoesNotRollbackOrReplay(t *testing.T) {
	service, _ := newAppSetupFixture(t)
	before := appSetupTrace(t, service)
	_, err := service.ActivateFeishuSetup(context.Background(), "")
	if err == nil || err.Error() != "configuration_action_retired" {
		t.Fatalf("retired operation not rejected: %v", err)
	}
	assertNoSetupMutations(t, appSetupTrace(t, service)[len(before):])
}

func TestSettingsRestartFailureRetainsConfirmedWrite(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), filepath.Base(executable))
	if err := os.WriteFile(fixture, data, 0o700); err != nil {
		t.Fatal(err)
	}
	service, _ := newAppSetupFixture(t, fixture)
	settings := managedfeishu.DefaultSettings()
	if err := os.Rename(fixture, fixture+".moved"); err != nil {
		t.Skipf("platform cannot move a running test executable: %v", err)
	}
	if err := service.saveAndRestartFeishuSettings(context.Background(), settings); err == nil || !strings.Contains(err.Error(), "已保存") {
		t.Fatalf("restart failure did not report committed settings: %v", err)
	}
	stored, err := managedfeishu.NewSettingsStore(service.feishuDataRoot).Load()
	if err != nil || stored != settings {
		t.Fatalf("restart failure restored old settings: %+v %v", stored, err)
	}
	if strings.Count(appSetupTrace(t, service), feishuprotocol.MethodSettingsCompareAndSwap) != 1 || strings.Contains(appSetupTrace(t, service), feishuprotocol.SettingsWrite) {
		t.Fatal("restart failure caused rollback write")
	}
}

func TestCancelWithoutPendingFlowPreservesTerminalAndCheckState(t *testing.T) {
	for _, flowState := range []string{"completed", "failed", "expired", "cancelled", ""} {
		t.Run(flowState, func(t *testing.T) {
			service, _ := newAppSetupFixture(t)
			previous := managedfeishu.SetupState{Version: 1, Stage: "failed", Mode: "existing", ReadyToActivate: true, LastError: "previous check"}
			if err := managedfeishu.NewSetupStore(service.feishuDataRoot).Save(previous); err != nil {
				t.Fatal(err)
			}
			writeSetupFixture(t, service, "fixture-evidence.json", map[string]any{"flow": feishuprotocol.ConfigurationFlow{ID: "previous", Kind: "user", State: flowState}})
			before := appSetupTrace(t, service)
			state, err := service.CancelFeishuSetup()
			if err != nil || state != previous {
				t.Fatalf("cancel rewrote terminal state: %+v %v", state, err)
			}
			trace := appSetupTrace(t, service)[len(before):]
			assertNoSetupMutations(t, trace)
			if strings.Contains(trace, feishuprotocol.MethodConfigurationCancel) || strings.Contains(trace, feishuprotocol.MethodAuthCancel) {
				t.Fatal("terminal flow was cancelled again")
			}
		})
	}
}

func TestPermissionsRequireExplicitEvidenceAndKeepIdentitiesSeparate(t *testing.T) {
	paths := []string{"verified", "identities.bot", "identities.bot.ready", "identities.bot.application", "identities.bot.application.complete", "identities.bot.application.missing"}
	for _, path := range paths {
		for _, malformed := range []any{nil, "", true, map[string]any{}} {
			value := completeSetupPermissions()
			parent := value["permissions"].(map[string]any)
			keys := strings.Split(path, ".")
			for _, key := range keys[:len(keys)-1] {
				parent = parent[key].(map[string]any)
			}
			if malformed == true && (keys[len(keys)-1] == "verified" || keys[len(keys)-1] == "ready" || keys[len(keys)-1] == "complete") {
				continue
			}
			parent[keys[len(keys)-1]] = malformed
			if permissionsReady(value) {
				t.Fatalf("missing/malformed evidence accepted: %s = %#v", path, malformed)
			}
		}
	}
	value := completeSetupPermissions()
	bot := value["permissions"].(map[string]any)["identities"].(map[string]any)["bot"].(map[string]any)
	delete(bot, "application")
	if overview := feishuPermissionOverview(value, nil); overview.Application != "unknown" || overview.User != "verified" {
		t.Fatalf("permission sources conflated: %+v", overview)
	}
	if overview := feishuPermissionOverview(nil, nil); overview.Application == "verified" || overview.User == "verified" {
		t.Fatalf("empty report verified: %+v", overview)
	}
}
