package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/userapproval"
)

const registrationURLFixture = "https://open.feishu.cn/page/cli?user_code=ABC-123&lpv=1.0.93&ocv=1.0.93&from=cli"
const registrationConfigFixture = `{"apps":[{"name":"default","appId":"cli_fixture","appSecret":{"source":"keychain","id":"appsecret:cli_fixture"},"brand":"feishu","lang":"zh_cn","users":[]}]}`

func fakeAppRegistrationCLI(t *testing.T, ending string) CapabilityExecutor {
	t.Helper()
	runner := fakeAuthCLI(t, `case "$1" in
--version) printf 'lark-cli version 1.0.93'; exit 0 ;;
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

func TestAppRegistrationStagesThenPublishesOnce(t *testing.T) {
	runner := fakeAppRegistrationCLI(t, successfulRegistrationScript())
	status, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true)
	if err != nil || status["status"] != "pending" || status["verificationUrl"] != registrationURLFixture || status["flow"] != "app-create" {
		t.Fatalf("start=%#v err=%v", status, err)
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

func TestAppRegistrationRefusesHistoricalBindingsAndWork(t *testing.T) {
	for _, relative := range []string{"client.json", "task-links-v1.json", "integration-events-v1.json", "private-cache/inbound-work/old.json", "private-cache/workbox-v3/outbox/pending/old.json", "private-cache/workbox-v3/docbox/pending/old.json", "logs/docbox.jsonl", "integration-event-receipts-v1/old.json"} {
		t.Run(relative, func(t *testing.T) {
			runner := fakeAppRegistrationCLI(t, successfulRegistrationScript())
			path := filepath.Join(runner.DataRoot, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("historical-fact"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := StartAppConfiguration(context.Background(), runner, runner.DataRoot, "default", true); err == nil {
				t.Fatal("historical business state admitted new application")
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
		t.Fatal("fixed upstream URL rejected")
	}
	for _, value := range []string{
		strings.Replace(registrationURLFixture, "open.feishu.cn", "open.feishu.cn.evil.test", 1),
		registrationURLFixture + "&device_code=secret",
		registrationURLFixture + "&user_code=OTHER",
		strings.ReplaceAll(registrationURLFixture, "1.0.93", "1.0.94"),
		strings.Replace(registrationURLFixture, "/page/cli", "/redirect", 1),
	} {
		if validAppRegistrationURL(value) {
			t.Fatal("unsafe URL accepted")
		}
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
	if session.snapshot().VerificationURL != registrationURLFixture {
		t.Fatal("split URL not reconstructed")
	}
	if _, err := output.Write(make([]byte, maximumAuthOutputBytes)); err == nil || ctx.Err() == nil {
		t.Fatal("overflow did not cancel process")
	}
	encoded, _ := json.Marshal(session.snapshot())
	if strings.Contains(string(encoded), "device_code") {
		t.Fatal("intermediate credential exposed")
	}
}
