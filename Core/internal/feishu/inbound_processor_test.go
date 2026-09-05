package feishu

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestInboundProcessorAuthorizesConfiguredDirectUserAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	config := DefaultClientConfig()
	config.MessageTargets["我"] = MessageTarget{Type: "open_id", ID: "ou_allowed"}
	config.DirectAllowedAliases = []string{"我"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	handled := make(chan struct{}, 2)
	processor, err := NewInboundProcessor(root, DefaultSettings(), func(_ context.Context, message InboundMessage) error {
		if message.Text != "你好" {
			t.Fatalf("unexpected text: %q", message.Text)
		}
		handled <- struct{}{}
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"header": map[string]any{"event_id": "evt_1"}, "event": map[string]any{"message": map[string]any{"message_id": "om_1", "chat_id": "oc_1", "chat_type": "p2p", "message_type": "text", "content": `{"text":"你好"}`}, "sender": map[string]any{"sender_id": map[string]any{"open_id": "ou_allowed"}}}})
	if err := processor.Handle(context.Background(), "im.message.receive_v1", payload); err != nil {
		t.Fatal(err)
	}
	if err := processor.Handle(context.Background(), "im.message.receive_v1", payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("event was not handled")
	}
	select {
	case <-handled:
		t.Fatal("duplicate event was handled")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestInboundProcessorRejectsUnknownDirectUser(t *testing.T) {
	root := t.TempDir()
	if err := NewClientConfigStore(root).Save(DefaultClientConfig()); err != nil {
		t.Fatal(err)
	}
	processor, err := NewInboundProcessor(root, DefaultSettings(), func(context.Context, InboundMessage) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"event":{"message":{"message_id":"om_1","chat_id":"oc_1","chat_type":"p2p","message_type":"text","content":"{\"text\":\"x\"}"},"sender":{"sender_id":{"open_id":"ou_unknown"}}}}`)
	if err := processor.Handle(context.Background(), "im.message.receive_v1", payload); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		items, err := processor.workbox.Pending()
		if err == nil && len(items) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("denied event was not terminally discarded")
}

func TestNormalizeInboundCardValidatesFrozenNamespaceAndForm(t *testing.T) {
	raw := map[string]any{"event": map[string]any{"event_id": "evt_card", "operator": map[string]any{"operator_id": map[string]any{"open_id": "ou_owner"}}, "context": map[string]any{"open_chat_id": "oc_chat", "open_message_id": "om_card"}, "token": "token", "action": map[string]any{"value": map[string]any{"namespace": "feishu_bridge", "version": float64(1), "action": "task_link_followup", "taskKey": "0123456789abcdef0123", "linkId": "LINK-0123456789ABCDEF"}, "form_value": map[string]any{"followup": "  继续  ", "turnMode": "default"}}}}
	action, err := normalizeInboundCard(raw)
	if err != nil {
		t.Fatal(err)
	}
	if action.EventID != "evt_card" || action.ChatID != "oc_chat" || action.MessageID != "om_card" || action.FormValue["followup"] != "继续" {
		t.Fatalf("unexpected normalized action: %#v", action)
	}
	raw["event"].(map[string]any)["action"].(map[string]any)["value"].(map[string]any)["namespace"] = "foreign"
	if _, err := normalizeInboundCard(raw); err == nil {
		t.Fatal("foreign card namespace was accepted")
	}
	raw["event"].(map[string]any)["action"].(map[string]any)["value"].(map[string]any)["namespace"] = "feishu_bridge"
	delete(raw["event"].(map[string]any)["action"].(map[string]any)["value"].(map[string]any), "linkId")
	if _, err := normalizeInboundCard(raw); err == nil {
		t.Fatal("card action without a connection identity was accepted")
	}
}

func TestNormalizeInboundAnswerRequiresAndPreservesQuestionRevision(t *testing.T) {
	value := map[string]any{
		"namespace": "feishu_bridge", "version": float64(1), "action": "task_link_answer",
		"taskKey": "0123456789abcdef0123", "linkId": "LINK-0123456789ABCDEF",
		"questionId": "choice", "questionRevision": "0123456789abcdef0123", "answer": "A",
	}
	raw := map[string]any{"event": map[string]any{
		"operator": map[string]any{"operator_id": map[string]any{"open_id": "ou_owner"}},
		"action":   map[string]any{"value": value},
	}}
	action, err := normalizeInboundCard(raw)
	if err != nil {
		t.Fatal(err)
	}
	if action.QuestionRevision != "0123456789abcdef0123" {
		t.Fatalf("question revision was not preserved: %#v", action)
	}
	delete(value, "questionRevision")
	if _, err := normalizeInboundCard(raw); err == nil {
		t.Fatal("answer without a question revision was accepted")
	}
}
