package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codexusagebar/core/internal/domain"
)

func TestFeishuClientUsesNativeEntryWithoutNode(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("fixture uses POSIX shell")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "codex-feishu-bridge")
	calls := filepath.Join(root, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + calls + `"
case "$*" in
  "client status") printf '{"pid":{"alive":true},"outbound":{"enabled":true,"dryRun":false},"service":{"running":true,"loaded":true},"eventConsumer":{"profile":"primary","profileValid":true,"desiredConnection":true,"connection":{"state":"connected"}}}\n' ;;
  "client targets list") printf '{"targets":{"messages":[{"alias":"我","taskLinkEligible":true}]}}\n' ;;
  "client task-link protocol") printf '{"protocol":"codex-feishu-task-link-v1","version":2,"readiness":{"ready":true,"blockers":[]}}\n' ;;
  "client task-link list") printf '{"links":[]}\n' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client := FeishuClient{Executable: executable, Node: filepath.Join(root, "missing-node")}
	snapshot, err := client.Inspect(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Availability != "ready" || len(snapshot.TargetAliases) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "client status") {
		t.Fatalf("native client was not used: %s", data)
	}
}

func TestFeishuClientPassesPackagedLarkCLIToNativeClient(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("fixture uses POSIX shell")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "codex-feishu-bridge")
	script := "#!/bin/sh\nprintf '{\"lark\":\"%s\"}\\n' \"$LARK_CLI_BIN\"\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := (FeishuClient{Executable: executable, LarkCLI: "/packaged/lark-cli"}).Permissions(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if result["lark"] != "/packaged/lark-cli" {
		t.Fatalf("native client did not receive lark-cli path: %#v", result)
	}
}

func TestFeishuInspectRequiresActualConnectedState(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("fixture uses POSIX shell")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "codex-feishu-bridge")
	script := `#!/bin/sh
case "$*" in
  "client status") printf '{"pid":{"alive":true},"outbound":{"enabled":true,"dryRun":false},"service":{"running":true},"eventConsumer":{"profile":"primary","profileValid":true,"desiredConnection":true,"connection":{"state":"reconnecting"}}}\n' ;;
  "client targets list") printf '{"targets":{"messages":[]}}\n' ;;
  "client task-link protocol") printf '{"protocol":"codex-feishu-task-link-v1","version":2,"readiness":{"ready":true,"blockers":[]}}\n' ;;
  "client task-link list") printf '{"links":[]}\n' ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := (FeishuClient{Executable: executable}).Inspect(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.InboundConnection {
		t.Fatal("desired connection must not be reported as an actual connection")
	}
}

func TestTaskKeyMatchesBridgeContract(t *testing.T) {
	if value := domain.PublicTaskKey("thread-1"); value != "4b0a5fefc328e6b9257b" {
		t.Fatalf("unexpected task key: %s", value)
	}
}

func TestPrivateQRStaysInsideBridgeDataRoot(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", dataRoot)
	authRoot := filepath.Join(dataRoot, "auth")
	if err := os.MkdirAll(authRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	inside := filepath.Join(authRoot, "user-oauth.png")
	if err := os.WriteFile(inside, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateQR(inside); err != nil {
		t.Fatalf("expected private QR to pass: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "user-oauth.png")
	if err := os.WriteFile(outside, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateQR(outside); err == nil {
		t.Fatal("expected QR outside private data root to be rejected")
	}
}
