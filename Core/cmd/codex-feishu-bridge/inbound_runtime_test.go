package main

import (
	"os"
	"strings"
	"testing"
)

func TestReplyIdempotencyKeyMatchesFrozenNodeContract(t *testing.T) {
	if got := replyIdempotencyKey("om_source", "final", 0); got != "codex-0ffa69992e2090a68c370874f2dc4972" {
		t.Fatalf("unexpected reply idempotency key: %q", got)
	}
	if replyIdempotencyKey("om_source", "processing", 0) == replyIdempotencyKey("om_source", "final", 0) {
		t.Fatal("reply phases must not share an idempotency key")
	}
}

func TestMessageTurnTracksOnlyTheBotOwnedCard(t *testing.T) {
	source, err := os.ReadFile("inbound_runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "turnID, message.MessageID, cardMessageID, cleanupDir") {
		t.Fatal("message turn must not attempt to patch the user's source message when card creation fails")
	}
	if !strings.Contains(string(source), `runtime.messages.Send(ctx, link.Target, "card", cardJSON`) {
		t.Fatal("message processing must create the task card through the verified proactive card channel")
	}
	if strings.Contains(string(source), `runtime.messages.Reply(ctx, message.MessageID, "card"`) {
		t.Fatal("message processing must not use the rejected reply-card endpoint")
	}
	if !strings.Contains(string(source), `[]string{message.MessageID, message.RootID, message.ParentID}`) {
		t.Fatal("message retries must recover their existing task link before creating another task")
	}
}

func TestReleaseCardActionAcceptsAlreadyReleasedReplay(t *testing.T) {
	source, err := os.ReadFile("inbound_runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	value := string(source)
	if !strings.Contains(value, `action.Action == "task_link_release"`) || !strings.Contains(value, `candidate.LinkState == "released"`) {
		t.Fatal("released card action replay is not idempotent")
	}
}

func TestNormalizeDesktopStateSortsPaginatedTurns(t *testing.T) {
	state := map[string]any{"turnHistory": map[string]any{"history": map[string]any{"entitiesByKey": map[string]any{
		"new": map[string]any{"turnId": "turn-new", "turnStartedAtMs": float64(2000)},
		"old": map[string]any{"turnId": "turn-old", "turnStartedAtMs": float64(1000)},
	}}}}
	turns := normalizeDesktopState(state)["turns"].([]any)
	if turns[0].(map[string]any)["id"] != "turn-old" || turns[1].(map[string]any)["id"] != "turn-new" {
		t.Fatalf("turns were not sorted: %#v", turns)
	}
}

func TestDesktopPendingInputRequiresUniqueExactQuestions(t *testing.T) {
	questions := []any{map[string]any{"id": "choice", "isSecret": false}}
	state := map[string]any{"requests": []any{
		map[string]any{"id": "request-a", "method": "item/tool/requestUserInput", "params": map[string]any{"turnId": "turn-1", "questions": questions}},
		map[string]any{"id": "request-b", "method": "item/tool/requestUserInput", "params": map[string]any{"turnId": "turn-2", "questions": questions}},
	}}
	id, turnID, _ := matchingDesktopPendingInput(state, "turn-1", []string{"choice"})
	if id != "request-a" || turnID != "turn-1" {
		t.Fatalf("unexpected request match: id=%q turn=%q", id, turnID)
	}
	if id, _, _ := matchingDesktopPendingInput(state, "", []string{"choice"}); id != "" {
		t.Fatalf("ambiguous request was accepted: %q", id)
	}
}
