package feishu

import (
	"encoding/json"
	"testing"
)

func TestTaskLinkCardExposesFrozenControlsByState(t *testing.T) {
	link := TaskLink{TaskKey: "0123456789abcdef0123", Title: "迁移任务", ProjectName: "CodexAssistant", LinkState: "active", TurnState: "running", TurnOwner: "bridge", Phase: "执行"}
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
	for _, expected := range []string{"text_tag_list", "默认", "迁移任务", "飞书控制", "请继续迁移", "task_link_interrupt", "task_link_release", "task_link_followup", "followup", "form_action_type"} {
		if !containsText(text, expected) {
			t.Fatalf("running card is missing %s", expected)
		}
	}
	link.TurnState, link.PendingPlanRevision = "plan_ready", "abcdef0123456789abcd"
	encoded, err = TaskLinkCardJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(encoded, "task_link_implement_plan") || containsText(encoded, "task_link_interrupt") {
		t.Fatalf("unexpected plan-ready card: %s", encoded)
	}
}
