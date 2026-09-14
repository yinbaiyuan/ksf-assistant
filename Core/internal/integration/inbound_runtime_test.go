package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
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

func TestTaskLinkAnswerRejectsStaleQuestionBeforeMutatingCard(t *testing.T) {
	link := TaskLink{Extra: map[string]json.RawMessage{}}
	link.SetExtraValue("pendingQuestions", []map[string]any{{"id": "current-question"}})
	if taskLinkHasPendingQuestion(link, "old-question") {
		t.Fatal("stale card question was accepted")
	}
	if !taskLinkHasPendingQuestion(link, "current-question") {
		t.Fatal("current card question was rejected")
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

func TestDesktopPendingInputByIDCannotDriftToAnotherRequest(t *testing.T) {
	questions := []any{map[string]any{"id": "choice", "isSecret": false}}
	state := map[string]any{"requests": []any{
		map[string]any{"id": "request-old", "method": "item/tool/requestUserInput", "params": map[string]any{"turnId": "turn-1", "questions": questions}},
		map[string]any{"id": "request-current", "method": "item/tool/requestUserInput", "params": map[string]any{"turnId": "turn-1", "questions": questions}},
	}}
	id, turnID, _ := matchingDesktopPendingInputByID(state, "turn-1", []string{"choice"}, "request-current")
	if id != "request-current" || turnID != "turn-1" {
		t.Fatalf("exact request was not selected: id=%q turn=%q", id, turnID)
	}
	if id, _, _ := matchingDesktopPendingInputByID(state, "turn-1", []string{"choice"}, "request-missing"); id != "" {
		t.Fatalf("missing request drifted to another candidate: %q", id)
	}
}

func TestDesktopPendingInputAllowsMissingTurnIDWhenQuestionsMatch(t *testing.T) {
	state := map[string]any{"requests": []any{
		map[string]any{
			"id": "request-without-turn", "method": "item/tool/requestUserInput",
			"params": map[string]any{"questions": []any{map[string]any{"id": "choice", "isSecret": false}}},
		},
	}}
	id, turnID, _ := matchingDesktopPendingInput(state, "turn-1", []string{"choice"})
	if id != "request-without-turn" || turnID != "" {
		t.Fatalf("missing-turn request was not accepted safely: id=%q turn=%q", id, turnID)
	}
}

func TestDesktopRequestPendingTracksExactRequest(t *testing.T) {
	state := map[string]any{"requests": []any{
		map[string]any{"id": "request-a", "method": "item/tool/requestUserInput", "params": map[string]any{"questions": []any{map[string]any{"id": "choice"}}}},
	}}
	if !desktopRequestPending(state, "request-a") {
		t.Fatal("exact request was not found")
	}
	if desktopRequestPending(state, "request-b") {
		t.Fatal("unrelated request was treated as pending")
	}
}

func TestDesktopAnswerSubmissionSuppressesOnlyTheSameStaleRequest(t *testing.T) {
	link := TaskLink{Extra: map[string]json.RawMessage{}}
	link.SetExtraString("pendingInputAnswerRequestID", "request-1")
	link.SetExtraString("pendingInputAnswerTurnID", "turn-1")
	link.SetExtraString("pendingInputAnsweredAt", time.Now().UTC().Format(time.RFC3339Nano))
	projection := desktopTaskProjection{TurnID: "turn-1", TurnState: "waiting_input", PendingRequestID: "request-1"}
	if !desktopAnswerSubmissionPending(link, projection) {
		t.Fatal("fresh answer submission did not suppress its stale Desktop snapshot")
	}
	projection.PendingRequestID = "request-2"
	if desktopAnswerSubmissionPending(link, projection) {
		t.Fatal("a different pending request was incorrectly suppressed")
	}
}

func TestProjectDesktopTaskLinkTracksRunningDesktopTurn(t *testing.T) {
	snapshot := map[string]any{
		"thread": map[string]any{"status": map[string]any{"type": "active"}},
		"turns": []any{map[string]any{
			"id": "turn-desktop", "status": map[string]any{"type": "running"},
			"items": []any{map[string]any{"type": "agentMessage", "phase": "commentary", "text": "正在核对生产链路。"}},
		}},
	}
	projection := projectDesktopTaskLink(snapshot)
	if projection.TurnID != "turn-desktop" || projection.TurnState != "running" || projection.TurnOwner != "desktop" || projection.Detail != "正在核对生产链路。" {
		t.Fatalf("unexpected running projection: %#v", projection)
	}
	link := TaskLink{TurnState: "completed", TurnOwner: "none", ActionRequired: "none"}
	if !desktopProjectionRequiresSync(link, projection) {
		t.Fatal("new desktop turn was not marked for card synchronization")
	}
}

func TestDesktopTurnProgressAccumulatesPublicMessages(t *testing.T) {
	turn := map[string]any{"items": []any{
		map[string]any{"id": "one", "type": "agentMessage", "phase": "commentary", "text": "第一段"},
		map[string]any{"id": "private", "type": "agentMessage", "phase": "analysis", "text": "内部推理"},
		map[string]any{"id": "tool", "type": "commandExecution", "text": "工具输出"},
		map[string]any{"id": "two", "type": "agentMessage", "phase": "commentary", "text": "第二段"},
		map[string]any{"id": "final", "type": "agentMessage", "phase": "final_answer", "text": "最终回答"},
	}}
	segments := desktopTurnProgressSegments(turn)
	if len(segments) != 3 || segments[0].Text != "第一段" || segments[1].Text != "第二段" || segments[2].Text != "最终回答" {
		t.Fatalf("unexpected public progress: %#v", segments)
	}
	if got := desktopTurnProgress(turn); got != "第一段\n\n第二段\n\n最终回答" {
		t.Fatalf("progress = %q", got)
	}
}

func TestProgressSegmentMergeUsesSnapshotOverlapWithoutDuplication(t *testing.T) {
	existing := []taskProgressSegment{{Text: "第一段"}, {Text: "第二段"}}
	incoming := []taskProgressSegment{{Text: "第一段"}, {Text: "第二段"}, {Text: "第三段"}}
	merged := mergeTaskProgressSegments(existing, incoming)
	if len(merged) != 3 || merged[2].Text != "第三段" {
		t.Fatalf("full snapshot duplicated progress: %#v", merged)
	}
	merged = mergeTaskProgressSegments(merged, []taskProgressSegment{{Text: "第三段"}})
	if len(merged) != 3 {
		t.Fatalf("partial snapshot duplicated latest progress: %#v", merged)
	}
	merged = mergeTaskProgressSegments(merged, []taskProgressSegment{{ID: "live", Text: "草稿"}})
	merged = mergeTaskProgressSegments(merged, []taskProgressSegment{{ID: "live", Text: "草稿已扩展"}})
	if merged[len(merged)-1].Text != "草稿已扩展" {
		t.Fatalf("stable item update was not replaced: %#v", merged)
	}
	evicted := []taskProgressSegment{{Text: "第三段"}, {Text: "第四段"}}
	fullSnapshot := []taskProgressSegment{{Text: "第一段"}, {Text: "第二段"}, {Text: "第三段"}, {Text: "第四段"}, {Text: "第五段"}}
	merged = mergeTaskProgressSegments(evicted, fullSnapshot)
	if len(merged) != 3 || merged[0].Text != "第三段" || merged[2].Text != "第五段" {
		t.Fatalf("evicted progress re-entered the queue: %#v", merged)
	}
}

func TestDesktopProjectionRejectsOlderOrIncompleteTurnRegression(t *testing.T) {
	link := TaskLink{TurnState: "running", ActiveTurnID: "current", Extra: map[string]json.RawMessage{}}
	link.SetExtraString("latestInputTurnId", "current")
	oldOnly := map[string]any{"turns": []any{map[string]any{"id": "old", "status": "failed"}}}
	if desktopProjectionCanAdvance(link, oldOnly, projectDesktopTaskLink(oldOnly)) {
		t.Fatal("an incomplete old failure replaced the current running turn")
	}
	ordered := map[string]any{"turns": []any{
		map[string]any{"id": "old", "status": "failed"},
		map[string]any{"id": "current", "status": "interrupted"},
		map[string]any{"id": "new", "status": "running"},
	}}
	if !desktopProjectionCanAdvance(link, ordered, projectDesktopTaskLink(ordered)) {
		t.Fatal("an ordered newer turn was rejected")
	}
}

func TestInterruptedTurnAllowsTimestampedNewRunningTurn(t *testing.T) {
	link := TaskLink{TurnState: "interrupted", Extra: map[string]json.RawMessage{}}
	link.SetExtraString("latestInputTurnId", "stopped")
	link.SetExtraValue("desktopTurnStartedAtMs", int64(100))
	newOnly := map[string]any{
		"thread": map[string]any{"status": map[string]any{"type": "active"}},
		"turns":  []any{map[string]any{"id": "new", "status": "running", "turnStartedAtMs": float64(200)}},
	}
	if !desktopProjectionCanAdvance(link, newOnly, projectDesktopTaskLink(newOnly)) {
		t.Fatal("a newer running turn after interruption was rejected")
	}
}

func TestProjectDesktopTaskLinkTracksCompletedResultOnce(t *testing.T) {
	snapshot := map[string]any{"turns": []any{map[string]any{
		"id": "turn-complete", "status": map[string]any{"type": "completed"},
		"items": []any{map[string]any{"type": "agentMessage", "phase": "final_answer", "text": "修复已经完成。"}},
	}}}
	projection := projectDesktopTaskLink(snapshot)
	if projection.TurnState != "completed" || projection.Detail != "修复已经完成。" || projection.TurnOwner != "none" {
		t.Fatalf("unexpected completed projection: %#v", projection)
	}
	link := TaskLink{TurnState: "completed", TurnOwner: "none", ActionRequired: "none", Phase: "已完成", Detail: "修复已经完成。", Extra: map[string]json.RawMessage{}}
	link.SetExtraString("lastDeliveredTurnId", "turn-complete")
	link.SetExtraString("latestInputTurnId", "turn-complete")
	if desktopProjectionRequiresSync(link, projection) {
		t.Fatal("an already synchronized terminal turn would be patched repeatedly")
	}
}

func TestProjectDesktopTaskLinkPreservesWaitingInputAndPlanStates(t *testing.T) {
	waiting := projectDesktopTaskLink(map[string]any{
		"turns": []any{map[string]any{"id": "turn-wait", "status": map[string]any{"type": "running"}}},
		"requests": []any{map[string]any{
			"id": "request-1", "method": "item/tool/requestUserInput",
			"params": map[string]any{"turnId": "turn-wait", "questions": []any{map[string]any{"id": "choice", "question": "继续吗？", "isSecret": false}}},
		}},
	})
	if waiting.TurnState != "waiting_input" || waiting.ActionRequired != "feishu" || len(waiting.PendingQuestions) != 1 {
		t.Fatalf("unexpected waiting-input projection: %#v", waiting)
	}

	plan := projectDesktopTaskLink(map[string]any{
		"turns":                     []any{map[string]any{"id": "turn-plan", "status": map[string]any{"type": "completed"}}},
		"pendingPlanImplementation": map[string]any{"turnId": "turn-plan", "planContent": "1. 完成迁移"},
	})
	if plan.TurnState != "plan_ready" || plan.PendingPlan.TurnID != "turn-plan" || plan.ActionRequired != "feishu" {
		t.Fatalf("unexpected plan projection: %#v", plan)
	}
}

func TestWaitingInputRequestIdentityTriggersCardSynchronization(t *testing.T) {
	questions := []map[string]any{{"id": "choice", "question": "请选择"}}
	link := TaskLink{TurnState: "waiting_input", TurnOwner: "desktop", ActionRequired: "feishu", ActiveTurnID: "turn-1", Phase: "等待输入", Detail: "Codex 等待你的选择。", Extra: map[string]json.RawMessage{}}
	link.SetExtraString("pendingQuestionRequestID", "request-old")
	link.SetExtraString("pendingQuestionRevision", PendingQuestionRevision("turn-1", "request-old", questions))
	next := desktopTaskProjection{
		TurnID: "turn-1", TurnState: "waiting_input", TurnOwner: "desktop", ActionRequired: "feishu",
		Phase: "等待输入", Detail: "Codex 等待你的选择。", PendingQuestions: questions,
		PendingRequestID: "request-new", PendingQuestionRevision: PendingQuestionRevision("turn-1", "request-new", questions),
	}
	if !desktopProjectionRequiresSync(link, next) {
		t.Fatal("a new Desktop request with the same question did not refresh the card")
	}
}

func TestServerRequestIDPreservesStringAndNumericIDs(t *testing.T) {
	if got := serverRequestID(json.RawMessage(`"request-1"`)); got != "request-1" {
		t.Fatalf("string request id = %q", got)
	}
	if got := serverRequestID(json.RawMessage(`9007199254740993`)); got != "9007199254740993" {
		t.Fatalf("numeric request id lost precision: %q", got)
	}
	if got := serverRequestID(json.RawMessage(`null`)); got != "" {
		t.Fatalf("null request id = %q", got)
	}
	if got := serverRequestID(json.RawMessage(`1.5`)); got != "" {
		t.Fatalf("non-integer request id = %q", got)
	}
}

func TestDesktopRequestIDRawPreservesIntegerBeyondInt64(t *testing.T) {
	raw := desktopRequestIDRaw(json.Number("900719925474099312345"))
	if string(raw) != "900719925474099312345" {
		t.Fatalf("large Desktop request ID changed: %q", raw)
	}
	if value := desktopRequestIDRaw(json.Number("1.5")); len(value) != 0 {
		t.Fatalf("non-integer Desktop request ID accepted: %q", value)
	}
}
