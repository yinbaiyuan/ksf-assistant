package integration

import (
	"encoding/json"
	"ksfassistant/core/internal/feishuprotocol"
	"testing"
)

func TestReviewReceiptDoesNotReplayUnknownOrConflictingEvents(t *testing.T) {
	r := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	data, _ := json.Marshal(InboundMessage{EventID: "evt", MessageID: "msg", ChatID: "chat", SenderOpenID: "owner", MessageType: "text", Text: "hi"})
	e := feishuprotocol.Event{ID: "evt", Kind: "message", Payload: data}
	state, err := r.EventReceiptState(e)
	if err != nil || state != "not_received" {
		t.Fatalf("%s %v", state, err)
	}
	_, conversation, digest, err := decodeEvent(e)
	_ = conversation
	if err != nil {
		t.Fatal(err)
	}
	r.inbox.mu.Lock()
	r.inbox.file.Events = append(r.inbox.file.Events, inboxEvent{Event: e, Digest: digest, State: "outcome_unknown"})
	r.inbox.mu.Unlock()
	state, err = r.EventReceiptState(e)
	if err != nil || state != "outcome_unknown" {
		t.Fatalf("%s %v", state, err)
	}
	e.Payload = json.RawMessage(`{"eventId":"evt","messageId":"msg","text":"changed"}`)
	if _, err = r.EventReceiptState(e); err == nil {
		t.Fatal("conflicting event accepted")
	}
}
