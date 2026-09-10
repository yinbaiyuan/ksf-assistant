package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

func configurationFixtureFile(t *testing.T) {
	t.Helper()
	root := os.Getenv("LARKSUITE_CLI_CONFIG_DIR")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"fixture":true}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationMissingDoesNotInvokeCLI(t *testing.T) {
	runner := fakeAuthCLI(t, `touch unexpected-call; exit 1`)
	evidence, err := ReadConfigurationEvidence(context.Background(), runner, runner.DataRoot)
	if err != nil || evidence.ApplicationState != "missing" || evidence.ContextRevision == "" {
		t.Fatalf("evidence: %+v %v", evidence, err)
	}
	if _, err := os.Stat(filepath.Join(runner.WorkingDirectory, "unexpected-call")); !os.IsNotExist(err) {
		t.Fatal("missing configuration invoked CLI")
	}
}

func TestConfigurationEvidenceDoesNotBindUserOrInventScopes(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"verified":true,"openId":"ou_fixture"},"bot":{"available":true,"verified":true}}}' ;;
scopes) printf '{}' ;;
*) touch unexpected-call; exit 1 ;;
esac`)
	configurationFixtureFile(t)
	before, err := NewClientConfigStore(runner.DataRoot).Load()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := ReadConfigurationEvidence(context.Background(), runner, runner.DataRoot)
	if err != nil || evidence.ApplicationState != "present" || evidence.Auth == nil || evidence.Auth.Status != "authorized" || evidence.BotState != "present" {
		t.Fatalf("evidence: %+v %v", evidence, err)
	}
	if evidence.OperatorState != "missing" || evidence.UserPermissions != "unknown" || evidence.ApplicationPermissions != "unknown" || evidence.BotPermissions != "unknown" {
		t.Fatalf("invented facts: %+v", evidence)
	}
	if evidence.ApplicationID != "cli_fixture" {
		t.Fatal("verified application identity missing from evidence")
	}
	after, err := NewClientConfigStore(runner.DataRoot).Load()
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("read changed operator policy")
	}
	encoded, _ := json.Marshal(evidence)
	if strings.Contains(string(encoded), "ou_fixture") {
		t.Fatal("raw identity leaked into public evidence")
	}
	if _, err := os.Stat(filepath.Join(runner.WorkingDirectory, "unexpected-call")); !os.IsNotExist(err) {
		t.Fatal("unexpected command")
	}
}

func TestConfigurationEvidenceSeparatesRequestedApplicationScopeFromUserGrant(t *testing.T) {
	contract, err := RequiredPermissionScopes()
	if err != nil {
		t.Fatal(err)
	}
	offered := make([]string, 0, len(contract.User))
	for _, scope := range contract.User {
		if scope != "docx:document:readonly" {
			offered = append(offered, scope)
		}
	}
	scopesJSON, _ := json.Marshal(map[string]any{
		"appId": "cli_fixture", "brand": "feishu", "tokenType": "user",
		"userScopes": offered, "botScopes": BaseConnectionPermissionScopes(),
	})
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":false,"status":"missing","scope":[]},"bot":{"available":true,"verified":true}}}' ;;
scopes) printf '%s' '`+string(scopesJSON)+`' ;;
esac`)
	configurationFixtureFile(t)
	if err := bindRegistrationOperator(runner.DataRoot, "cli_fixture", "ou_fixture"); err != nil {
		t.Fatal(err)
	}
	request, err := writeProgressiveAuthorizationRequest(runner.DataRoot, "cli_fixture", "docs.fixture.read", []string{"docx:document:readonly"})
	if err != nil || request == nil {
		t.Fatal(err)
	}
	evidence, err := ReadConfigurationEvidence(context.Background(), runner, runner.DataRoot)
	if err != nil || evidence.AuthorizationRequest == nil || !contains(evidence.MissingApplicationScopes, "docx:document:readonly") {
		t.Fatalf("requested application scope was not separated: %+v %v", evidence, err)
	}
	if evidence.ApplicationPermissions != "present" {
		t.Fatalf("base bot readiness was conflated with the requested user scope: %+v", evidence)
	}
}

func TestConfigurationReadIgnoresPendingOAuthWhenExistingUserWorks(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"verified":true},"bot":{"available":false}}}' ;;
scopes) printf '{"userScopes":[]}' ;;
esac`)
	configurationFixtureFile(t)
	session := &userAuthSession{status: emptyAuthStatus("pending"), done: make(chan struct{}), configurationStartedAt: time.Now()}
	session.cancel = func() { close(session.done) }
	userAuthSessions.Lock()
	userAuthSessions.items[runner.DataRoot] = session
	userAuthSessions.Unlock()
	evidence, err := ReadConfigurationEvidence(context.Background(), runner, runner.DataRoot)
	if err != nil || evidence.Auth.Status != "authorized" || evidence.Flow == nil || evidence.Flow.State != "pending" {
		t.Fatalf("pending OAuth replaced existing identity: %+v %v", evidence, err)
	}
}

func TestConfigurationScopeAndIdentityRequireEvidence(t *testing.T) {
	if configurationIdentityState(map[string]any{"available": false, "verified": false, "status": "verify_failed"}) != "failed" {
		t.Fatal("verification failure became missing identity")
	}
	for _, value := range []any{nil, 12, map[string]any{}, []any{"read", false}} {
		if configurationScopeState(value, []string{"read"}) != "unknown" {
			t.Fatalf("invalid scope accepted: %#v", value)
		}
	}
	if configurationScopeState([]any{}, []string{"read"}) != "missing" || configurationScopeState([]any{"read"}, []string{"read"}) != "present" {
		t.Fatal("scope evidence classification")
	}
	for _, value := range []map[string]any{nil, {}, {"available": true}, {"available": "true", "verified": true}} {
		if configurationIdentityState(value) != "unknown" {
			t.Fatalf("inferred identity: %#v", value)
		}
	}
}

func TestConfigurationFileChangeDuringCheckInvalidatesEvidence(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":false},"bot":{"available":true,"verified":true}}}' ;;
scopes) printf '\nchanged' >> "$LARKSUITE_CLI_CONFIG_DIR/config.json"; printf '{"userScopes":[]}' ;;
esac`)
	configurationFixtureFile(t)
	evidence, err := ReadConfigurationEvidence(context.Background(), runner, runner.DataRoot)
	if err != nil || evidence.ContextRevision != "" || evidence.ApplicationState != "stale" || evidence.Auth != nil {
		t.Fatalf("mixed contexts accepted: %+v %v", evidence, err)
	}
}

func TestConfigurationFlowCancellationIsScopedAndHidesExpiredQR(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &userAuthSession{status: feishuprotocol.AuthStatus{Status: "pending", VerificationURL: "https://accounts.feishu.cn/fixture", UserCode: "fixture"}, cancel: cancel, done: make(chan struct{}), configurationStartedAt: time.Now()}
	userAuthSessions.Lock()
	userAuthSessions.items[root] = session
	userAuthSessions.Unlock()
	defer func() { userAuthSessions.Lock(); delete(userAuthSessions.items, root); userAuthSessions.Unlock() }()
	flow := ReadConfigurationFlow(root)
	if _, err := CancelConfigurationFlow(root, "wrong", "user"); err == nil || ctx.Err() != nil {
		t.Fatal("wrong flow cancelled current flow")
	}
	go func() { <-ctx.Done(); close(session.done) }()
	if result, err := CancelConfigurationFlow(root, flow.ID, "user"); err != nil || result.State != "cancelled" || ReadConfigurationFlow(root) != nil {
		t.Fatalf("cancel failed: %+v %v", result, err)
	}
	expired := &userAuthSession{status: session.status, done: make(chan struct{}), configurationStartedAt: time.Now().Add(-11 * time.Minute)}
	expired.status.Status = "pending"
	expired.status.UserCode = "expired"
	if result := configurationSessionFlow(expired, "user"); result.State != "expired" || result.UserCode != "" || result.VerificationURL != "" {
		t.Fatalf("expired QR survived: %+v", result)
	}
}

func TestConfigurationLatestCompletedAppIsNotHiddenByOlderOAuth(t *testing.T) {
	root := t.TempDir()
	old := &userAuthSession{status: emptyAuthStatus("completed"), configurationStartedAt: time.Now().Add(-time.Minute), done: make(chan struct{})}
	current := &userAuthSession{status: emptyAuthStatus("completed"), configurationStartedAt: time.Now(), done: make(chan struct{})}
	userAuthSessions.Lock()
	userAuthSessions.items[root] = old
	userAuthSessions.Unlock()
	appConfigurationSessions.Lock()
	appConfigurationSessions.items[root] = &appConfigurationSession{userAuthSession: current}
	appConfigurationSessions.Unlock()
	defer func() {
		userAuthSessions.Lock()
		delete(userAuthSessions.items, root)
		userAuthSessions.Unlock()
		appConfigurationSessions.Lock()
		delete(appConfigurationSessions.items, root)
		appConfigurationSessions.Unlock()
	}()
	if flow := ReadConfigurationFlow(root); flow == nil || flow.Kind != "app" || flow.State != "completed" {
		t.Fatalf("old OAuth hid app completion: %+v", flow)
	}
}
