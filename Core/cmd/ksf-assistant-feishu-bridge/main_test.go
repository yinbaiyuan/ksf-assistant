package main

import (
	"os"
	"path/filepath"
	"testing"

	"ksfassistant/core/internal/feishu"
)

func TestRetiredClientEntryIsRejectedWithoutCreatingState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", root)
	path := filepath.Join(root, feishu.SettingsFilename)
	if err := os.WriteFile(path, []byte("deliberately invalid settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"client", "status"}); err == nil || err.Error() != "unsupported ksf-assistant-feishu-bridge argument" {
		t.Fatalf("retired client entry was not rejected: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != feishu.SettingsFilename {
		t.Fatalf("client created local state: entries=%v err=%v", entries, err)
	}
}
