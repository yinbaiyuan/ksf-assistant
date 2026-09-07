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
	time.Sleep(100 * time.Millisecond)
	items, err := processor.workbox.Pending()
	if err != nil || len(items) != 1 {
		t.Fatalf("work was not retained: %v %#v", err, items)
	}
	processor.Close()
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
	deferred.Close()
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
	defer recovered.Close()
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err = recovered.workbox.Pending()
	if err != nil || len(items) != 0 || count != 1 {
		t.Fatalf("work recovery failed: count=%d err=%v items=%#v", count, err, items)
	}
}

func TestCardDeliveryFailureRemainsPendingAndCanRecover(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), nil, func(context.Context, InboundCardAction) error {
		if attempts.Add(1) <= 1 {
			return errors.New("Core ACK unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	if err := processor.Handle(context.Background(), "card.action.trigger", deliveryCard("evt_card_retry")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var work inboundWork
	for time.Now().Before(deadline) {
		items, err := processor.workbox.Pending()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 1 && items[0].AttemptCount == 1 {
			work = items[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if work.ID == "" || work.Status != "pending" || work.LastError != "temporary_failure" || len(work.Payload) == 0 {
		t.Fatalf("card delivery was discarded instead of deferred: %#v", work)
	}
	if err := processor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 {
		t.Fatal("future retry was executed early")
	}
	past := time.Now().Add(-time.Second)
	work.NextAttemptAt = &past
	if err := writePrivateJSON(filepath.Join(processor.workbox.root, work.ID+".json"), work); err != nil {
		t.Fatal(err)
	}
	if err := processor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitDeliveryDrained(t, processor)
	if attempts.Load() != 2 {
		t.Fatalf("card ACK was not retried: %d", attempts.Load())
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
	defer processor.Close()
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

func TestRecoverDeliversCardActionLeftByRestart(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	box := NewInboundWorkbox(root)
	work, err := box.Enqueue("card.action.trigger", deliveryCard("restart-original-id"))
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), nil, func(ctx context.Context, action InboundCardAction) error {
		if action.EventID != "restart-original-id" {
			return errors.New("event ID changed")
		}
		attempts.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	if err := processor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 {
		t.Fatal("persisted card delivery was dropped after restart")
	}
	var stored inboundWork
	if missing, err := readPrivateJSON(filepath.Join(box.root, work.ID+".json"), &stored); err != nil || missing {
		t.Fatalf("read card delivery receipt: missing=%v err=%v", missing, err)
	}
	if stored.Status != "completed" || len(stored.Payload) != 0 {
		t.Fatalf("unexpected ACK receipt: %#v", stored)
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
