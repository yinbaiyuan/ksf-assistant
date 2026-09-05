package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func deliveryConfig(t *testing.T, root string) {
	t.Helper()
	config := DefaultClientConfig()
	config.MessageTargets["owner"] = MessageTarget{Type: "open_id", ID: "ou_owner"}
	config.DirectAllowedAliases = []string{"owner"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
}

func deliveryMessage(id string) []byte {
	data, _ := json.Marshal(map[string]any{"header": map[string]any{"event_id": id}, "event": map[string]any{
		"message": map[string]any{"message_id": id, "chat_id": "oc_chat", "chat_type": "p2p", "message_type": "text", "content": `{"text":"hello"}`},
		"sender":  map[string]any{"sender_id": map[string]any{"open_id": "ou_owner"}},
	}})
	return data
}

func deliveryCard(id string) []byte {
	data, _ := json.Marshal(map[string]any{"header": map[string]any{"event_id": id}, "event": map[string]any{
		"operator": map[string]any{"operator_id": map[string]any{"open_id": "ou_owner"}},
		"context":  map[string]any{"open_message_id": "om_card", "open_chat_id": "oc_chat"},
		"action":   map[string]any{"value": map[string]any{"namespace": "feishu_bridge", "version": 1, "action": "task_link_interrupt", "taskKey": "0123456789abcdef0123", "linkId": "LINK-0123456789ABCDEF"}},
	}})
	return data
}

func waitDeliveryDrained(t *testing.T, processor *InboundProcessor) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items, err := processor.workbox.Pending()
		if err == nil && len(items) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("delivery work did not drain")
}

func TestDeliveryRetriesLostACKForBothMessagesAndCards(t *testing.T) {
	for _, kind := range []string{"message", "card"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			deliveryConfig(t, root)
			var attempts atomic.Int32
			deliver := func() error {
				if attempts.Add(1) == 1 {
					return errors.New("Core accepted event but ACK was lost")
				}
				return nil
			}
			processor, err := NewInboundProcessor(root, DefaultSettings(), func(context.Context, InboundMessage) error { return deliver() }, func(context.Context, InboundCardAction) error { return deliver() })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(processor.Close)
			key, payload := "im.message.receive_v1", deliveryMessage("message-retry")
			if kind == "card" {
				key, payload = "card.action.trigger", deliveryCard("card-retry")
			}
			if err := processor.Handle(context.Background(), key, payload); err != nil {
				t.Fatal(err)
			}
			waitDeliveryDrained(t, processor)
			if attempts.Load() != 2 {
				t.Fatalf("delivery attempts: %d", attempts.Load())
			}
		})
	}
}

func TestDeliveryRecoversCardAfterRestartWithoutDroppingIt(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	box := NewInboundWorkbox(root)
	if _, err := box.Enqueue("card.action.trigger", deliveryCard("card-restart")); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), nil, func(ctx context.Context, card InboundCardAction) error {
		if card.EventID != "card-restart" {
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
		t.Fatal("durable card delivery was not resumed")
	}
	waitDeliveryDrained(t, processor)
}

func TestDeliveryCloseCancelsAndPreservesUnacknowledgedWork(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	started := make(chan struct{})
	processor, err := NewInboundProcessor(root, DefaultSettings(), func(ctx context.Context, message InboundMessage) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), "im.message.receive_v1", deliveryMessage("cancelled")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	closed := make(chan struct{})
	go func() { processor.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("processor Close did not cancel delivery")
	}
	items, err := processor.workbox.Pending()
	if err != nil || len(items) != 1 {
		t.Fatalf("unacknowledged event lost: %#v %v", items, err)
	}
	if err := processor.Handle(context.Background(), "im.message.receive_v1", deliveryMessage("after-close")); err == nil {
		t.Fatal("closed processor accepted new work")
	}
}

func TestDeliveryUsesFreshAuthorizationAtEveryAttempt(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), func(ctx context.Context, message InboundMessage) error {
		attempts.Add(1)
		if err := NewClientConfigStore(root).Save(DefaultClientConfig()); err != nil {
			return err
		}
		return errors.New("ACK lost")
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	if err := processor.Handle(context.Background(), "im.message.receive_v1", deliveryMessage("revoked")); err != nil {
		t.Fatal(err)
	}
	waitDeliveryDrained(t, processor)
	if attempts.Load() != 1 {
		t.Fatal("delivery reused revoked authorization")
	}
}

func TestDeliveryUsesFreshGroupSettings(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	settings := DefaultSettings()
	settings.Group.Enabled = true
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, settings, func(context.Context, InboundMessage) error { attempts.Add(1); return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	settings.Group.Enabled = false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(deliveryMessage("group-revoked"), &raw)
	raw["event"].(map[string]any)["message"].(map[string]any)["chat_type"] = "group"
	payload, _ := json.Marshal(raw)
	if err := processor.dispatch(context.Background(), "im.message.receive_v1", payload); err == nil {
		t.Fatal("disabled group accepted")
	}
	if attempts.Load() != 0 {
		t.Fatal("cached group settings reached Core")
	}
}

func TestDeliveryWorkerQueueIsBoundedWithDurableOverflow(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	started := make(chan struct{}, 1)
	processor, err := NewInboundProcessor(root, DefaultSettings(), func(ctx context.Context, message InboundMessage) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	count := inboundDeliveryBuffer + 10
	for index := 0; index < count; index++ {
		if err := processor.Handle(context.Background(), "im.message.receive_v1", deliveryMessage(fmt.Sprintf("bounded-%d", index))); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery not started")
	}
	processor.executionMu.Lock()
	flights := len(processor.inFlight)
	processor.executionMu.Unlock()
	if flights > inboundDeliveryBuffer+1 {
		t.Fatalf("unbounded scheduled work: %d", flights)
	}
	items, err := processor.workbox.Pending()
	if err != nil || len(items) != count {
		t.Fatalf("overflow was not durable: %d %v", len(items), err)
	}
}

func TestDeliveryDoesNotClassifyRemoteBusinessErrors(t *testing.T) {
	root := t.TempDir()
	deliveryConfig(t, root)
	var attempts atomic.Int32
	processor, err := NewInboundProcessor(root, DefaultSettings(), nil, func(context.Context, InboundCardAction) error {
		if attempts.Add(1) == 1 {
			return errors.New("invalid_remote_response")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	if err := processor.Handle(context.Background(), "card.action.trigger", deliveryCard("remote-error")); err != nil {
		t.Fatal(err)
	}
	waitDeliveryDrained(t, processor)
	if attempts.Load() != 2 {
		t.Fatal("remote error was treated as local terminal rejection")
	}
	files, _ := filepath.Glob(filepath.Join(processor.workbox.root, "*.json"))
	if len(files) != 1 {
		t.Fatal("durable delivery receipt missing")
	}
}
