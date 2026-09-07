package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/capabilitypolicy"
	"ksfassistant/core/internal/userapproval"
)

func fakeAuthCLI(t *testing.T, body string) CapabilityExecutor {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake process fixture")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "fake-lark")
	// Streaming-login fixtures include the successful application preflight.
	if strings.Contains(body, "login") && !strings.Contains(body, "\nscopes)") {
		contract, err := RequiredPermissionScopes()
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"appId": "cli_fixture", "brand": "feishu", "tokenType": "user", "userScopes": contract.User})
		body = "if [ \"$4\" = scopes ]; then printf '%s' '" + string(payload) + "'; exit; fi\n" + body
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LARKSUITE_CLI_CONFIG_DIR", filepath.Join(root, "config"))
	t.Cleanup(func() { CancelUserAuthFlow(root) })
	return CapabilityExecutor{Binary: binary, Profile: "default", DataRoot: root, WorkingDirectory: root}
}

func TestRunAuthJSONBoundsAndSanitizesResponses(t *testing.T) {
	for _, body := range []string{
		`echo 'private access_token=secret' >&2; exit 1`,
		`printf 'not-json private-secret'`,
		`printf 'null'`,
		`printf '{"ok":false,"error":"private-secret"}'`,
		`printf '{"identities":{"user":{"available":true}},"access_token":"private-secret"}'`,
		`head -c 300000 /dev/zero`,
	} {
		t.Run(body[:min(24, len(body))], func(t *testing.T) {
			runner := fakeAuthCLI(t, body)
			_, err := runner.RunAuthJSON(context.Background(), []string{"auth", "status", "--verify", "--json"}, nil, time.Second)
			if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe result: %v", err)
			}
		})
	}
}

func TestAuthMutationsRespectExecutionLeaseButStatusRemainsReadable(t *testing.T) {
	runner := fakeAuthCLI(t, `printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true}}}'`)
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, request := range []struct {
		args  []string
		input []byte
	}{
		{[]string{"auth", "logout", "--json"}, nil},
		{[]string{"config", "init", "--name", "default", "--app-id", "cli_fixture", "--app-secret-stdin", "--json"}, []byte("fixture-secret")},
	} {
		if _, err := runner.RunAuthJSON(context.Background(), request.args, request.input, time.Second); err == nil || err.Error() != "approval_authorization_busy" {
			t.Fatalf("auth mutation crossed lease: %v", err)
		}
	}
	if _, err := runner.RunAuthJSON(context.Background(), []string{"auth", "status", "--json"}, nil, time.Second); err != nil {
		t.Fatalf("status blocked by execution lease: %v", err)
	}
}

func TestAuthSessionOwnsAndReleasesAuthorizationLease(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":false}}}' ;;
login) printf '{"event":"device_authorization","verification_uri_complete":"https://accounts.feishu.cn/oauth?user_code=ABC-123","user_code":"ABC-123"}\n'; while :; do sleep 0.02; done ;;
*) printf '{}' ;;
esac`)
	if _, err := StartUserAuth(context.Background(), runner, runner.DataRoot, "required"); err != nil {
		t.Fatal(err)
	}
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err == nil {
		release()
		t.Fatal("login session did not own authorization lease")
	}
	if err.Error() != "approval_authorization_busy" {
		t.Fatal(err)
	}
	CancelUserAuthFlow(runner.DataRoot)
	release, err = userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatalf("cancelled login retained lease: %v", err)
	}
	release()
}

func TestFailedAuthSessionReleasesAuthorizationLease(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":false}}}' ;;
*) exit 1 ;;
esac`)
	if _, err := StartUserAuth(context.Background(), runner, runner.DataRoot, "required"); err == nil {
		t.Fatal("failed login was accepted")
	}
	CancelUserAuthFlow(runner.DataRoot)
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatalf("failed login retained lease: %v", err)
	}
	release()
}

func TestRunAuthJSONPreservesOfficialIdentityAndRejectsCredentialArguments(t *testing.T) {
	runner := fakeAuthCLI(t, `printf '{"appId":"cli_fixture","identities":{"bot":{"available":true,"verified":true},"user":{"available":false}}}'`)
	result, err := runner.RunAuthJSON(context.Background(), []string{"auth", "status", "--verify", "--json"}, nil, 5*time.Second)
	if err != nil || result["appId"] != "cli_fixture" {
		t.Fatalf("official identity lost: %#v %v", result, err)
	}
	for _, args := range [][]string{
		{"auth", "login", "--device-code", "private-secret", "--json"},
		{"auth", "status", "--token", "private-secret", "--json"},
		{"auth", "status", "--profile", "other", "--json"},
	} {
		if _, err := runner.RunAuthJSON(context.Background(), args, nil, time.Second); err == nil {
			t.Fatal("credential/override arguments admitted")
		}
	}
}

func TestConfigureExistingAppUsesOnlyOfficialSecretStdin(t *testing.T) {
	runner := fakeAuthCLI(t, `printf '%s\n' "$@" > args; cat > stdin; printf '{"appId":"cli_fixture","appSecret":"****","brand":"feishu"}'`)
	result, err := ConfigureExistingApp(context.Background(), runner, "cli_fixture", "private-secret", "feishu", "default")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(runner.DataRoot, "args"))
	stdin, _ := os.ReadFile(filepath.Join(runner.DataRoot, "stdin"))
	if strings.Contains(string(args), "private-secret") || !strings.Contains(string(args), "--app-secret-stdin") || string(stdin) != "private-secret\n" {
		t.Fatal("secret boundary failed")
	}
	if _, exists := result["sdkCredential"]; exists {
		t.Fatal("SDK credential copy still created")
	}
}

func TestAuthStatusSeparatesIdentityFromPermissionCompleteness(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identity":"user","verified":true,"identities":{"user":{"available":true,"verified":true,"scope":"im:message"}}}' ;;
scopes) printf '{"userScopes":[]}' ;;
esac`)
	status, err := ReadAuthStatus(context.Background(), runner, runner.DataRoot)
	if err != nil || status.Status != "authorized" || !status.IdentityValid || !status.ProfileValid || len(status.MissingCapabilities) == 0 {
		t.Fatalf("authorization confused with scopes: %+v %v", status, err)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "im:message") {
		t.Fatal("raw scope leaked to desktop")
	}
}

func TestAuthSessionUsesStreamingLoginAndNoSecretPersistence(t *testing.T) {
	runner := fakeAuthCLI(t, `printf '%s\n' "$@" >> args
case "$4" in
login) printf '{"event":"device_authorization","verification_uri_complete":"https://accounts.feishu.cn/oauth?user_code=ABC-123","user_code":"ABC-123"}\n'; while [ ! -f complete ]; do sleep 0.02; done; printf '{"event":"authorization_complete","user_open_id":"ou_private","scope":"contact:user.base:readonly"}\n' ;;
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"verified":true,"scope":"contact:user.base:readonly"}}}' ;;
oldscopes) printf '{"userScopes":[]}' ;;
*) exit 1 ;;
esac`)
	if err := capabilitypolicy.SignOut(runner.DataRoot); err != nil {
		t.Fatal(err)
	}
	start, err := StartUserAuth(context.Background(), runner, runner.DataRoot, "required")
	if err != nil || start["status"] != "pending" || start["userCode"] != "ABC-123" {
		t.Fatalf("start: %#v %v", start, err)
	}
	pending, err := FinishUserAuthFlow(context.Background(), runner, runner.DataRoot, "")
	if err != nil || pending["status"] != "pending" {
		t.Fatalf("pending flow claimed success: %#v %v", pending, err)
	}
	if err := os.WriteFile(filepath.Join(runner.DataRoot, "complete"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-currentUserAuthSession(runner.DataRoot).done:
	case <-time.After(3 * time.Second):
		t.Fatal("fake authorization did not complete")
	}
	if capabilitypolicy.CheckSession(runner.DataRoot) != nil {
		t.Fatal("early check left completed OAuth signed out")
	}
	finish, err := FinishUserAuthFlow(context.Background(), runner, runner.DataRoot, "")
	if err != nil || finish["status"] != "authorized" {
		t.Fatalf("finish: %#v %v", finish, err)
	}
	encoded, _ := json.Marshal(finish)
	args, _ := os.ReadFile(filepath.Join(runner.DataRoot, "args"))
	for _, forbidden := range []string{"deviceCode", "device_code", "statePath", "ou_private", "--device-code", "--no-wait"} {
		if strings.Contains(string(encoded)+string(args), forbidden) {
			t.Fatalf("leaked %s", forbidden)
		}
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "auth", "user-oauth.json")); !os.IsNotExist(err) {
		t.Fatal("persisted user auth session")
	}
}

func TestAuthSessionCancelAndLegacyContinuationFailClosed(t *testing.T) {
	runner := fakeAuthCLI(t, `if [ "$4" = status ]; then printf '{"brand":"feishu","appId":"cli_fixture","identities":{"user":{"available":false}}}'; elif [ "$4" = login ]; then printf '{"event":"device_authorization","verification_uri_complete":"https://accounts.feishu.cn/authorize","user_code":"ABC"}\n'; sleep 30; else exit 1; fi`)
	_, err := StartUserAuth(context.Background(), runner, runner.DataRoot, "required")
	if err != nil {
		t.Fatal(err)
	}
	session := currentUserAuthSession(runner.DataRoot)
	CancelUserAuthFlow(runner.DataRoot)
	select {
	case <-session.done:
	default:
		t.Fatal("cancel did not reap process")
	}
	if currentUserAuthSession(runner.DataRoot) != nil {
		t.Fatal("canceled session retained")
	}
	if _, err := FinishUserAuthFlow(context.Background(), runner, runner.DataRoot, "private-secret"); err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal("legacy continuation admitted or leaked")
	}
	if _, err := FinishUserAuthFlow(context.Background(), runner, runner.DataRoot, ""); err == nil {
		t.Fatal("restart silently restored old session")
	}
}

func TestAuthEventParserRejectsUntrustedURLsSecretsAndFalseSuccess(t *testing.T) {
	for _, input := range []string{
		`{"event":"device_authorization","verification_uri_complete":"https://accounts.feishu.cn/x?access_token=private-secret","user_code":"ABC"}`,
		`{"event":"device_authorization","verification_uri_complete":"https://feishu.cn.evil.test/x","user_code":"ABC"}`,
		`{"event":"device_authorization","verification_uri_complete":"https://feishu.cn/x","user_code":"ABC","device_code":"private"}`,
		`{"event":"authorization_complete"}`,
		`{"event":"authorization_failed","error":"private-secret"}`,
	} {
		session := &userAuthSession{status: emptyAuthStatus("pending"), ready: make(chan struct{})}
		if complete, err := session.readEvents(strings.NewReader(input)); err == nil || complete || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe event: %v %v", complete, err)
		}
	}
}

func TestAppCreationPreservesExistingConfigurationBeforeProcessStart(t *testing.T) {
	runner := fakeAuthCLI(t, `touch unexpected-process`)
	if err := os.MkdirAll(os.Getenv("LARKSUITE_CLI_CONFIG_DIR"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("LARKSUITE_CLI_CONFIG_DIR"), "config.json"), []byte("existing-or-malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true)
	if err == nil || !strings.Contains(err.Error(), "不会覆盖") {
		t.Fatalf("automatic app creation admitted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "unexpected-process")); !os.IsNotExist(err) {
		t.Fatal("automatic app creation spawned a process")
	}
}

func TestAuthSessionCompletionRequiresCleanExitAndNoSecretOutput(t *testing.T) {
	for _, ending := range []string{
		`printf '{"event":"authorization_complete","access_token":"private-secret"}\n'`,
		`printf '{"event":"authorization_complete"}\n'; echo private-secret >&2; exit 1`,
	} {
		t.Run(ending[:min(32, len(ending))], func(t *testing.T) {
			runner := fakeAuthCLI(t, `if [ "$4" = status ]; then printf '{"brand":"feishu","appId":"cli_fixture","identities":{"user":{"available":false}}}'; exit 0; fi
if [ "$4" != login ]; then exit 1; fi
printf '{"event":"device_authorization","verification_uri_complete":"https://accounts.feishu.cn/authorize","user_code":"ABC"}\n'
`+ending)
			_, _ = StartUserAuth(context.Background(), runner, runner.DataRoot, "required")
			session := currentUserAuthSession(runner.DataRoot)
			select {
			case <-session.done:
			case <-time.After(3 * time.Second):
				t.Fatal("child Wait deadlocked")
			}
			result, err := FinishUserAuthFlow(context.Background(), runner, runner.DataRoot, "")
			encoded, _ := json.Marshal(result)
			if err == nil || strings.Contains(err.Error()+string(encoded), "private-secret") || result["status"] == "authorized" {
				t.Fatalf("unsafe completion: %#v %v", result, err)
			}
		})
	}
}

func TestAuthSessionHasTenMinuteCeilingAndCanceledStartReapsChild(t *testing.T) {
	if maximumAuthSessionDuration != 10*time.Minute {
		t.Fatal("auth session lifetime contract changed")
	}
	runner := fakeAuthCLI(t, `if [ "$4" = status ]; then printf '{"brand":"feishu","appId":"cli_fixture","identities":{"user":{"available":false}}}'; elif [ "$4" = login ]; then sleep 30; fi`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := StartUserAuth(ctx, runner, runner.DataRoot, "required")
		result <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	var session *userAuthSession
	for session == nil && time.Now().Before(deadline) {
		session = currentUserAuthSession(runner.DataRoot)
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled start succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled start did not return")
	}
	if session == nil {
		t.Fatal("fake session never started")
	}
	select {
	case <-session.done:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled child Wait deadlocked")
	}
}

func TestAuthProfileMustMatchManagedFeishuToolchain(t *testing.T) {
	runner := fakeAuthCLI(t, `if [ "$4" = scopes ]; then printf '{"userScopes":[]}'; else printf '{"appId":"cli_fixture","brand":"lark","identities":{"user":{"available":true,"verified":true}}}'; fi`)
	status, err := ReadAuthStatus(context.Background(), runner, runner.DataRoot)
	if err != nil || status.ProfileValid || status.Status == "authorized" {
		t.Fatalf("wrong brand accepted: %+v %v", status, err)
	}
	if _, err := ConfigureExistingApp(context.Background(), runner, "cli_fixture", "private-secret", "lark", "default"); err == nil {
		t.Fatal("non-Feishu configuration admitted")
	}
	if _, err := StartUserAuth(context.Background(), runner, runner.DataRoot, "required"); err == nil {
		t.Fatal("non-Feishu authorization admitted")
	}
	for _, expected := range []string{"LARKSUITE_CLI_PROFILE=default", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1", "LARKSUITE_CLI_REMOTE_META=off"} {
		if !contains(authEnvironment(), expected) {
			t.Fatalf("missing managed environment %s", expected)
		}
	}
}

func TestAuthorizationPreflightDistinguishesApplicationGapsFromUnknownEvidence(t *testing.T) {
	for _, test := range []struct{ payload, code string }{
		{`{}`, "application_permissions_unverified"},
		{`{"appId":"cli_other","brand":"feishu","tokenType":"user","userScopes":[]}`, "application_permissions_unverified"},
		{`{"appId":"cli_fixture","brand":"feishu","tokenType":"user","userScopes":null}`, "application_permissions_unverified"},
		{`{"appId":"cli_fixture","brand":"feishu","tokenType":"user","userScopes":[]}`, "application_permissions_missing"},
	} {
		runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":false}}}' ;;
scopes) printf '%s' '`+test.payload+`' ;;
login) touch unexpected-login; exit 1 ;;
esac`)
		_, err := StartUserAuth(context.Background(), runner, runner.DataRoot, "required")
		if err == nil || err.Error() != test.code {
			t.Fatalf("wrong preflight: %v want %s", err, test.code)
		}
		if _, err := os.Stat(filepath.Join(runner.DataRoot, "unexpected-login")); !os.IsNotExist(err) {
			t.Fatal("invalid application preflight started OAuth")
		}
	}
}
