package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFixedBotRejectsIdentityOverridesAndOtherCommands(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation fixedBotOperation
		args      []string
	}{
		{"user_override", fixedBotResourceDownload, []string{"im", "+messages-resources-download", "--as", "user"}},
		{"bot_override", fixedBotResourceDownload, []string{"im", "+messages-resources-download", "--as", "bot"}},
		{"duplicate", fixedBotResourceDownload, []string{"im", "+messages-resources-download", "--message-id", "om_fixture", "--message-id", "om_other"}},
		{"other_command", fixedBotResourceDownload, []string{"mail", "+message-modify"}},
		{"other_operation", fixedBotOperation(99), []string{"docs", "+create"}},
		{"other_api", fixedBotResourceDownload, []string{"api", "POST", "/open-apis/im/v1/messages", "--data", "-"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			if _, err := runner.runFixedBotBusiness(context.Background(), test.operation, test.args, []byte("body"), time.Second); err == nil {
				t.Fatal("invalid fixed invocation executed")
			}
			if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) || *requests != 0 {
				t.Fatal("invalid invocation reached business process or approval")
			}
		})
	}
}

func TestFixedBotResourceDownloadKeepsBotIdentity(t *testing.T) {
	runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "cat >/dev/null", "printf '%s\\n' \"$*\" >> '"+logPath+".args'; while [ $# -gt 0 ]; do if [ \"$1\" = '--output' ]; then shift; mkdir -p \"$(dirname \"$1\")\"; printf 'resource' > \"$1\"; fi; shift; done; cat >/dev/null", 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	if err := runner.DownloadMessageResource(context.Background(), "om_fixture", "file_fixture", "file", filepath.Join(runner.DataRoot, "resource"), time.Second); err != nil {
		t.Fatal(err)
	}
	resource, err := os.ReadFile(filepath.Join(runner.DataRoot, "resource"))
	if err != nil || string(resource) != "resource" {
		t.Fatalf("materialized resource did not return to private destination: %q %v", resource, err)
	}
	args, err := os.ReadFile(logPath + ".args")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 1 || *requests != 0 {
		t.Fatalf("calls=%q approvals=%d", args, *requests)
	}
	for _, line := range lines {
		if strings.Count(line, "--as bot") != 1 || strings.Contains(line, "--as user") {
			t.Fatalf("identity not fixed: %s", line)
		}
	}
}
