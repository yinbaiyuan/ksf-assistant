package feishu

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeEventCLI(t *testing.T) CapabilityExecutor {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX lifecycle fixture")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "lark-cli")
	script := `#!/bin/sh
root='ROOT'
if [ "$1" = "--profile" ]; then shift 2; fi
if [ "$1" != "event" ]; then exit 2; fi
case "$2" in
 status)
  if [ ! -f "$root/bus" ]; then printf '{"apps":[{"status":"not_running"}]}\n'; exit 0; fi
  count=0
  consumers=""
  for file in "$root/"*.pid; do
   [ -f "$file" ] || continue
   pid=$(cat "$file")
   name=$(basename "$file" .pid)
   if [ "$count" -gt 0 ]; then consumers="$consumers,"; fi
   consumers="$consumers{\"pid\":$pid,\"event_key\":\"$name\",\"dropped\":0}"
   count=$((count+1))
  done
  printf '{"apps":[{"status":"running","pid":999,"active_consumers":%s,"consumers":[%s]}]}\n' "$count" "$consumers"
  ;;
 consume)
  key="$3"
  : > "$root/bus"
  printf '%s' "$$" > "$root/$key.pid"
  trap 'rm -f "$root/$key.pid"' EXIT
  printf '[event] ready event_key=%s\n' "$key" >&2
  read ignored
  exit 0
  ;;
 stop)
  printf stopped > "$root/stopped"
  rm -f "$root/bus"
  printf '{"results":[{"status":"stopped"}]}\n'
  ;;
 *) exit 2 ;;
esac
`
	script = strings.ReplaceAll(script, "ROOT", root)
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return CapabilityExecutor{Binary: binary, Profile: "default", DataRoot: root, WorkingDirectory: root}
}

func TestCLIInboundOwnsConsumersAndClosesThroughStdin(t *testing.T) {
	runner := fakeEventCLI(t)
	connected := make(chan struct{}, 1)
	inbound, _ := NewOfficialInbound(runner, nil, func(context.Context, string, []byte) error { return nil }, func(state string) {
		if state == "connected" {
			connected <- struct{}{}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- inbound.Start(ctx) }()
	select {
	case <-connected:
	case err := <-done:
		t.Fatalf("startup failed %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("ready missing")
	}
	if err := inbound.Start(ctx); err == nil {
		t.Fatal("duplicate consumer accepted")
	}
	inbound.Close()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("consumer shutdown stuck")
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "stopped")); err != nil {
		t.Fatal("owned bus was not stopped")
	}
	if matches, _ := filepath.Glob(filepath.Join(runner.DataRoot, "*.pid")); len(matches) != 0 {
		t.Fatal("consumer process did not exit gracefully")
	}
}

func TestCLIInboundRefusesForeignBusWithoutStoppingIt(t *testing.T) {
	runner := fakeEventCLI(t)
	if err := os.WriteFile(filepath.Join(runner.DataRoot, "bus"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	inbound, _ := NewOfficialInbound(runner, nil, func(context.Context, string, []byte) error { return nil }, nil)
	if err := inbound.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "existing_bus_conflict") {
		t.Fatalf("foreign bus accepted %v", err)
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "stopped")); !os.IsNotExist(err) {
		t.Fatal("foreign bus stopped")
	}
}
