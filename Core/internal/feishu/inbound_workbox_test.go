package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestInboundWorkIsRecoveredAfterHandlerFailure(t *testing.T) {
	root := t.TempDir()
	config := DefaultClientConfig()
	config.MessageTargets["owner"] = MessageTarget{Type: "open_id", ID: "ou_owner"}
	config.DirectAllowedAliases = []string{"owner"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"header": map[string]any{"event_id": "evt_recover"}, "event": map[string]any{"message": map[string]any{"message_id": "om_recover", "chat_id": "oc_recover", "chat_type": "p2p", "message_type": "text", "content": `{"text":"继续"}`}, "sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}}}})
	processor, err := NewInboundProcessor(root, DefaultSettings(), func(context.Context, InboundMessage) error { return errors.New("temporary failure") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), "im.message.receive_v1", payload); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	items, err := processor.workbox.Pending()
	if err != nil || len(items) != 1 {
		t.Fatalf("work was not retained: %v %#v", err, items)
	}
	deferredCount := 0
	deferred, err := NewInboundProcessor(root, DefaultSettings(), func(context.Context, InboundMessage) error { deferredCount++; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := deferred.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deferredCount != 0 {
		t.Fatalf("future retry was executed early: %d", deferredCount)
	}
	past := time.Now().UTC().Add(-time.Second)
	items[0].NextAttemptAt = &past
	if err := writePrivateJSON(filepath.Join(processor.workbox.root, items[0].ID+".json"), items[0]); err != nil {
		t.Fatal(err)
	}
	count := 0
	recovered, err := NewInboundProcessor(root, DefaultSettings(), func(context.Context, InboundMessage) error { count++; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err = recovered.workbox.Pending()
	if err != nil || len(items) != 0 || count != 1 {
		t.Fatalf("work recovery failed: count=%d err=%v items=%#v", count, err, items)
	}
}

func TestCardActionFailureIsSingleAttemptAndNeverRecovered(t *testing.T) {
	root := t.TempDir()
	config := DefaultClientConfig()
	config.MessageTargets["owner"] = MessageTarget{Type: "open_id", ID: "ou_owner"}
	config.DirectAllowedAliases = []string{"owner"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"header": map[string]any{"event_id": "evt_card_once"}, "event": map[string]any{"operator": map[string]any{"operator_id": map[string]any{"open_id": "ou_owner"}}, "context": map[string]any{"open_message_id": "om_card"}, "action": map[string]any{"value": map[string]any{"namespace": "feishu_bridge", "version": 1, "action": "task_link_answer", "taskKey": "0123456789abcdef0123", "linkId": "LINK-0123456789ABCDEF", "questionRevision": "0123456789abcdef0123", "questionId": "choice", "answer": "A"}}}})
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), nil, func(context.Context, InboundCardAction) error {
		attempts.Add(1)
		return errors.New("temporary failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), "card.action.trigger", payload); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for attempts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if attempts.Load() != 1 {
		t.Fatalf("card action attempts = %d, want 1", attempts.Load())
	}
	if err := processor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if attempts.Load() != 1 {
		t.Fatalf("failed card action was replayed: %d", attempts.Load())
	}
	files, err := filepath.Glob(filepath.Join(processor.workbox.root, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("failed card action record missing: %v %v", err, files)
	}
	var work inboundWork
	if missing, err := readPrivateJSON(files[0], &work); err != nil || missing {
		t.Fatalf("read failed card action: missing=%v err=%v", missing, err)
	}
	if work.Status != "failed" || work.AttemptCount != 1 {
		t.Fatalf("unexpected failed card action: %#v", work)
	}
}

func TestRecoverDoesNotRaceActiveCardAction(t *testing.T) {
	root := t.TempDir()
	config := DefaultClientConfig()
	config.MessageTargets["owner"] = MessageTarget{Type: "open_id", ID: "ou_owner"}
	config.DirectAllowedAliases = []string{"owner"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"header": map[string]any{"event_id": "evt_card_race"}, "event": map[string]any{"operator": map[string]any{"operator_id": map[string]any{"open_id": "ou_owner"}}, "context": map[string]any{"open_message_id": "om_card"}, "action": map[string]any{"value": map[string]any{"namespace": "feishu_bridge", "version": 1, "action": "task_link_answer", "taskKey": "0123456789abcdef0123", "linkId": "LINK-0123456789ABCDEF", "questionRevision": "0123456789abcdef0123", "questionId": "choice", "answer": "A"}}}})
	started := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), nil, func(context.Context, InboundCardAction) error {
		attempts.Add(1)
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), "card.action.trigger", payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("card action did not start")
	}
	if err := processor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if attempts.Load() != 1 {
		t.Fatalf("card action executed concurrently: %d", attempts.Load())
	}
}

func TestRecoverExpiresCardActionLeftByRestart(t *testing.T) {
	root := t.TempDir()
	if err := NewClientConfigStore(root).Save(DefaultClientConfig()); err != nil {
		t.Fatal(err)
	}
	box := NewInboundWorkbox(root)
	work, err := box.Enqueue("card.action.trigger", []byte(`{"event":{"sequence":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), nil, func(context.Context, InboundCardAction) error {
		attempts.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 0 {
		t.Fatal("card action left by restart was executed")
	}
	var stored inboundWork
	if missing, err := readPrivateJSON(filepath.Join(box.root, work.ID+".json"), &stored); err != nil || missing {
		t.Fatalf("read expired card action: missing=%v err=%v", missing, err)
	}
	if stored.Status != "failed" || stored.LastError != "stale_card_action_after_restart" {
		t.Fatalf("unexpected expired card action: %#v", stored)
	}
}

func TestInboundRecoveryDefersFailedWorkWithoutBlockingLaterItems(t *testing.T) {
	root := t.TempDir()
	box := NewInboundWorkbox(root)
	first, err := box.Enqueue("im.message.receive_v1", []byte(`{"event":{"sequence":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	_, err = box.Enqueue("im.message.receive_v1", []byte(`{"event":{"sequence":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	seenSecond := false
	if err := box.Recover(context.Background(), func(_ context.Context, _ string, payload []byte) error {
		var value map[string]any
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		sequence := int(value["event"].(map[string]any)["sequence"].(float64))
		if sequence == 1 {
			return errors.New("temporary failure")
		}
		seenSecond = sequence == 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	items, err := box.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if !seenSecond || len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("recovery did not isolate failed work: seenSecond=%v items=%#v", seenSecond, items)
	}
}
