package feishu

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/privateipc"
)

func TestManagedSupervisorStartsAndStopsChildWithPublicState(t *testing.T) {
	supervisor := newTestSupervisor(t, "wait")
	supervisor.SetConfigured(false)
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	status := supervisor.Status()
	if status.State != StateIdleUnconfigured || status.PID <= 0 || status.Configured {
		t.Fatalf("unexpected running status: %#v", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	status = supervisor.Status()
	if status.State != StateStopped || status.PID != 0 {
		t.Fatalf("unexpected stopped status: %#v", status)
	}
}

func TestManagedSupervisorRestartsThreeTimesThenDegrades(t *testing.T) {
	supervisor := newTestSupervisor(t, "crash")
	defer supervisor.Stop(context.Background())
	supervisor.restartDelays = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 15 * time.Millisecond}
	supervisor.restartWindow = time.Second
	supervisor.healthyReset = time.Hour
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := supervisor.Status()
		if status.State == StateDegraded && status.PID == 0 {
			if status.RestartCount != 3 || !strings.Contains(status.LastError, "exit status") {
				t.Fatalf("unexpected degraded status: %#v", status)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("supervisor did not degrade: %#v", supervisor.Status())
}

func TestManagedSupervisorOwnsBidirectionalPrivateRPC(t *testing.T) {
	supervisor := newTestSupervisor(t, "rpc")
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var response struct {
		Value string `json:"value"`
	}
	if err := supervisor.Call(ctx, "bridge/test/echo", map[string]string{"value": "private"}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Value != "private" {
		t.Fatalf("unexpected private RPC response: %#v", response)
	}
}

func TestFeishuSupervisorHelperProcess(t *testing.T) {
	if os.Getenv("KSF_ASSISTANT_TEST_BRIDGE") != "1" {
		return
	}
	mode := os.Getenv("KSF_ASSISTANT_TEST_BRIDGE_MODE")
	if mode == "crash" {
		os.Exit(7)
	}
	if mode == "rpc" {
		var peer *privateipc.Peer
		peer = privateipc.NewPeer(os.Stdin, os.Stdout, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			if method == "bridge/test/callback" {
				var result map[string]uint64
				err := peer.Call(ctx, "core/test/epoch", nil, &result)
				return result, err
			}
			if method != "bridge/test/echo" {
				return nil, privateipc.ErrMethodNotFound
			}
			var value map[string]string
			_ = json.Unmarshal(params, &value)
			return value, nil
		}))
		_ = peer.Serve(context.Background())
		os.Exit(0)
	}
	if mode == "eof" {
		_ = os.Stdout.Close()
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func newTestSupervisor(t *testing.T, mode string) *Supervisor {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return NewSupervisor(SupervisorOptions{
		Executable: executable,
		DataRoot:   t.TempDir(),
		Arguments:  []string{"-test.run=TestFeishuSupervisorHelperProcess"},
		Environment: []string{
			"KSF_ASSISTANT_TEST_BRIDGE=1",
			"KSF_ASSISTANT_TEST_BRIDGE_MODE=" + mode,
		},
	})
}
