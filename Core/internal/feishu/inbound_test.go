package feishu

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFixedEventCatalogMatchesFrozenContract(t *testing.T) {
	if len(FixedEventKeys) != 23 {
		t.Fatalf("event count = %d, want 23", len(FixedEventKeys))
	}
	for _, key := range FixedEventKeys {
		if key == "approval.instance.status_changed_v4" || key == "approval.task.status_changed_v4" {
			t.Fatalf("approval event escaped fixed catalog: %s", key)
		}
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

func TestOfficialInboundRejectsUnknownReplayEvent(t *testing.T) {
	inbound, err := NewOfficialInbound("cli_test", "secret", func(context.Context, string, []byte) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = inbound.HandlePayload(context.Background(), []byte(`{"schema":"2.0","header":{"event_type":"approval.task.status_changed_v4"}}`))
	if err == nil {
		t.Fatal("unknown event was accepted")
	}
}
