package feishu

import (
	"context"
	"encoding/json"
	"errors"
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
