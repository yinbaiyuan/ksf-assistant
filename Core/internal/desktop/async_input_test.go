package desktop

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func asyncState(status string) map[string]any {
	return map[string]any{"turns": []any{map[string]any{"id": "turn", "status": status, "items": []any{map[string]any{"id": "call", "type": "agentMessage", "delivery": "async", "questions": []any{map[string]any{"title": "具体指什么？", "options": []any{"产品", "实践"}}, map[string]any{"title": "展示什么成果？"}}}}}}}
}
func TestAsyncInputsTrackIndividualQuestionsAndAcceptedAnswers(t *testing.T) {
	state := asyncState("inProgress")
	pending := AsyncInputs(state, false)
	if len(pending) != 2 || pending[0].Questions[0]["asyncQuestionItemId"] != `["request_user_input_async","call",0]` {
		t.Fatal(pending)
	}
	ref, _ := RequestRefFromValue(pending[0].RequestID)
	target := UserInputTarget{State: asyncState("inProgress"), RequestID: ref}
	payload, _ := json.Marshal([]map[string]string{{"questionItemId": pending[0].Questions[0]["asyncQuestionItemId"].(string), "question": "具体指什么？", "answer": "产品"}})
	reply := map[string]any{"type": "steeringUserMessage", "status": "pending", "input": []any{map[string]any{"type": "text", "text": asyncReplyOpen + "\n" + string(payload) + "\n" + asyncReplyClose}}}
	turn := state["turns"].([]any)[0].(map[string]any)
	turn["items"] = append(turn["items"].([]any), reply)
	if len(AsyncInputs(state, false)) != 2 || asyncAnswerAccepted(state, target) {
		t.Fatal("queued answer treated as accepted")
	}
	reply["status"] = "accepted"
	if len(AsyncInputs(state, false)) != 1 || !asyncAnswerAccepted(state, target) {
		t.Fatal("accepted answer not consumed")
	}
	if _, ok := FindPendingUserInput(state, ref); ok {
		t.Fatal("stale question remained")
	}
}

func TestAsyncSubmissionUsesQuestionEnvelopeAndExactOwner(t *testing.T) {
	for _, status := range []string{"inProgress", "completed"} {
		t.Run(status, func(t *testing.T) {
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()
			client := New("test")
			client.connection = local
			client.started = true
			client.clientID = "bridge"
			state := asyncState(status)
			input := AsyncInputs(state, false)[0]
			ref, _ := RequestRefFromValue(input.RequestID)
			target := UserInputTarget{ThreadID: "thread", TurnID: "turn", OwnerClientID: "owner", State: state, RequestID: ref}
			done := make(chan error, 1)
			go func() {
				done <- client.SubmitBridgeUserInput(context.Background(), target, map[string]any{"answers": map[string]any{input.RequestID: map[string]any{"answers": []string{"豆包办公"}}}})
			}()
			frame := readTestFrame(t, remote)
			method := "thread-follower-steer-turn"
			if status == "completed" {
				method = "thread-follower-start-turn"
			}
			if frame["method"] != method || frame["targetClientId"] != "owner" {
				t.Fatal(frame)
			}
			raw, _ := json.Marshal(frame["params"])
			if !strings.Contains(string(raw), "send_user_message_question_reply") || !strings.Contains(string(raw), "questionItemId") {
				t.Fatal("plain followup instead of answer")
			}
			client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": frame["requestId"], "result": map[string]any{}}))
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("submit timeout")
			}
		})
	}
}
