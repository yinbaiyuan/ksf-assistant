package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAsyncQuestionProjectsOutsidePlanAndUsesBoundAnswerForm(t *testing.T) {
	state := map[string]any{"turns": []any{map[string]any{"id": "turn", "status": "running", "items": []any{map[string]any{"id": "async-call", "type": "agentMessage", "delivery": "async", "questions": []any{map[string]any{"title": "具体指什么？"}}}}}}}
	p := projectDesktopTaskLink(state)
	if p.TurnState != "waiting_input" || len(p.PendingQuestions) != 1 {
		t.Fatalf("missing async question: %#v", p)
	}
	link := TaskLink{ID: "LINK-1234567890123456", TaskKey: strings.Repeat("a", 20), LinkState: "active", TurnState: p.TurnState}
	link.SetExtraValue("pendingQuestions", p.PendingQuestions)
	link.SetExtraString("pendingQuestionRevision", strings.Repeat("b", 20))
	raw, _ := json.Marshal(taskLinkQuickReplyForm(link))
	if !strings.Contains(string(raw), `"answerFromInput":true`) || !strings.Contains(string(raw), `"action":"task_link_answer"`) {
		t.Fatal(string(raw))
	}
	if ref, _, _ := desktopPendingInputRaw(state, "other-turn"); len(ref) != 0 {
		t.Fatal("cross-turn prompt leaked")
	}
	answer := InboundCardAction{Action: "task_link_answer", QuestionRevision: strings.Repeat("b", 20), Value: map[string]any{"questionId": p.PendingQuestions[0]["id"], "answerFromInput": true}, FormValue: map[string]any{"followup": "豆包办公"}}
	if err := validateTaskCard(answer); err != nil {
		t.Fatal(err)
	}
	answer.FormValue["followup"] = ""
	if validateTaskCard(answer) == nil {
		t.Fatal("empty answer accepted")
	}
}

func TestAsyncReplyDisplaysOnlyHumanAnswer(t *testing.T) {
	turn := map[string]any{"items": []any{map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "<send_user_message_question_reply>\n[{\"questionItemId\":\"private-id\",\"question\":\"什么产品\",\"answer\":\"豆包办公\"}]\n</send_user_message_question_reply>"}}}}}
	if actual := desktopTurnUserInput(turn); actual != "豆包办公" {
		t.Fatal(actual)
	}
}
