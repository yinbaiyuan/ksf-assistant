package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/localipc"
)

func TestClientAndDaemonVersionAgree(t *testing.T) {
	if feishucli.Version != version {
		t.Fatalf("client version=%s daemon=%s", feishucli.Version, version)
	}
}

func TestClientEntryDoesNotLoadSettingsOrFallBackToLocalExecution(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", root)
	path := filepath.Join(root, feishu.SettingsFilename)
	if err := os.WriteFile(path, []byte("deliberately invalid settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"status", "snapshot", "doctor"} {
		err := run([]string{"client", command})
		if !errors.Is(err, localipc.ErrNotRunning) || !strings.Contains(err.Error(), "service unavailable") {
			t.Fatalf("%s should fail at the gateway, not load settings: %v", command, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != feishu.SettingsFilename {
		t.Fatalf("client created local state: entries=%v err=%v", entries, err)
	}
}
