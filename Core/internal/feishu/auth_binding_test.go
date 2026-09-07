package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func operatorBindingFixture(t *testing.T) (CapabilityExecutor, ClientConfigStore, OperatorBindingExpectation) {
	t.Helper()
	runner := fakeAuthCLI(t, `printf '%s\n' "$3/$4" >> calls
case "$4" in
status) if [ -f change-config ]; then printf '{"changed":"during-verification"}' > "$LARKSUITE_CLI_CONFIG_DIR/config.json"; fi; if [ -f change-timestamp ]; then touch -t 202001010101 "$LARKSUITE_CLI_CONFIG_DIR/config.json"; fi; cat status.json ;;
scopes) printf '{"appId":"cli_fixture","brand":"feishu","tokenType":"user","userScopes":[]}' ;;
*) exit 1 ;;
esac`)
	configurationFixtureFile(t)
	status := `{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"verified":true,"status":"ready","openId":"ou_expected"},"bot":{"available":true,"verified":true}}}`
	if err := os.WriteFile(filepath.Join(runner.DataRoot, "status.json"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewClientConfigStore(runner.DataRoot)
	initial := DefaultClientConfig()
	initial.Extra["future"] = json.RawMessage(`{"enabled":true}`)
	if err := store.Save(initial); err != nil {
		t.Fatal(err)
	}
	evidence, err := ReadConfigurationEvidence(context.Background(), runner, runner.DataRoot)
	if err != nil || evidence.IdentityRevision == "" || evidence.ContextRevision == "" {
		t.Fatal("fixture evidence unavailable", err)
	}
	if err := os.WriteFile(filepath.Join(runner.DataRoot, "calls"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return runner, store, OperatorBindingExpectation{IdentityRevision: evidence.IdentityRevision, ContextRevision: evidence.ContextRevision, ApplicationID: evidence.ApplicationID}
}

func TestOperatorBindingUsesOnlyConfirmedVerifiedStatusIdentity(t *testing.T) {
	runner, store, expected := operatorBindingFixture(t)
	stamp := time.Unix(123456789, 0)
	if err := os.Chtimes(filepath.Join(os.Getenv("LARKSUITE_CLI_CONFIG_DIR"), "config.json"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	result, err := EnsureCurrentUserWithExpected(context.Background(), runner, store, expected)
	if err != nil || result["status"] != "configured" {
		t.Fatalf("binding failed: %v", err)
	}
	config, err := store.Load()
	if err != nil || config.MessageTargets["我"].ID != "ou_expected" || !contains(config.DirectAllowedAliases, "我") || config.Extra["future"] == nil {
		t.Fatal("confirmed identity or unrelated fields not preserved", err)
	}
	calls, err := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
	if err != nil || string(calls) != "auth/status\n" {
		t.Fatalf("binding reselected user or ran unexpected CLI: %s %v", calls, err)
	}
	public, _ := json.Marshal(result)
	if strings.Contains(string(public), "ou_expected") {
		t.Fatal("binding exported raw user identity")
	}
}

func TestOperatorBindingRejectsChangedIdentityApplicationAndContext(t *testing.T) {
	for _, change := range []string{"identity", "application", "scope", "verified", "config-during-check", "timestamp-during-check", "policy", "cancelled", "legacy"} {
		t.Run(change, func(t *testing.T) {
			runner, store, expected := operatorBindingFixture(t)
			path := filepath.Join(runner.DataRoot, "status.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "identity":
				data = []byte(strings.ReplaceAll(string(data), "ou_expected", "ou_other"))
			case "application":
				data = []byte(strings.ReplaceAll(string(data), "cli_fixture", "cli_other"))
			case "scope":
				data = []byte(strings.ReplaceAll(string(data), `"openId":"ou_expected"`, `"openId":"ou_expected","scope":"changed"`))
			case "verified":
				data = []byte(strings.ReplaceAll(string(data), `"verified":true`, `"verified":false`))
			case "config-during-check":
				if err := os.WriteFile(filepath.Join(runner.DataRoot, "change-config"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "timestamp-during-check":
				if err := os.WriteFile(filepath.Join(runner.DataRoot, "change-timestamp"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "policy":
				config, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				config.MessageTargets["other"] = MessageTarget{Type: "open_id", ID: "ou_other"}
				if err := store.Save(config); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(store.Path())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if change == "cancelled" {
				cancel()
			}
			if change == "legacy" {
				_, err = EnsureCurrentUser(ctx, runner, store)
			} else {
				_, err = EnsureCurrentUserWithExpected(ctx, runner, store, expected)
			}
			if err == nil || change != "cancelled" && change != "legacy" && !errors.Is(err, ErrOperatorContextConflict) {
				t.Fatalf("changed context did not fail closed: %v", err)
			}
			after, readErr := os.ReadFile(store.Path())
			if readErr != nil || string(after) != string(before) {
				t.Fatal("refused binding changed persisted client config")
			}
			if change == "cancelled" || change == "legacy" {
				calls, err := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
				if err != nil || len(calls) != 0 {
					t.Fatal("legacy or cancelled binding started CLI")
				}
			}
		})
	}
}
