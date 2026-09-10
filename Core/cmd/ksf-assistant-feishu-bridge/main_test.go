package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/localipc"
)

func TestManagedEventStartupKeepsCLIAndIdentityGates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake CLI fixture")
	}
	for _, mode := range []string{"ready", "wrong-version", "bad-schema", "auth-error", "missing-bot", "wrong-brand", "missing-app", "wrong-auth-profile"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("USERPROFILE", root)
			t.Setenv("LARKSUITE_CLI_CONFIG_DIR", filepath.Join(root, "config"))
			identity := map[string]any{"appId": "cli_fixture", "brand": "feishu", "identities": map[string]any{"bot": map[string]any{"available": mode != "missing-bot"}, "user": map[string]any{"available": false, "status": "missing"}}}
			if mode == "wrong-brand" {
				identity["brand"] = "lark"
			}
			if mode == "missing-app" {
				identity["appId"] = ""
			}
			identityBytes, err := json.Marshal(identity)
			if err != nil {
				t.Fatal(err)
			}
			versionOutput := "lark-cli version " + feishu.PinnedLarkCLIVersion
			if mode == "wrong-version" {
				versionOutput = "lark-cli version 1.0.92"
			}
			schemaOutput := `{"name":"approval approvals get","inputSchema":{"type":"object"}}`
			if mode == "bad-schema" {
				schemaOutput = `{}`
			}
			authExit := "0"
			if mode == "auth-error" {
				authExit = "1"
			}
			logPath := filepath.Join(root, "calls")
			binary := filepath.Join(root, "lark-cli")
			script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$1" in
 --version) printf '%%s\n' '%s'; exit 0 ;;
 schema) printf '%%s\n' '%s'; exit 0 ;;
esac
if [ "$1" = "--profile" ] && [ "$2" = "default" ]; then shift 2; fi
if [ "$1" != "auth" ] || [ "$2" != "status" ]; then exit 2; fi
printf '%%s\n' '%s'
exit %s
`, logPath, versionOutput, schemaOutput, identityBytes, authExit)
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			runner := feishu.CapabilityExecutor{Binary: binary, Profile: "default", DataRoot: root, WorkingDirectory: root}
			if mode == "wrong-auth-profile" {
				runner.Profile = "manual-only"
			}
			client, err := managedMessageClient(context.Background(), runner)
			if mode == "ready" && (err != nil || client == nil) || mode != "ready" && (err == nil || client != nil) {
				t.Fatalf("managed event startup gate %s: client=%v error=%v", mode, client != nil, err)
			}
			calls, err := os.ReadFile(logPath)
			if err != nil || strings.Contains(string(calls), "event ") || strings.Contains(string(calls), "config ") || strings.Contains(string(calls), "login") {
				t.Fatalf("startup probe launched another consumer or changed auth: %s %v", calls, err)
			}
		})
	}
}

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
