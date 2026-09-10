package feishu

import (
	"context"
	"encoding/json"
	"ksfassistant/core/internal/capabilitypolicy"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureExistingAppRefusesEveryExistingConfigBeforeCLI(t *testing.T) {
	for _, contents := range []string{`{"profiles":{"default":{}}}`, `{"profiles":{"other":{}}}`, `broken`, ``} {
		t.Run(contents, func(t *testing.T) {
			runner := fakeAuthCLI(t, `touch unexpected-process; exit 1`)
			configDir := os.Getenv("LARKSUITE_CLI_CONFIG_DIR")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(configDir, "config.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ConfigureExistingApp(context.Background(), runner, "cli_fixture", "fixture-secret", "feishu", "default"); err == nil {
				t.Fatal("existing config accepted for overwrite")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != contents {
				t.Fatal("existing config changed")
			}
			if _, err := os.Stat(filepath.Join(runner.DataRoot, "unexpected-process")); !os.IsNotExist(err) {
				t.Fatal("refused configuration started CLI")
			}
		})
	}
}

func TestLogoutPurgesLocalProfileWithoutDependingOnUserStatus(t *testing.T) {
	runner := fakeAuthCLI(t, `printf '%s\n' "$4" >> calls
if [ "$4" = logout ]; then rm -f "$LARKSUITE_CLI_CONFIG_DIR/config.json"; printf '{"ok":true,"loggedOut":true,"purged":true,"remoteRevocationConfirmed":false}'; else exit 1; fi`)
	if err := capabilitypolicy.SignOut(runner.DataRoot); err != nil {
		t.Fatal(err)
	}
	status, err := LogoutUserAuth(context.Background(), runner, runner.DataRoot)
	if err != nil || status.Status != "unauthorized" || status.RemoteRevocationConfirmed == nil || *status.RemoteRevocationConfirmed {
		t.Fatalf("logout result: %+v %v", status, err)
	}
	if capabilitypolicy.CheckSession(runner.DataRoot) != nil {
		t.Fatal("legacy signed-out marker was not removed with local authentication state")
	}
	calls, err := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
	if err != nil || string(calls) != "logout\n" {
		t.Fatalf("unexpected logout actions: %s %v", calls, err)
	}
}

func TestLogoutCLIFailureLeavesCleanupFailClosed(t *testing.T) {
	runner := fakeAuthCLI(t, `exit 1`)
	if _, err := LogoutUserAuth(context.Background(), runner, runner.DataRoot); err == nil {
		t.Fatal("failed CLI logout claimed success")
	}
	if !LocalFeishuCleanupPending(runner.DataRoot) || capabilitypolicy.CheckSession(runner.DataRoot) == nil {
		t.Fatal("failed CLI logout did not preserve the cleanup gate")
	}
}

func TestReadCLIAuthStatusDoesNotTurnVerificationFailureIntoUnauthorized(t *testing.T) {
	for _, test := range []struct{ name, user, want string }{
		{"network-failure", `{"available":false,"verified":false,"status":"verify_failed"}`, "failed"},
		{"verify-failed-without-flag", `{"available":false,"status":"verify_failed"}`, "failed"},
		{"verify-false-without-status", `{"available":false,"verified":false}`, "failed"},
		{"unavailable-only", `{"available":false}`, "unknown"},
		{"missing", `{"available":false,"status":"missing"}`, "unauthorized"},
		{"not-configured", `{"available":false,"status":"not_configured"}`, "unknown"},
		{"unverified", `{"available":true,"status":"ready"}`, "unknown"},
		{"verified", `{"available":true,"verified":true,"status":"ready"}`, "authorized"},
		{"refreshed", `{"available":true,"verified":true,"status":"needs_refresh"}`, "authorized"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := fakeAuthCLI(t, `if [ "$4" = scopes ]; then printf '{"userScopes":[]}'; else printf '%s' '{"appId":"cli_fixture","brand":"feishu","identities":{"user":`+test.user+`}}'; fi`)
			status, err := readCLIAuthStatus(context.Background(), runner)
			if err != nil || status.Status != test.want || status.IdentityValid != (test.want == "authorized") {
				t.Fatalf("identity observation changed meaning: %+v %v", status, err)
			}
		})
	}
	t.Run("read-failed", func(t *testing.T) {
		runner := fakeAuthCLI(t, `exit 1`)
		status, err := readCLIAuthStatus(context.Background(), runner)
		if err == nil || status.Status != "unknown" {
			t.Fatalf("read failure became logged out: %+v %v", status, err)
		}
	})
}

func TestAuthConfigurationCancelledBeforeAnyProcess(t *testing.T) {
	runner := fakeAuthCLI(t, `touch unexpected-process`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ConfigureExistingApp(ctx, runner, "cli_fixture", "fixture-secret", "feishu", "default"); err == nil {
		t.Fatal("cancelled configuration accepted")
	}
	if status, err := LogoutUserAuth(ctx, runner, runner.DataRoot); err == nil || status.Status != "unknown" {
		t.Fatal("cancelled logout accepted")
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "unexpected-process")); !os.IsNotExist(err) {
		t.Fatal("cancelled request started CLI")
	}
}

func TestAuthPermissionsUnknownEvidenceRemainsIndependent(t *testing.T) {
	contract, err := RequiredPermissionScopes()
	if err != nil {
		t.Fatal(err)
	}
	scopeJSON, err := json.Marshal(strings.Join(contract.User, " "))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, scope, scopesReply string
		appKnown, oauthKnown     bool
	}{
		{"app-unavailable", string(scopeJSON), `exit 1`, false, true},
		{"oauth-missing", `null`, `printf '{"userScopes":[]}'`, true, false},
		{"oauth-malformed", `["im:message",1]`, `printf '{"userScopes":[]}'`, true, false},
		{"app-malformed", string(scopeJSON), `printf '{"userScopes":[1]}'`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := fakeAuthCLI(t, `case "$4" in
status) printf '%s' '{"appId":"cli_fixture","identities":{"user":{"available":true,"scope":`+test.scope+`}}}' ;;
scopes) `+test.scopesReply+` ;;
esac`)
			result, err := AuthPermissions(context.Background(), runner)
			if err != nil {
				t.Fatal(err)
			}
			permissions := result["permissions"].(map[string]any)
			identities := permissions["identities"].(map[string]any)
			user := identities["user"].(map[string]any)
			bot := identities["bot"].(map[string]any)
			if permissions["verified"] != nil || user["ready"] != nil || bot["ready"] != nil || user["complete"] != nil || user["missing"] != nil {
				t.Fatalf("unknown fields claimed evidence: %+v", permissions)
			}
			if (user["application"] != nil) != test.appKnown || (user["oauth"] != nil) != test.oauthKnown || bot["scopeVerification"] != "verified_by_lark_cli_auth_scopes" {
				t.Fatalf("independent evidence lost: %+v", permissions)
			}
		})
	}
}

func TestAuthPermissionsComparesOnlyCurrentProgressiveRequest(t *testing.T) {
	scopes, _ := json.Marshal(map[string]any{
		"appId": "cli_fixture", "brand": "feishu", "tokenType": "user",
		"userScopes": []string{"docx:document:readonly"}, "botScopes": BaseConnectionPermissionScopes(),
	})
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"verified":true,"scope":[]},"bot":{"available":true,"verified":true}}}' ;;
scopes) printf '%s' '`+string(scopes)+`' ;;
esac`)
	request, err := writeProgressiveAuthorizationRequest(runner.DataRoot, "cli_fixture", "docs.fixture.read", []string{"docx:document:readonly"})
	if err != nil || request == nil {
		t.Fatal(err)
	}
	result, err := AuthPermissions(context.Background(), runner)
	if err != nil {
		t.Fatal(err)
	}
	permissions := result["permissions"].(map[string]any)
	user := permissions["identities"].(map[string]any)["user"].(map[string]any)
	missing := user["missing"].([]string)
	if user["requiredCount"] != 1 || len(missing) != 1 || missing[0] != "docx:document:readonly" {
		t.Fatalf("full user catalog leaked into base readiness: %#v", user)
	}
}
