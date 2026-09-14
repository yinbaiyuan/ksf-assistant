package integration

import (
	"context"
	"strings"
	"testing"
	"time"
)

type blockedCardPort struct {
	fakeFeishuPort
	entered chan struct{}
	release chan struct{}
	cards   chan string
}

func (p *blockedCardPort) PatchCard(ctx context.Context, _, card string) error {
	if p.cards != nil {
		p.cards <- card
	}
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestCardWorkerCoalescesWhileSendingAndDeliversTerminal(t *testing.T) {
	p := &blockedCardPort{entered: make(chan struct{}, 1), release: make(chan struct{}), cards: make(chan string, 8)}
	r, err := NewRuntime(t.TempDir(), p, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, _ := r.links.Upsert("thread", "title", "", "me")
	l, _ = r.links.UpdateByID(l.ID, func(l *TaskLink) {
		l.RootMessageID = "card"
		l.Detail = "first"
		l.SetExtraValue("cardSyncPending", true)
	})
	r.deliverTaskCard(l.ID)
	select {
	case <-p.cards:
	case <-time.After(time.Second):
		t.Fatal("no first send")
	}
	for _, text := range []string{"intermediate", "finished"} {
		_, err = r.links.UpdateByID(l.ID, func(l *TaskLink) {
			l.Detail = text
			l.TurnState = "completed"
			l.SetExtraString("latestInputTurnId", "turn-final")
			l.SetExtraValue("cardSyncPending", true)
		})
		if err != nil {
			t.Fatal(err)
		}
		r.deliverTaskCard(l.ID)
	}
	close(p.release)
	select {
	case card := <-p.cards:
		if !strings.Contains(card, "finished") || strings.Contains(card, "intermediate") {
			t.Fatal("stale queued content sent")
		}
	case <-time.After(time.Second):
		t.Fatal("terminal card lost")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _, _ := r.links.FindByID(l.ID)
		if current.ExtraString("lastDeliveredTurnId") == "turn-final" && !TaskLinkCardSyncPending(current) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("terminal delivery not acknowledged")
}

func TestCardObservationDoesNotWaitForNetwork(t *testing.T) {
	p := &blockedCardPort{entered: make(chan struct{}, 1), release: make(chan struct{})}
	r, err := NewRuntime(t.TempDir(), p, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer close(p.release)
	l, _ := r.links.Upsert("thread", "title", "", "me")
	l, _ = r.links.UpdateByID(l.ID, func(l *TaskLink) { l.RootMessageID = "card" })
	snapshot := map[string]any{"turns": []any{map[string]any{"id": "turn", "status": "running", "items": []any{map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "partial"}}}}}
	done := make(chan error, 1)
	go func() { done <- r.applyDesktopTaskSnapshot(context.Background(), l, snapshot, "1") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("observation blocked on card delivery")
	}
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("sender did not start")
	}
	l, _, _ = r.links.FindByID(l.ID)
	if l.Detail != "partial" {
		t.Fatalf("latest projection lost: %q", l.Detail)
	}
}

func TestBridgeProgressUsesPublicProjectionWithoutChangingOwner(t *testing.T) {
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, _ := r.links.Upsert("thread", "title", "", "me")
	l, _ = r.links.UpdateByID(l.ID, func(l *TaskLink) { l.ActiveTurnID = "turn"; l.TurnState = "running"; l.TurnOwner = "bridge" })
	r.projectBridgeProgress(l, map[string]any{"thread": map[string]any{"turns": []any{map[string]any{"id": "turn", "items": []any{
		map[string]any{"id": "public", "type": "agentMessage", "phase": "final_answer", "text": "partial answer"},
		map[string]any{"id": "private", "type": "agentMessage", "phase": "analysis", "text": "private reasoning"},
	}}}}}, "turn")
	current, _, _ := r.links.FindByID(l.ID)
	if current.TurnOwner != "bridge" || current.Detail != "partial answer" || !TaskLinkCardSyncPending(current) {
		t.Fatalf("bad bridge projection: owner=%s detail=%q", current.TurnOwner, current.Detail)
	}
}
