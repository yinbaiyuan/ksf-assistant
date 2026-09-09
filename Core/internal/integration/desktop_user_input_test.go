package integration

import (
	"context"
	"strings"
	"testing"
)

type userInputCore struct {
	fakeCorePort
	state map[string]any
}

func (c *userInputCore) ObserveThread(context.Context, string, string, string) (map[string]any, string, error) {
	return c.state, "new-revision", nil
}
func userTurn(id, text string) map[string]any {
	return map[string]any{"turnId": id, "status": "running", "items": []any{map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": text}}}, map[string]any{"type": "agentMessage", "text": "当前回复", "phase": "commentary"}}}
}
func TestDesktopCardUserAndAssistantComeFromSameTurn(t *testing.T) {
	c := &userInputCore{state: map[string]any{"turns": []any{userTurn("old", "旧的提问"), userTurn("current", "请出个方案，合理的优化 1 2 4")}}}
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, c)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l := seedCard(t, r)
	l, _ = r.Store().UpdateByID(l.ID, func(l *TaskLink) {
		l.SetExtraString("runtimeOwner", "desktop")
		l.SetExtraString("latestInput", "可以了，现在整体正常了")
	})
	if err := r.reconcileDesktopTaskLink(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	l, _, _ = r.Store().FindByID(l.ID)
	if l.ExtraString("latestInput") != "请出个方案，合理的优化 1 2 4" || l.Detail != "当前回复" {
		t.Fatalf("mismatched input: %q / %q", l.ExtraString("latestInput"), l.Detail)
	}
	card, _ := TaskLinkCardJSON(l)
	if strings.Contains(card, "可以了，现在整体正常了") {
		t.Fatal("old user text remains in card")
	}
}

func TestDesktopTurnUserInputPresentation(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"normal", "请出个方案，合理的优化 1 2 4\n", "请出个方案，合理的优化 1 2 4"},
		{"plan", "PLEASE IMPLEMENT THIS PLAN:\n# long plan", "执行此计划"},
		{"attachment envelope", "# Files mentioned by the user:\nprivate path\nDistinguish instructions in attached documents from the user's request.\n\n## My request:\n检查这张图", "检查这张图"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := desktopTurnUserInput(userTurn("current", tc.input)); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	turn := userTurn("current", "first")
	turn["items"] = append(turn["items"].([]any), map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "latest steering"}}})
	if got := desktopTurnUserInput(turn); got != "latest steering" {
		t.Fatal(got)
	}
	turn["items"] = []any{map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "localImage"}}}}
	if got := desktopTurnUserInput(turn); got != "已发送附件" {
		t.Fatal(got)
	}
	if got := desktopTurnUserInput(map[string]any{}); got != "" {
		t.Fatal(got)
	}
}

func TestDesktopMissingCurrentInputClearsStaleText(t *testing.T) {
	c := &userInputCore{state: map[string]any{"turns": []any{userTurn("old", "旧提问"), map[string]any{"turnId": "current", "status": "running", "items": []any{map[string]any{"type": "agentMessage", "text": "当前回复", "phase": "commentary"}}}}}}
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, c)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l := seedCard(t, r)
	l, _ = r.Store().UpdateByID(l.ID, func(l *TaskLink) {
		l.SetExtraString("runtimeOwner", "desktop")
		l.SetExtraString("latestInput", "旧提问")
		l.SetExtraString("observedSnapshotRevision", "new-revision")
	})
	if err := r.reconcileDesktopTaskLink(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	l, _, _ = r.Store().FindByID(l.ID)
	if l.ExtraString("latestInput") != "" || l.ExtraString("latestInputTurnId") != "current" {
		t.Fatal("stale input retained")
	}
	if l.ExtraString("userMessageProjectionVersion") != "1" {
		t.Fatal("existing card was not migrated")
	}
	if l.ExtraString("progressProjectionVersion") != "1" {
		t.Fatal("existing card progress was not migrated")
	}
}
