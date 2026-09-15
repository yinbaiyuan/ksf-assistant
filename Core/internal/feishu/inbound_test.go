package feishu

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCLIInboundNamedFormWithoutValue(t *testing.T) {
	var got []byte
	inbound, _ := NewOfficialInbound(CapabilityExecutor{Binary: "fixture"}, nil, func(_ context.Context, _ string, body []byte) error { got = body; return nil }, nil)
	err := inbound.HandleCLIEvent(context.Background(), "card.action.trigger", []byte(`{"type":"card.action.trigger","event_id":"evt_form","message_id":"om_fixture","operator_id":"ou_fixture","action_tag":"button","action_name":"probe_submit_fixture","form_value":"{\"probe_input\":\"中文测试\"}"}`))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(got, &raw); err != nil {
		t.Fatal(err)
	}
	card, err := normalizeInboundCard(raw)
	if err != nil || card.FormValue["probe_input"] != "中文测试" || jsonObject(card.Raw["action"])["name"] != "probe_submit_fixture" {
		t.Fatal("form identity or content lost", err)
	}
}

func TestCLIInboundStillRejectsUnidentifiedForms(t *testing.T) {
	for _, extra := range []string{
		`"action_tag":"button","form_value":"{\"field\":\"text\"}"`,
		`"action_tag":"button","action_name":"submit","form_value":"{}"`,
		`"action_tag":"button","action_name":"submit","form_value":"bad"`,
	} {
		called := false
		inbound, _ := NewOfficialInbound(CapabilityExecutor{Binary: "fixture"}, nil, func(context.Context, string, []byte) error { called = true; return nil }, nil)
		err := inbound.HandleCLIEvent(context.Background(), "card.action.trigger", []byte(`{"type":"card.action.trigger","event_id":"evt_bad","message_id":"om_fixture","operator_id":"ou_fixture",`+extra+`}`))
		if err == nil || called {
			t.Fatal("malformed or unidentified form accepted")
		}
	}
}

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
	inbound, err := NewOfficialInbound(CapabilityExecutor{Binary: "fixture-cli"}, nil, func(_ context.Context, key string, body []byte) error {
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
	inbound, err := NewOfficialInbound(CapabilityExecutor{Binary: "fixture-cli"}, nil, func(_ context.Context, key string, _ []byte) error { got = key; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = inbound.HandlePayload(context.Background(), []byte(`{"schema":"2.0","header":{"event_id":"evt_approval","event_type":"approval.task.status_changed_v4"},"event":{"task_id":"task_1","instance_code":"instance_1"}}`))
	if err != nil || got != ApprovalTaskStatusChangedEvent {
		t.Fatalf("approval replay failed: key=%q err=%v", got, err)
	}
}
