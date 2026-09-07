package feishu

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDeliveryOneAttemptPerDispatch(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	calls := 0
	p, err := NewInboundProcessor(root, DefaultSettings(), func(context.Context, InboundMessage) error { calls++; return errors.New("temporary") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	w, err := p.workbox.Enqueue("im.message.receive_v1", deliveryMessage("single"))
	if err != nil {
		t.Fatal(err)
	}
	p.processPersisted(w)
	if calls != 1 {
		t.Fatalf("one dispatch made %d attempts", calls)
	}
}
func TestOldDeliveryPausesWithoutDispatch(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	calls := 0
	p, _ := NewInboundProcessor(root, DefaultSettings(), func(context.Context, InboundMessage) error { calls++; return nil }, nil)
	defer p.Close()
	w, _ := p.workbox.Enqueue("im.message.receive_v1", deliveryMessage("old"))
	w.CreatedAt = time.Now().Add(-2 * time.Hour)
	w.AttemptCount = 2000
	p.processPersisted(w)
	if calls != 0 {
		t.Fatal("old instruction was dispatched")
	}
}

func TestPausedDeliverySurvivesRestartAndExplicitRetryKeepsIdentity(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	box := NewInboundWorkbox(root)
	w, _ := box.Enqueue("im.message.receive_v1", deliveryMessage("paused"))
	w.AttemptCount = 21
	if err := box.Pause(w, "retry_budget_exhausted", "budget", 0); err != nil {
		t.Fatal(err)
	}
	p, _ := NewInboundProcessor(root, DefaultSettings(), nil, nil)
	defer p.Close()
	items, err := p.workbox.Ready(time.Now())
	if err != nil || len(items) != 0 {
		t.Fatal("paused event recovered")
	}
	event, err := box.ReviewEvent(w.ID)
	if err != nil || event.ID != "paused" {
		t.Fatalf("identity lost %v", err)
	}
	if err := box.RetryReviewed(w.ID); err != nil {
		t.Fatal(err)
	}
	items, _ = box.Ready(time.Now())
	if len(items) != 1 || items[0].AttemptCount != 21 || items[0].BudgetAttempts() != 0 {
		t.Fatal("explicit retry reset audit or failed to reset budget")
	}
}
