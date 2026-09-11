package feishu

import (
	"context"
	"errors"
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

func TestCLIInboundRecoversOwnedConsumerExit(t *testing.T) {
	runner := fakeEventCLI(t)
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "  read ignored", `  if [ "$key" = "im.message.receive_v1" ] && [ ! -f "$root/failed-once" ]; then
   : > "$root/failed-once"
   sleep 0.2
   exit 1
  fi
  read ignored`, 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 4)
	inbound, _ := NewOfficialInbound(runner, nil, func(context.Context, string, []byte) error { return nil }, func(state string) {
		if state == "connected" {
			connected <- struct{}{}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer inbound.Close()
	done := make(chan error, 1)
	go func() { done <- inbound.Start(ctx) }()
	for count := 0; count < 2; count++ {
		select {
		case <-connected:
		case err := <-done:
			t.Fatalf("owned consumer was never recovered: %v", err)
		case <-time.After(6 * time.Second):
			t.Fatal("reconnection did not become ready")
		}
	}
}

func TestCLIInboundSurvivesOneFailedStatusProbe(t *testing.T) {
	runner := fakeEventCLI(t)
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), " status)\n", " status)\n  if [ -f \"$root/fail-probe\" ]; then rm -f \"$root/fail-probe\"; exit 1; fi\n", 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	states := make(chan string, 10)
	inbound, _ := NewOfficialInbound(runner, nil, func(context.Context, string, []byte) error { return nil }, func(state string) { states <- state })
	defer inbound.Close()
	done := make(chan error, 1)
	go func() { done <- inbound.Start(context.Background()) }()
	connected := 0
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for connected < 2 {
		select {
		case state := <-states:
			if state == "connected" {
				connected++
				if connected == 1 {
					if err := os.WriteFile(filepath.Join(runner.DataRoot, "fail-probe"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
		case err := <-done:
			t.Fatalf("single probe failure stopped the listeners: %v", err)
		case <-timer.C:
			t.Fatal("listener health did not recover")
		}
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "stopped")); !os.IsNotExist(err) {
		t.Fatal("healthy consumers were restarted")
	}
}

func TestCLIInboundSkipsMalformedEventWithoutStoppingConsumers(t *testing.T) {
	runner := fakeEventCLI(t)
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "  read ignored", `  if [ "$key" = "card.action.trigger" ]; then
   printf 'not-json\n'
   printf '{"type":"card.action.trigger","event_id":"evt-valid","message_id":"om-card","operator_id":"ou-user","chat_id":"oc-chat","action_tag":"button","action_value":"{\\"operation\\":\\"release\\"}"}\n'
  fi
  read ignored`, 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	received := make(chan struct{}, 1)
	inbound, _ := NewOfficialInbound(runner, nil, func(_ context.Context, key string, _ []byte) error {
		if key == "card.action.trigger" {
			received <- struct{}{}
		}
		return nil
	}, nil)
	done := make(chan error, 1)
	go func() { done <- inbound.Start(context.Background()) }()
	defer inbound.Close()
	select {
	case <-received:
	case err := <-done:
		t.Fatalf("one malformed event stopped the listeners: %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("valid event after malformed input was not delivered")
	}
}

func TestCLIInboundRecoversAfterTransientPersistenceFailure(t *testing.T) {
	runner := fakeEventCLI(t)
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "  read ignored", `  if [ "$key" = "card.action.trigger" ] && [ ! -f "$root/emitted-once" ]; then
	   : > "$root/emitted-once"
	   sleep 1
	   printf '{"type":"card.action.trigger","event_id":"evt-transient","message_id":"om-card","operator_id":"ou-user","chat_id":"oc-chat","action_tag":"button","action_value":"{\\"operation\\":\\"release\\"}"}\n'
  fi
  read ignored`, 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	states := make(chan string, 10)
	inbound, _ := NewOfficialInbound(runner, nil, func(context.Context, string, []byte) error {
		return errors.New("fixture_persistence_failure")
	}, func(state string) { states <- state })
	done := make(chan error, 1)
	go func() { done <- inbound.Start(context.Background()) }()
	defer inbound.Close()
	connected := 0
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for connected < 2 {
		select {
		case state := <-states:
			if state == "connected" {
				connected++
			}
		case err := <-done:
			t.Fatalf("transient persistence failure stopped the listeners: %v", err)
		case <-timer.C:
			t.Fatal("listeners did not recover after transient persistence failure")
		}
	}
}
