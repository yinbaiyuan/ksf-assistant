package feishu

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/userapproval"
)

const registrationURLFixture = "https://open.feishu.cn/page/cli?user_code=ABC-123&lpv=" + PinnedLarkCLIUpstreamVersion + "&ocv=" + PinnedLarkCLIUpstreamVersion + "&from=cli"
const registrationConfigFixture = `{"apps":[{"name":"default","appId":"cli_fixture","appSecret":{"source":"keychain","id":"appsecret:cli_fixture"},"brand":"feishu","lang":"zh_cn","users":[]}]}`

func assertRequiredRegistrationAddons(t *testing.T, value string) {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	encoded := query.Get("addons")
	if encoded == "" || len(query["addons"]) != 1 {
		t.Fatalf("required registration addons missing: %q", value)
	}
	compressed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("registration addons are not base64url: %v", err)
	}
	reader, err := gzip.NewReader(strings.NewReader(string(compressed)))
	if err != nil {
		t.Fatalf("registration addons are not gzip: %v", err)
	}
	payload, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("registration addons cannot be read: %v", err)
	}
	if string(payload) != `{"callbacks":{"items":["card.action.trigger","im.message.receive_v1"]}}` {
		t.Fatalf("unexpected registration addons: %s", payload)
	}
	query.Del("addons")
	parsed.RawQuery = query.Encode()
	original, err := url.Parse(registrationURLFixture)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != original.Scheme || parsed.Host != original.Host || parsed.Path != original.Path || parsed.Query().Encode() != original.Query().Encode() {
		t.Fatalf("registration contract changed beyond addons: %q", value)
	}
}

func TestNativeRegistrationURLIncludesCompleteTaskCardContract(t *testing.T) {
	value, err := nativeRegistrationURL(registrationURLFixture)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(value)
	if parsed.Query().Get("from") != "sdk" || parsed.Query().Get("tp") != "sdk" || parsed.Query().Get("createOnly") != "true" {
		t.Fatalf("SDK registration identity missing: %s", value)
	}
	compressed, err := base64.RawURLEncoding.DecodeString(parsed.Query().Get("addons"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(strings.NewReader(string(compressed)))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var addons struct {
		Scopes struct {
			Tenant []string `json:"tenant"`
		} `json:"scopes"`
		Events struct {
			Items struct {
				Tenant []string `json:"tenant"`
			} `json:"items"`
		} `json:"events"`
		Callbacks struct {
			Items []string `json:"items"`
		} `json:"callbacks"`
	}
	if json.Unmarshal(payload, &addons) != nil || !contains(addons.Events.Items.Tenant, "im.message.receive_v1") || !contains(addons.Callbacks.Items, "card.action.trigger") {
		t.Fatalf("required events or callbacks missing: %s", payload)
	}
	for _, scope := range BaseConnectionPermissionScopes() {
		if !contains(addons.Scopes.Tenant, scope) {
			t.Fatalf("required scope missing: %s", scope)
		}
	}
}

func fakeAppRegistrationCLI(t *testing.T, ending string) CapabilityExecutor {
	t.Helper()
	runner := fakeAuthCLI(t, `case "$1" in
--version) printf 'lark-cli version 1.0.93-ksfassistant.1'; exit 0 ;;
schema) printf '{"name":"approval.approvals.get","inputSchema":{}}'; exit 0 ;;
auth) exit 1 ;;
esac
umask 077
printf '%s\n' "$@" > "$KSF_APP_TEST_ROOT/args"
printf '%s' "$LARKSUITE_CLI_CONFIG_DIR" > "$KSF_APP_TEST_ROOT/stage"
printf 'started\n' >> "$KSF_APP_TEST_ROOT/starts"
printf '  `+registrationURLFixture+`\n' >&2
while [ ! -f "$KSF_APP_TEST_ROOT/complete" ]; do sleep 0.02; done
`+ending)
	t.Setenv("KSF_APP_TEST_ROOT", runner.DataRoot)
	t.Cleanup(func() { CancelAppConfiguration(runner.DataRoot) })
	return runner
}

func completeRegistration(t *testing.T, runner CapabilityExecutor) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(runner.DataRoot, "complete"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	appConfigurationSessions.Lock()
	session := appConfigurationSessions.items[runner.DataRoot]
	appConfigurationSessions.Unlock()
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("registration did not finish")
	}
}

func successfulRegistrationScript() string {
	return `printf '%s' '` + registrationConfigFixture + `' > "$LARKSUITE_CLI_CONFIG_DIR/config.json"
printf '{"appId":"cli_fixture","appSecret":"****","brand":"feishu"}'`
}

func TestAppRegistrationBindsReturnedUserWithoutOAuth(t *testing.T) {
	ending := strings.Replace(successfulRegistrationScript(), `"brand":"feishu"}`, `"brand":"feishu","registrationUser":{"openId":"ou_fixture","tenantBrand":"feishu"}}`, 1)
	runner := fakeAppRegistrationCLI(t, ending)
	if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true); err != nil {
		t.Fatal(err)
	}
	completeRegistration(t, runner)
	result, err := FinishAppConfiguration(context.Background(), runner, runner.DataRoot)
	if err != nil || result["operatorBound"] != true || result["operatorAuthorizationRequired"] != false {
		t.Fatalf("one-scan binding failed: %#v %v", result, err)
	}
	config, err := NewClientConfigStore(runner.DataRoot).Load()
	if err != nil || config.Operator == nil || config.Operator.AppID != "cli_fixture" || config.Operator.OpenID != "ou_fixture" || config.MessageTargets["我"].ID != "ou_fixture" {
		t.Fatalf("app-bound operator missing: %#v %v", config, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "ou_fixture") {
		t.Fatal("raw registration identity crossed the bridge result")
	}
}

func TestAppRegistrationStagesThenPublishesOnce(t *testing.T) {
	runner := fakeAppRegistrationCLI(t, successfulRegistrationScript())
	status, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true)
	if err != nil || status["status"] != "pending" || status["flow"] != "app-create" {
		t.Fatalf("start=%#v err=%v", status, err)
	}
	verificationURL, ok := status["verificationUrl"].(string)
	if !ok {
		t.Fatalf("verification URL missing: %#v", status)
	}
	assertRequiredRegistrationAddons(t, verificationURL)
	args, err := os.ReadFile(filepath.Join(runner.DataRoot, "args"))
	if err != nil || strings.Contains("\n"+string(args), "\n--json\n") {
		t.Fatalf("config init used an unsupported JSON flag: %q (%v)", args, err)
	}
	destination := filepath.Join(os.Getenv("LARKSUITE_CLI_CONFIG_DIR"), "config.json")
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("published before registration completed")
	}
	if release, err := userapproval.TryExecutionLease(runner.DataRoot); err == nil {
		release()
		t.Fatal("registration did not protect authorization context")
	}
	pending, err := FinishAppConfiguration(context.Background(), runner, runner.DataRoot)
	if err != nil || pending["status"] != "pending" {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true); err != nil {
		t.Fatal(err)
	}
	starts, _ := os.ReadFile(filepath.Join(runner.DataRoot, "starts"))
	if string(starts) != "started\n" {
		t.Fatal("duplicate start created another process")
	}
	stage, _ := os.ReadFile(filepath.Join(runner.DataRoot, "stage"))
	canonical, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil || filepath.Dir(string(stage)) != canonical || string(stage) == canonical {
		t.Fatal("CLI configuration was not isolated")
	}
	completeRegistration(t, runner)
	result, err := FinishAppConfiguration(context.Background(), runner, runner.DataRoot)
	if err != nil || result["status"] != "completed" || result["profileValid"] != true || result["identityValid"] != false || result["verificationUrl"] != nil {
		t.Fatalf("finish=%#v err=%v", result, err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != registrationConfigFixture {
		t.Fatalf("config was not preserved: %v", err)
	}
	if _, err := os.Stat(string(stage)); !os.IsNotExist(err) {
		t.Fatal("successful stage was retained")
	}
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := os.WriteFile(destination, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinishAppConfiguration(context.Background(), runner, runner.DataRoot); err == nil {
		t.Fatal("changed identity was reported as completed")
	}
}

func TestAppRegistrationCancellationDoesNotPublish(t *testing.T) {
	runner := fakeAppRegistrationCLI(t, successfulRegistrationScript())
	if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true); err != nil {
		t.Fatal(err)
	}
	CancelAppConfiguration(runner.DataRoot)
	if _, err := FinishAppConfiguration(context.Background(), runner, runner.DataRoot); err == nil {
		t.Fatal("cancelled session survived")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("LARKSUITE_CLI_CONFIG_DIR"), "config.json")); !os.IsNotExist(err) {
		t.Fatal("cancelled registration published config")
	}
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestAppRegistrationFailureAndConcurrentConfigDoNotOverwrite(t *testing.T) {
	for _, mode := range []string{"exit", "secret", "concurrent", "business-enabled"} {
		t.Run(mode, func(t *testing.T) {
			ending := successfulRegistrationScript()
			if mode == "exit" {
				ending += "\nexit 1"
			}
			if mode == "secret" {
				ending = strings.ReplaceAll(ending, `"appSecret":"****"`, `"appSecret":"private-secret"`)
			}
			runner := fakeAppRegistrationCLI(t, ending)
			if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(os.Getenv("LARKSUITE_CLI_CONFIG_DIR"), "config.json")
			if mode == "concurrent" {
				if err := os.WriteFile(destination, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "business-enabled" {
				settings := DefaultSettings()
				settings.Group.Enabled = true
				if err := NewSettingsStore(runner.DataRoot).Save(settings); err != nil {
					t.Fatal(err)
				}
			}
			completeRegistration(t, runner)
			result, err := FinishAppConfiguration(context.Background(), runner, runner.DataRoot)
			if err == nil || result["status"] != "failed" || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("false success or secret error: %#v %v", result, err)
			}
			if mode == "concurrent" {
				data, _ := os.ReadFile(destination)
				if string(data) != "existing" {
					t.Fatal("existing configuration overwritten")
				}
			} else if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("failed process published config")
			}
			stage, _ := os.ReadFile(filepath.Join(runner.DataRoot, "stage"))
			if _, err := os.Stat(filepath.Join(string(stage), "config.json")); err != nil {
				t.Fatal("unknown result lost official recovery config")
			}
		})
	}
}

func TestAppRegistrationRefusesExistingBusinessChannels(t *testing.T) {
	runner := fakeAppRegistrationCLI(t, successfulRegistrationScript())
	settings := DefaultSettings()
	settings.Group.Enabled = true
	if err := NewSettingsStore(runner.DataRoot).Save(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true); err == nil {
		t.Fatal("existing business channel admitted app creation")
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "starts")); !os.IsNotExist(err) {
		t.Fatal("registration started despite existing business state")
	}
}

func TestAppRegistrationPreSubmissionFailureIsExplicitAndRecoverable(t *testing.T) {
	root := t.TempDir()
	runner := CapabilityExecutor{Binary: filepath.Join(root, "missing-lark-cli"), Profile: "default", DataRoot: root, WorkingDirectory: root}
	_, err := StartAppConfiguration(context.Background(), runner, root, "default", true)
	if err == nil || !IsAppConfigurationNotStarted(err) {
		t.Fatalf("pre-submission failure was ambiguous: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "user-authorization-execution-v1.lock")); !os.IsNotExist(statErr) {
		t.Fatal("failed preflight crossed the execution lease boundary")
	}
}

func TestAppRegistrationFailureBeforeVerificationURLIsRetryable(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$1" in
--version) printf 'lark-cli version 1.0.93-ksfassistant.1'; exit 0 ;;
schema) printf '{"name":"approval.approvals.get","inputSchema":{}}'; exit 0 ;;
*) exit 1 ;;
esac`)
	_, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true)
	if err == nil || !IsAppConfigurationNotStarted(err) {
		t.Fatalf("failure before a user-visible verification URL was treated as possibly submitted: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(runner.DataRoot, "user-authorization-execution-v1.lock")); statErr != nil {
		t.Fatalf("fixture did not cross the local process boundary: %v", statErr)
	}
}

func TestAppRegistrationPreservesInactiveHistoricalWork(t *testing.T) {
	for _, relative := range []string{"integration-events-v1.json", "private-cache/inbound-work/old.json", "private-cache/workbox-v3/outbox/pending/old.json", "private-cache/workbox-v3/docbox/pending/old.json", "logs/docbox.jsonl", "integration-event-receipts-v1/old.json"} {
		t.Run(relative, func(t *testing.T) {
			runner := fakeAppRegistrationCLI(t, successfulRegistrationScript())
			path := filepath.Join(runner.DataRoot, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("historical-fact"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true); err != nil {
				t.Fatalf("inactive history blocked a fresh isolated connection: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "historical-fact" {
				t.Fatal("registration modified historical state")
			}
		})
	}
}

func TestReuseRequiresActualFeishuApplication(t *testing.T) {
	for _, body := range []string{
		`printf '{"identities":{"user":{"available":false}}}'`,
		`printf '{"appId":"cli_fixture","brand":"lark","identities":{"user":{"available":true}}}'`,
	} {
		t.Run(body, func(t *testing.T) {
			runner := fakeAuthCLI(t, body)
			if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", false); err == nil {
				t.Fatal("missing or wrong application accepted for reuse")
			}
		})
	}
}

func TestAppRegistrationURLAndResultContracts(t *testing.T) {
	if !validAppRegistrationURL(registrationURLFixture) {
		t.Fatal("fixed upstream protocol URL rejected")
	}
	for _, value := range []string{
		strings.Replace(registrationURLFixture, "open.feishu.cn", "open.feishu.cn.evil.test", 1),
		registrationURLFixture + "&device_code=secret",
		registrationURLFixture + "&addons=untrusted",
		registrationURLFixture + "&user_code=OTHER",
		strings.ReplaceAll(registrationURLFixture, PinnedLarkCLIUpstreamVersion, PinnedLarkCLIVersion),
		strings.ReplaceAll(registrationURLFixture, PinnedLarkCLIUpstreamVersion, "1.0.94"),
		strings.Replace(registrationURLFixture, "/page/cli", "/redirect", 1),
	} {
		if validAppRegistrationURL(value) {
			t.Fatal("unsafe URL accepted")
		}
	}
	first, err := appRegistrationURLWithRequiredAddons(registrationURLFixture)
	if err != nil {
		t.Fatal(err)
	}
	second, err := appRegistrationURLWithRequiredAddons(registrationURLFixture)
	if err != nil || first != second {
		t.Fatal("registration addons are not deterministic")
	}
	assertRequiredRegistrationAddons(t, first)
	if _, err := appRegistrationURLWithRequiredAddons("https://open.feishu.cn/page/cli?user_code=untrusted"); err == nil {
		t.Fatal("invalid upstream registration URL was augmented")
	}
	for _, value := range []string{`null`, `{}`, `{"appId":"cli_fixture","appSecret":"secret","brand":"feishu"}`, `{"appId":"cli_fixture","appSecret":"****","brand":"lark"}`} {
		if _, valid := registrationResult([]byte(value)); valid {
			t.Fatal("unsafe result accepted")
		}
	}
}

func TestAppRegistrationOutputIsBoundedAndStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &userAuthSession{status: emptyAuthStatus("pending"), cancel: cancel, ready: make(chan struct{})}
	output := &appRegistrationOutput{session: session}
	for _, chunk := range []string{"QR console art\n  https://open.", strings.TrimPrefix(registrationURLFixture, "https://open.") + "\n"} {
		if _, err := output.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	assertRequiredRegistrationAddons(t, session.snapshot().VerificationURL)
	if _, err := output.Write(make([]byte, maximumAuthOutputBytes)); err == nil || ctx.Err() == nil {
		t.Fatal("overflow did not cancel process")
	}
	encoded, _ := json.Marshal(session.snapshot())
	if strings.Contains(string(encoded), "device_code") {
		t.Fatal("intermediate credential exposed")
	}
}
