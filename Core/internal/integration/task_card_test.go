package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskLinkCardExposesFrozenControlsByState(t *testing.T) {
	link := TaskLink{ID: "LINK-0123456789ABCDEF", TaskKey: "0123456789abcdef0123", Title: "迁移任务", ProjectName: "KSFAssistant", LinkState: "active", TurnState: "running", TurnOwner: "bridge", Phase: "执行"}
	link.SetExtraString("latestInput", "请继续迁移")
	link.SetExtraString("activeTurnMode", "default")
	encoded, err := TaskLinkCardJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(encoded), &card); err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{"text_tag_list", "默认", "迁移任务", "飞书控制", "请继续迁移", "task_link_interrupt", "task_link_release", "task_link_followup", "linkId", "LINK-0123456789ABCDEF", "followup", "form_action_type"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("running card is missing %s", expected)
		}
	}
	link.TurnState, link.PendingPlanRevision = "plan_ready", "abcdef0123456789abcd"
	encoded, err = TaskLinkCardJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "task_link_implement_plan") || strings.Contains(encoded, "task_link_interrupt") {
		t.Fatalf("unexpected plan-ready card: %s", encoded)
	}
}

func TestPendingQuestionRevisionBindsDesktopRequest(t *testing.T) {
	questions := []map[string]any{{"id": "choice", "question": "请选择", "options": []any{map[string]any{"label": "A"}}}}
	first := PendingQuestionRevision("turn-1", "request-1", questions)
	if first == "" || first != PendingQuestionRevision("turn-1", "request-1", questions) {
		t.Fatalf("question revision is not stable: %q", first)
	}
	if first == PendingQuestionRevision("turn-1", "request-2", questions) {
		t.Fatal("different Desktop requests shared a question revision")
	}
}

func TestPendingQuestionRevisionScopesTaskOwnerAndTypedRequest(t *testing.T) {
	questions := []map[string]any{{"id": "choice", "question": "Pick one"}}
	base := PendingQuestionRevisionScoped("task-a", "turn-1", "owner-a", json.RawMessage(`7`), questions)
	for name, revision := range map[string]string{
		"task":  PendingQuestionRevisionScoped("task-b", "turn-1", "owner-a", json.RawMessage(`7`), questions),
		"turn":  PendingQuestionRevisionScoped("task-a", "turn-2", "owner-a", json.RawMessage(`7`), questions),
		"owner": PendingQuestionRevisionScoped("task-a", "turn-1", "owner-b", json.RawMessage(`7`), questions),
		"type":  PendingQuestionRevisionScoped("task-a", "turn-1", "owner-a", json.RawMessage(`"7"`), questions),
	} {
		if revision == base {
			t.Fatalf("%s was not bound into question revision", name)
		}
	}
}

func TestWaitingInputCardCarriesRevisionAndDoesNotRenderSubmissionError(t *testing.T) {
	questions := []map[string]any{{
		"id": "choice", "question": "请选择", "options": []any{map[string]any{"label": "A", "description": "第一项"}},
	}}
	revision := PendingQuestionRevision("turn-1", "request-1", questions)
	link := TaskLink{ID: "LINK-0123456789ABCDEF", TaskKey: "0123456789abcdef0123", Title: "测试", ProjectName: "KSFAssistant", LinkState: "active", TurnState: "waiting_input", TurnOwner: "desktop", ActionRequired: "feishu", ActiveTurnID: "turn-1"}
	link.SetExtraValue("pendingQuestions", questions)
	link.SetExtraString("pendingQuestionRequestID", "request-1")
	link.SetExtraString("pendingQuestionRevision", revision)
	link.SetExtraString("inputSubmissionError", "提交未生效，请重试。")
	encoded, err := TaskLinkCardJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, `"questionRevision":"`+revision+`"`) {
		t.Fatalf("card button is not bound to the pending request: %s", encoded)
	}
	if strings.Contains(encoded, "提交未生效") {
		t.Fatalf("internal submission error leaked into the shared card: %s", encoded)
	}
}

func TestRunningCardKeepsCurrentTurnProgressSegmentsInOrder(t *testing.T) {
	link := TaskLink{ID: "LINK-0123456789ABCDEF", TaskKey: "0123456789abcdef0123", ProjectName: "KSFAssistant", LinkState: "active", TurnState: "running", TurnOwner: "desktop", ActiveTurnID: "turn-1"}
	link.SetExtraString("latestInput", "检查任务卡片")
	link.SetExtraString("latestInputTurnId", "turn-1")
	link.SetExtraString("progressTurnId", "turn-1")
	link.SetExtraValue("progressSegments", []taskProgressSegment{{ID: "one", Text: "第一段进展"}, {ID: "two", Text: "第二段进展"}, {ID: "three", Text: "第三段进展"}})

	encoded, err := TaskLinkCardJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Index(encoded, "第一段进展")
	second := strings.Index(encoded, "第二段进展")
	third := strings.Index(encoded, "第三段进展")
	if first < 0 || second <= first || third <= second {
		t.Fatalf("progress segments are not ordered: %s", encoded)
	}
}

func TestRunningCardEvictsOldestWholeSegmentsWithinRequestBudget(t *testing.T) {
	link := TaskLink{ID: "LINK-0123456789ABCDEF", TaskKey: "0123456789abcdef0123", ProjectName: "KSFAssistant", LinkState: "active", TurnState: "running", TurnOwner: "desktop", ActiveTurnID: "turn-1"}
	link.SetExtraString("latestInput", "检查中文与 JSON 转义：\"卡片\"")
	link.SetExtraString("latestInputTurnId", "turn-1")
	link.SetExtraString("progressTurnId", "turn-1")
	segments := []taskProgressSegment{}
	for index := 0; index < 8; index++ {
		segments = append(segments, taskProgressSegment{ID: string(rune('a' + index)), Text: strings.Repeat("中", 1800) + "-segment-" + string(rune('a'+index))})
	}
	link.SetExtraValue("progressSegments", segments)

	encoded, err := TaskLinkCardJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskLinkCardRequestBytes(encoded); got > feishuCardSafeRequestBytes {
		t.Fatalf("request size = %d, limit = %d", got, feishuCardSafeRequestBytes)
	}
	if strings.Contains(encoded, "-segment-a") || !strings.Contains(encoded, "-segment-h") {
		t.Fatalf("FIFO eviction did not preserve newest segments: %s", encoded)
	}
}

func TestRunningCardKeepsTailWhenNewestSegmentAloneExceedsBudget(t *testing.T) {
	link := TaskLink{ID: "LINK-0123456789ABCDEF", TaskKey: "0123456789abcdef0123", ProjectName: "KSFAssistant", LinkState: "active", TurnState: "running", TurnOwner: "desktop", ActiveTurnID: "turn-1"}
	link.SetExtraString("latestInput", "检查超长进展")
	link.SetExtraString("latestInputTurnId", "turn-1")
	link.SetExtraString("progressTurnId", "turn-1")
	link.SetExtraValue("progressSegments", []taskProgressSegment{{ID: "latest", Text: "最早开头" + strings.Repeat("中", 20000) + "最新结尾"}})

	encoded, err := TaskLinkCardJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskLinkCardRequestBytes(encoded); got > feishuCardSafeRequestBytes {
		t.Fatalf("request size = %d, limit = %d", got, feishuCardSafeRequestBytes)
	}
	if !strings.Contains(encoded, "较早内容已省略") || strings.Contains(encoded, "最早开头") || !strings.Contains(encoded, "最新结尾") {
		t.Fatalf("oversized newest segment was not tail-truncated: %s", encoded)
	}
}
