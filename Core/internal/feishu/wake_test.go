package feishu

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestWakeServerPublishesNodeCompatibleStateAndAcceptsOnlyFixedRoute(t *testing.T) {
	root := t.TempDir()
	var called atomic.Int32
	server, err := StartWakeServer(root, map[string]func(){"actionbox": func() { called.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	if err := WakeQueue(root, "actionbox"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for called.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if called.Load() != 1 {
		t.Fatal("wake handler was not called")
	}
	var state queueState
	if missing, err := readPrivateJSON(filepath.Join(root, "logs", "actionbox-state.json"), &state); err != nil || missing || state.Wake.ActualPort == nil {
		t.Fatalf("wake state missing: %v %#v", err, state)
	}
	response, err := http.Post("http://127.0.0.1:"+strconv.Itoa(*state.Wake.ActualPort)+"/internal/not-actionbox", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status %d", response.StatusCode)
	}
}
