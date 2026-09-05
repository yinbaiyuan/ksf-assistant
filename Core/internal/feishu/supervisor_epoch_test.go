package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ksfassistant/core/internal/privateipc"
)

func waitSupervisor(t *testing.T, supervisor *Supervisor, matches func(SupervisorStatus) bool) SupervisorStatus {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		status := supervisor.Status()
		if matches(status) {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("supervisor did not reach expected state: %#v", supervisor.Status())
	return SupervisorStatus{}
}

func TestSupervisorEpochCallbacksManualAutomaticAndStaleRejection(t *testing.T) {
	supervisor := newTestSupervisor(t, "rpc")
	supervisor.restartDelays = []time.Duration{5 * time.Millisecond}
	if err := supervisor.SetHandler(privateipc.HandlerFunc(func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		if method != "core/test/epoch" {
			return nil, privateipc.ErrMethodNotFound
		}
		return map[string]uint64{"epoch": EpochFromContext(ctx)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	type connection struct {
		ctx        context.Context
		generation uint64
		err        error
	}
	connected := make(chan connection, 8)
	if err := supervisor.SetOnConnect(func(ctx context.Context, generation uint64) error {
		var result struct {
			Epoch uint64 `json:"epoch"`
		}
		err := supervisor.Call(ctx, "bridge/test/callback", nil, &result)
		if err == nil && result.Epoch != generation {
			err = errors.New("callback ctx epoch not injected")
		}
		connected <- connection{ctx, generation, err}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Stop(context.Background())
	next := func() connection {
		t.Helper()
		select {
		case result := <-connected:
			if result.err != nil || EpochFromContext(result.ctx) != result.generation {
				t.Fatalf("connect callback failed: %#v", result)
			}
			return result
		case <-time.After(4 * time.Second):
			t.Fatal("connect callback stuck (possibly called under supervisor lock)")
			return connection{}
		}
	}
	first := next()
	if first.generation != 1 || supervisor.Generation() != first.generation {
		t.Fatal("first generation missing")
	}
	if err := supervisor.SetOnConnect(nil); err == nil {
		t.Fatal("live callback replacement allowed")
	}
	oldHandler := supervisor.bindHandler(first.generation, privateipc.HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("stale handler was dispatched")
		return nil, nil
	}))
	supervisor.mu.Lock()
	err := supervisor.cmd.Process.Kill()
	supervisor.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	second := next()
	if second.generation != first.generation+1 || supervisor.IsCurrentGeneration(first.generation) {
		t.Fatal("automatic restart did not advance generation")
	}
	select {
	case <-first.ctx.Done():
	default:
		t.Fatal("old context is not cancelled")
	}
	if _, err := oldHandler.HandlePrivateRPC(context.Background(), "stale", nil); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale callback accepted: %v", err)
	}
	if err := supervisor.Call(WithEpoch(context.Background(), first.generation), "bridge/test/echo", nil, nil); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale outbound call accepted: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := supervisor.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	third := next()
	if third.generation != second.generation+1 {
		t.Fatal("manual restart did not advance generation")
	}
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if supervisor.Status().State != StateStopped || supervisor.IsCurrentGeneration(third.generation) || third.ctx.Err() == nil {
		t.Fatal("stop left generation active")
	}
}

func TestSupervisorInitializationStateAndCancellation(t *testing.T) {
	supervisor := newTestSupervisor(t, "wait")
	started := make(chan context.Context, 1)
	finished := make(chan struct{})
	if err := supervisor.SetOnConnect(func(ctx context.Context, generation uint64) error {
		started <- ctx
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Stop(context.Background())
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("missing callback")
	}
	supervisor.SetConfigured(true)
	if supervisor.Status().State != StateStarting {
		t.Fatal("configured bypassed initialization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("initialization did not cancel")
	}
	if supervisor.Status().State != StateStopped {
		t.Fatal("stale initialization result changed stopped state")
	}
}

func TestSupervisorInitializationFailureUsesBoundedRestarts(t *testing.T) {
	supervisor := newTestSupervisor(t, "wait")
	supervisor.restartDelays = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	if err := supervisor.SetOnConnect(func(context.Context, uint64) error { return errors.New("fixture initialization error") }); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Stop(context.Background())
	status := waitSupervisor(t, supervisor, func(status SupervisorStatus) bool { return status.PID == 0 && status.State == StateDegraded })
	if status.Generation != 3 || status.RestartCount != 2 {
		t.Fatalf("incorrect initialization retry lifecycle: %#v", status)
	}
}

func TestSupervisorEOFInvalidatesAndReapsChild(t *testing.T) {
	supervisor := newTestSupervisor(t, "eof")
	supervisor.restartDelays = nil
	connected := make(chan context.Context, 1)
	_ = supervisor.SetOnConnect(func(ctx context.Context, _ uint64) error { connected <- ctx; return nil })
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer supervisor.Stop(context.Background())
	waitSupervisor(t, supervisor, func(status SupervisorStatus) bool { return status.PID == 0 && status.State == StateDegraded })
	select {
	case ctx := <-connected:
		if ctx.Err() == nil {
			t.Fatal("EOF left epoch context active")
		}
	case <-time.After(time.Second):
		t.Fatal("connection callback missing")
	}
}
