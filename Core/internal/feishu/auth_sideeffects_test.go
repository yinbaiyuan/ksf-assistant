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

func TestLogoutRequiresExplicitUserMissingEvidence(t *testing.T) {
	for _, test := range []struct {
		name, response, want string
	}{
		{"missing", `{"available":false,"status":"missing"}`, "unauthorized"},
		{"unavailable-only", `{"available":false}`, "unknown"},
		{"verify-failed", `{"available":false,"verified":false,"status":"verify_failed"}`, "unknown"},
		{"missing-but-verify-failed", `{"available":false,"verified":false,"status":"missing"}`, "unknown"},
		{"not-configured", `{"available":false,"status":"not_configured"}`, "unknown"},
		{"available", `{"available":true,"verified":true}`, "unknown"},
		{"available-unverified", `{"available":true,"verified":false}`, "unknown"},
		{"unknown", `{}`, "unknown"},
		{"malformed", `{"available":"false"}`, "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := fakeAuthCLI(t, `printf '%s\n' "$4" >> calls
case "$4" in
logout) printf '{"ok":true,"loggedOut":true}' ;;
status) printf '%s' '{"appId":"cli_fixture","brand":"feishu","identities":{"user":`+test.response+`}}' ;;
*) exit 1 ;;
esac`)
			status, err := LogoutUserAuth(context.Background(), runner, runner.DataRoot)
			if err != nil || status.Status != test.want {
				t.Fatalf("logout result: %+v %v", status, err)
			}
			if capabilitypolicy.CheckSession(runner.DataRoot) == nil {
				t.Fatal("logout did not block bot execution")
			}
			calls, err := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
			if err != nil || string(calls) != "logout\nstatus\n" {
				t.Fatalf("unexpected logout actions: %s %v", calls, err)
			}
		})
	}
	t.Run("verification-failed", func(t *testing.T) {
		runner := fakeAuthCLI(t, `if [ "$4" = logout ]; then printf '{"ok":true,"loggedOut":true}'; else exit 1; fi`)
		status, err := LogoutUserAuth(context.Background(), runner, runner.DataRoot)
		if err != nil || status.Status != "unknown" {
			t.Fatalf("unverified logout claimed success: %+v %v", status, err)
		}
	})
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
			if (user["application"] != nil) != test.appKnown || (user["oauth"] != nil) != test.oauthKnown || bot["scopeVerification"] != "not_exposed_by_lark_cli_auth_scopes" {
				t.Fatalf("independent evidence lost: %+v", permissions)
			}
		})
	}
}
