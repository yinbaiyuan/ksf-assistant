package feishu

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFixedEventCatalogMatchesFrozenContract(t *testing.T) {
	if len(FixedEventKeys) != 26 {
		t.Fatalf("event count = %d, want 26", len(FixedEventKeys))
	}
	for _, key := range []string{ApprovalInstanceStatusChangedEvent, ApprovalTaskStatusChangedEvent} {
		if !contains(FixedEventKeys, key) {
			t.Fatalf("approval event missing from fixed catalog: %s", key)
		}
	}
	if !contains(FixedEventKeys, MailMessageReceivedEvent) {
		t.Fatal("mail event missing from fixed catalog")
	}
}

func TestOfficialInboundDispatchesFrozenReplayWithoutSecondConsumer(t *testing.T) {
	var gotKey string
	var gotBody []byte
	inbound, err := NewOfficialInbound("cli_test", "secret", func(_ context.Context, key string, body []byte) error {
		gotKey, gotBody = key, body
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"schema":"2.0","header":{"event_id":"evt_1","event_type":"im.message.receive_v1"},"event":{"message":{"message_id":"msg_1"}}}`)
	if err := inbound.HandlePayload(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if gotKey != "im.message.receive_v1" {
		t.Fatalf("event key = %q", gotKey)
	}
	var decoded map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("sink body is not JSON: %v", err)
	}
}

func TestOfficialInboundDispatchesApprovalReplay(t *testing.T) {
	var got string
	inbound, err := NewOfficialInbound("cli_test", "secret", func(_ context.Context, key string, _ []byte) error { got = key; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = inbound.HandlePayload(context.Background(), []byte(`{"schema":"2.0","header":{"event_id":"evt_approval","event_type":"approval.task.status_changed_v4"},"event":{"task_id":"task_1","instance_code":"instance_1"}}`))
	if err != nil || got != ApprovalTaskStatusChangedEvent {
		t.Fatalf("approval replay failed: key=%q err=%v", got, err)
	}
}
