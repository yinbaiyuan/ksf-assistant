package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"ksfassistant/core/internal/feishuprotocol"
)

func TestCoreOwnsOpaqueCardValidation(t *testing.T) {
	core := &fakeCorePort{}
	runtime := testRuntime(t, core, &fakeFeishuPort{})
	link := seedCard(t, runtime)
	for _, test := range []struct {
		name, action string
		extra, form  map[string]any
	}{
		{"unknown", "task_link_arbitrary", nil, nil},
		{"empty-followup", "task_link_followup", nil, map[string]any{"followup": "  "}},
		{"invalid-mode", "task_link_followup", nil, map[string]any{"followup": "go", "turnMode": "arbitrary"}},
		{"missing-plan-revision", "task_link_implement_plan", nil, nil},
		{"missing-question-revision", "task_link_answer", map[string]any{"questionId": "question", "answer": "yes"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := map[string]any{"namespace": "feishu_bridge", "version": 1, "action": test.action, "taskKey": link.TaskKey, "linkId": link.ID}
			for key, item := range test.extra {
				value[key] = item
			}
			err := runtime.HandleCard(context.Background(), InboundCardAction{OperatorOpenID: "user-1", MessageID: "card-1", Value: value, FormValue: test.form})
			if !errors.Is(err, ErrInvalidTaskCard) || core.calls.Load() != 0 {
				t.Fatalf("invalid card invoked business: %v", err)
			}
		})
	}
	if err := runtime.HandleCard(context.Background(), InboundCardAction{Value: map[string]any{"namespace": "another-business", "action": "anything"}}); err != nil || core.calls.Load() != 0 {
		t.Fatal("foreign card invoked Codex")
	}
	value := map[string]any{"namespace": "feishu_bridge", "version": 1, "action": "task_link_release", "taskKey": link.TaskKey, "linkId": link.ID}
	if err := runtime.HandleCard(context.Background(), InboundCardAction{OperatorOpenID: "user-1", MessageID: "card-1", Value: value}); err != nil {
		t.Fatal(err)
	}
	current, _, err := runtime.Store().FindByID(link.ID)
	if err != nil || current.LinkState != "released" {
		t.Fatal("opaque release was not interpreted by Core")
	}
}

func TestCardDigestPreservesPreviouslyAcknowledgedEnvelope(t *testing.T) {
	value := map[string]any{"namespace": "feishu_bridge", "version": 1, "action": "task_link_followup", "taskKey": "0123456789abcdef0123", "linkId": "LINK-0123456789ABCDEF"}
	legacy := InboundCardAction{EventID: "old-card-event", OperatorOpenID: "user", MessageID: "card", Action: "task_link_followup", TaskKey: "0123456789abcdef0123", LinkID: "LINK-0123456789ABCDEF", Value: value, FormValue: map[string]any{"followup": "continue"}, Raw: map[string]any{"action": map[string]any{"form_value": map[string]any{"followup": "continue"}}}}
	oldPayload, _ := json.Marshal(legacy)
	var oldCanonical map[string]any
	if err := json.Unmarshal(oldPayload, &oldCanonical); err != nil {
		t.Fatal(err)
	}
	oldPayload, _ = json.Marshal(oldCanonical)
	oldHash := sha256.Sum256(append([]byte("card\x00"), oldPayload...))
	opaque := legacy
	opaque.Action, opaque.TaskKey, opaque.LinkID = "", "", ""
	opaque.FormValue = map[string]any{"followup": " continue\x00 "}
	opaque.Raw = map[string]any{"action": map[string]any{"form_value": map[string]any{"followup": " continue\x00 "}}}
	newPayload, _ := json.Marshal(opaque)
	event := feishuprotocol.Event{ID: legacy.EventID, Kind: "card", Payload: newPayload}
	_, _, digest, err := decodeEvent(event)
	if err != nil || digest != hex.EncodeToString(oldHash[:]) {
		t.Fatalf("old ACK digest changed: %s %v", digest, err)
	}
	opaque.Action = "task_link_release"
	event.Payload, _ = json.Marshal(opaque)
	if _, _, _, err := decodeEvent(event); !errors.Is(err, ErrInvalidTaskCard) {
		t.Fatal("contradictory envelope accepted")
	}
}
