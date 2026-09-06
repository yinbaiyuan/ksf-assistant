package userapproval

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func approvalFixture(t *testing.T) (*Broker, context.Context, context.CancelFunc, *time.Time, string) {
	t.Helper()
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	broker := New(func() time.Time { return now }, nil)
	broker.Poll(true)
	owner, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	id, err := broker.Request(owner, strings.Repeat("a", 64), Review{User: "test user", Application: "test app", Action: "send", Target: "test chat", Content: "test content"})
	if err != nil {
		t.Fatal(err)
	}
	return broker, owner, cancel, &now, id
}

func TestSingleAtomicConsume(t *testing.T) {
	broker, owner, _, _, id := approvalFixture(t)
	if broker.Decide(id, true) {
		t.Fatal("unpresented request accepted")
	}
	if broker.Poll(true).ID != id || !broker.Decide(id, true) {
		t.Fatal("decision failed")
	}
	var accepted atomic.Int32
	var workers sync.WaitGroup
	for count := 0; count < 30; count++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if broker.Consume(owner, id, strings.Repeat("a", 64)) == nil {
				accepted.Add(1)
			}
		}()
	}
	workers.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("consumed %d times", accepted.Load())
	}
	_ = broker.Consume(owner, id, strings.Repeat("b", 64))
	if state, _ := broker.Status(owner, id); state != "executing" {
		t.Fatal("stale changed request erased an executing fact")
	}
	if err := broker.Result(owner, id, "unknown"); err != nil {
		t.Fatal(err)
	}
	if err := broker.Consume(owner, id, strings.Repeat("a", 64)); err == nil {
		t.Fatal("unknown outcome replayed")
	}
}

func TestInvalidation(t *testing.T) {
	for _, scenario := range []string{"reject", "close", "disconnect", "timeout", "heartbeat", "changed", "restart"} {
		t.Run(scenario, func(t *testing.T) {
			broker, owner, cancel, now, id := approvalFixture(t)
			broker.Poll(true)
			if scenario == "reject" {
				broker.Decide(id, false)
			} else {
				broker.Decide(id, true)
			}
			switch scenario {
			case "close":
				broker.Poll(false)
			case "disconnect":
				cancel()
			case "timeout":
				*now = now.Add(Lifetime)
			case "heartbeat":
				*now = now.Add(HeartbeatLifetime + time.Second)
				broker.Poll(true)
			case "changed":
				_ = broker.Consume(owner, id, strings.Repeat("b", 64))
			case "restart":
				broker.Close()
			}
			if broker.Consume(owner, id, strings.Repeat("a", 64)) == nil {
				t.Fatal("invalid approval consumed")
			}
		})
	}
}

func TestConnectionAndQueueLimits(t *testing.T) {
	broker, owner, _, _, id := approvalFixture(t)
	other, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := broker.Status(other, id); err == nil {
		t.Fatal("cross connection status")
	}
	if _, err := broker.Request(context.Background(), strings.Repeat("a", 64), Review{}); err == nil {
		t.Fatal("unbound request")
	}
	for count := 1; count < MaximumPending; count++ {
		if _, err := broker.Request(owner, strings.Repeat("a", 64), Review{User: "u", Application: "a", Action: "w", Target: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := broker.Request(owner, strings.Repeat("a", 64), Review{User: "u", Application: "a", Action: "w", Target: "t"}); err == nil {
		t.Fatal("queue overflow")
	}
}

func TestAuditFailureAndNoSensitiveFields(t *testing.T) {
	broker, owner, _, _, id := approvalFixture(t)
	broker.Poll(true)
	broker.audit = func(event AuditEvent) error {
		if event.ID != id || len(event.Digest) != 64 {
			t.Fatal("bad audit")
		}
		return errors.New("disk full")
	}
	if broker.Decide(id, true) {
		t.Fatal("audit failure approved")
	}
	if broker.Consume(owner, id, strings.Repeat("a", 64)) == nil {
		t.Fatal("audit failure consumed")
	}
}

func TestExecutionDisconnectBecomesUnknown(t *testing.T) {
	broker, owner, cancel, _, id := approvalFixture(t)
	broker.Poll(true)
	broker.Decide(id, true)
	if err := broker.Consume(owner, id, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	cancel()
	state, err := broker.Status(owner, id)
	if err != nil || state != "unknown" {
		t.Fatalf("%s %v", state, err)
	}
}
