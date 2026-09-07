package feishucommands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
)

func TestLegacyAuthRequiresDesktopBeforeAnyExecutorOrStorage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	for _, action := range []string{"start-config", "start-user", "finish-user", "ensure-current-user"} {
		_, err := Execute(context.Background(), root, nil, feishucli.Request{Command: "auth", Action: action})
		if err == nil || !strings.Contains(err.Error(), "configuration_desktop_required") {
			t.Fatalf("legacy auth admitted %s: %v", action, err)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("rejected auth touched storage")
	}
}

func TestTargetsCannotRebindOrDeleteAuthorizedOperator(t *testing.T) {
	root := t.TempDir()
	store := feishu.NewClientConfigStore(root)
	config := feishu.DefaultClientConfig()
	config.DirectAllowedAliases = []string{"operator"}
	config.MessageTargets["operator"] = feishu.MessageTarget{Type: "open_id", ID: "ou_original"}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	capability := feishu.NewCapabilityService(root, &recordingExecutor{}, nil)
	for _, args := range [][]string{{"targets", "remove", "message", "operator"}, {"targets", "set", "message", "operator", "--type", "open_id", "--value-file", "-"}} {
		request, err := feishucli.Parse(args, strings.NewReader("ou_replacement"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Execute(context.Background(), root, capability, request); err == nil || !strings.Contains(err.Error(), "configuration_desktop_required") {
			t.Fatalf("operator target mutated: %v", err)
		}
	}
	after, err := os.ReadFile(store.Path())
	if err != nil || string(after) != string(before) {
		t.Fatal("operator policy changed")
	}
}
