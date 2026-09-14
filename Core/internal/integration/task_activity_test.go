package integration

import (
	"context"
	"strings"
	"testing"
)

func TestActivityFollowsPublicProgressAndToolTransitions(t *testing.T) {
	items := []any{map[string]any{"type": "reasoning", "summary": []any{"**Implementing activity/read revision**\n\nLong summary body must not be forwarded."}, "content": []any{"private reasoning"}}}
	check := func(want string) {
		t.Helper()
		a := projectTaskActivity(map[string]any{"id": "turn", "items": items})
		if got := taskActivityText(activityTestLink(a, "running")); got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
	check("Implementing activity/read revision")
	items = append(items, map[string]any{"type": "commandExecution", "status": "inProgress", "commandActions": []any{map[string]any{"type": "read", "path": "/private/report.md"}}})
	check("📖 正在读取「report.md」")
	items[1].(map[string]any)["status"] = "completed"
	check("Implementing activity/read revision")
	items = append(items, map[string]any{"type": "reasoning", "summary": []any{"**Searching app-primary header**"}})
	check("Searching app-primary header")
	items = []any{map[string]any{"type": "reasoning", "content": []any{"private reasoning"}}}
	check("正在处理")
}

func TestPublicProgressTitleBoundary(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"**Extracting titles from hits**\n\nDo not send body", "Extracting titles from hits"},
		{"**partial title", ""},
		{"Searching app-primary header", "Searching app-primary header"},
		{"### Checking layout\n\nExcluded body", "Checking layout"},
		{"ordinary multiline\nbody", ""},
		{"**<at id=all>**", "&lt;at id=all&gt;"},
	} {
		if got := publicProgressTitle(tc.raw); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}

func TestActivityCompletedCommandsDoNotEraseTitleOrActiveTool(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		items := []any{
			map[string]any{"type": "reasoning", "summary": []any{"Checking all 18 pages"}},
			map[string]any{"type": "commandExecution", "status": status},
		}
		a := projectTaskActivity(map[string]any{"id": "one", "items": items})
		if got := taskActivityText(activityTestLink(a, "running")); got != "Checking all 18 pages" {
			t.Fatal(got)
		}
		partial := mergeTaskActivity(activityTestLink(a, "running"), projectTaskActivity(map[string]any{"id": "one"}))
		if partial.Title != a.Title {
			t.Fatal("partial snapshot lost title")
		}
		next := mergeTaskActivity(activityTestLink(a, "running"), projectTaskActivity(map[string]any{"id": "two"}))
		if next.Title != "" {
			t.Fatal("title crossed turn boundary")
		}
		items = append([]any{map[string]any{"type": "commandExecution", "status": "inProgress"}}, items...)
		if got := projectTaskActivity(map[string]any{"id": "one", "items": items}).Action; got != "正在运行命令" {
			t.Fatal("completed command erased concurrent action", got)
		}
	}
}

func TestOldActivityProjectionMigratesAtSameSnapshotRevision(t *testing.T) {
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, _ := r.links.Upsert("thread", "title", "", "me")
	snapshot := map[string]any{"turns": []any{map[string]any{"id": "turn", "status": "running", "items": []any{map[string]any{"type": "contextCompaction", "completed": false, "source": "automatic"}}}}}
	if err := r.applyDesktopTaskSnapshot(context.Background(), l, snapshot, "10"); err != nil {
		t.Fatal(err)
	}
	l, _, _ = r.links.FindByID(l.ID)
	// Simulate an old stored projection with otherwise identical content.
	l, err = r.links.UpdateActiveByID(l.ID, func(v *TaskLink) {
		v.SetExtraString("activityProjectionVersion", "3")
		v.SetExtraValue("taskActivity", taskActivity{TurnID: "turn", Title: "Old progress"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.applyDesktopTaskSnapshot(context.Background(), l, snapshot, "10"); err != nil {
		t.Fatal(err)
	}
	l, _, _ = r.links.FindByID(l.ID)
	if l.ExtraString("activityProjectionVersion") != activityProjectionVersion {
		t.Fatal("same-revision migration skipped")
	}
	if got := taskActivityText(l); got != "正在优化对话" {
		t.Fatalf("old card footer was not reprojected: %q", got)
	}
}

func TestPublicHeadingOnlySnapshotUpdatesFooter(t *testing.T) {
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, _ := r.links.Upsert("thread", "title", "", "me")
	for i, title := range []string{"Extracting titles from hits", "Searching app-primary header"} {
		snapshot := map[string]any{"turns": []any{map[string]any{"id": "turn", "status": "running", "items": []any{
			map[string]any{"type": "agentMessage", "phase": "commentary", "text": "Stable response"},
			map[string]any{"type": "reasoning", "summary": []any{"**" + title + "**\n\nExcluded summary body"}, "content": []any{"Excluded private content"}},
		}}}}
		if err := r.applyDesktopTaskSnapshot(context.Background(), l, snapshot, string(rune('1'+i))); err != nil {
			t.Fatal(err)
		}
		l, _, _ = r.links.FindByID(l.ID)
		card, err := TaskLinkCardJSON(l)
		if err != nil {
			t.Fatal(err)
		}
		body, footer, input := strings.Index(card, "Stable response"), strings.Index(card, title), strings.Index(card, "补充或修正")
		if body < 0 || footer <= body || input <= footer || strings.Contains(card, "Excluded") {
			t.Fatalf("incorrect public footer: %s", card)
		}
	}
	if got := taskActivityText(activityTestLink(projectTaskActivity(map[string]any{"id": "next"}), "running")); got != "正在处理" {
		t.Fatal(got)
	}
}

func TestActivityFooterBelowReplyBeforeInput(t *testing.T) {
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, _ := r.links.Upsert("thread", "title", "", "me")
	snapshot := map[string]any{"turns": []any{map[string]any{"id": "turn", "status": "running", "items": []any{
		map[string]any{"id": "reply", "type": "agentMessage", "phase": "commentary", "text": "回答正文"},
		map[string]any{"id": "edit", "type": "fileChange", "status": "completed", "changes": []any{map[string]any{"path": "src/a.go"}, map[string]any{"path": "src/./a.go"}, map[string]any{"path": "src/b.go"}}},
		map[string]any{"id": "read", "type": "commandExecution", "status": "inProgress", "command": "secret-command", "aggregatedOutput": "private-output", "commandActions": []any{map[string]any{"type": "read", "path": "/private/work/项目汇报.md"}}},
	}}}}
	if err := r.applyDesktopTaskSnapshot(context.Background(), l, snapshot, "1"); err != nil {
		t.Fatal(err)
	}
	l, _, _ = r.links.FindByID(l.ID)
	card, err := TaskLinkCardJSON(l)
	if err != nil {
		t.Fatal(err)
	}
	body, activity, input := strings.Index(card, "回答正文"), strings.Index(card, "正在读取「项目汇报.md」"), strings.Index(card, "补充或修正")
	if body < 0 || activity <= body || input <= activity || !strings.Contains(card, "已修改 2 个文件") {
		t.Fatalf("missing or misplaced footer: %s", card)
	}
	for _, secret := range []string{"/private/work", "secret-command", "private-output", "正在做：", "本轮变更："} {
		if strings.Contains(card, secret) {
			t.Fatalf("unsafe/noisy footer: %s", secret)
		}
	}
}

func activityTestLink(activity taskActivity, state string) TaskLink {
	l := TaskLink{LinkState: "active", TurnState: state}
	l.SetExtraString("latestInputTurnId", activity.TurnID)
	l.SetExtraValue("taskActivity", activity)
	return l
}

func TestActivityStatsDeduplicateAndDoNotSumRepeatedEdits(t *testing.T) {
	edit := func(id, status, p, diff string) any {
		return map[string]any{"id": id, "type": "fileChange", "status": status, "changes": []any{map[string]any{"path": p, "diff": diff}}}
	}
	turn := map[string]any{"id": "one", "items": []any{edit("a", "completed", "src/a.go", "--- a\n+++ b\n@@ -1 +1,2 @@\n-old\n+new\n+second\n"), edit("bad", "failed", "b.go", "@@ -0,0 +1 @@\n+not applied")}}
	a := projectTaskActivity(turn)
	if got := taskActivityText(activityTestLink(a, "completed")); got != "已完成 · 已修改 1 个文件 · +2 −1" {
		t.Fatal(got)
	}
	same := mergeTaskActivity(activityTestLink(a, "running"), projectTaskActivity(turn))
	if got := taskActivityText(activityTestLink(same, "completed")); !strings.Contains(got, "+2 −1") {
		t.Fatal("snapshot replay changed totals", got)
	}
	turn["items"] = []any{edit("b", "completed", "src/a.go", "@@ -1 +1 @@\n-new\n+again")}
	next := mergeTaskActivity(activityTestLink(a, "running"), projectTaskActivity(turn))
	if got := taskActivityText(activityTestLink(next, "completed")); got != "已完成 · 已修改 1 个文件" {
		t.Fatal("repeated edits reported as net diff", got)
	}
	turn["id"] = "two"
	turn["items"] = []any{}
	if got := taskActivityText(activityTestLink(mergeTaskActivity(activityTestLink(next, "running"), projectTaskActivity(turn)), "running")); strings.Contains(got, "修改") {
		t.Fatal("previous turn leaked", got)
	}
}

func TestActivityRecognizesExplicitVisualizationAndCompaction(t *testing.T) {
	for _, tc := range []struct {
		item map[string]any
		want string
	}{
		{map[string]any{"type": "agentMessage", "phase": "commentary", "text": "visualize{\"path\":\"/private/chart.html\"}"}, "已创建可视化"},
		{map[string]any{"type": "agentMessage", "phase": "analysis", "text": "visualize{\"path\":\"/private/chart.html\"}"}, "正在处理"},
		{map[string]any{"type": "contextCompaction", "status": "inProgress"}, "正在压缩上下文"},
		{map[string]any{"type": "contextCompaction", "status": "completed"}, "上下文已压缩"},
		{map[string]any{"type": "contextCompaction"}, "上下文压缩"},
	} {
		a := projectTaskActivity(map[string]any{"id": "turn", "items": []any{tc.item}})
		if got := taskActivityText(activityTestLink(a, "running")); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}

func TestDesktopCompactionLifecycleOverridesOldProgressTitle(t *testing.T) {
	for _, tc := range []struct {
		completed    bool
		source, want string
	}{
		{false, "automatic", "正在优化对话"},
		{true, "automatic", "已优化对话"},
		{false, "manual", "正在压缩上下文"},
		{true, "manual", "上下文已压缩"},
	} {
		items := []any{
			map[string]any{"type": "reasoning", "summary": []any{"**Old progress**"}},
			map[string]any{"type": "contextCompaction", "completed": tc.completed, "source": tc.source},
		}
		projection := projectDesktopTaskLink(map[string]any{"turns": []any{map[string]any{"id": "turn", "status": "inProgress", "items": items}}})
		link := activityTestLink(projection.Activity, "running")
		if got := taskActivityText(link); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
		if tc.completed {
			items = append(items, map[string]any{"type": "reasoning", "summary": []any{"**Next progress**"}})
			next := projectTaskActivity(map[string]any{"id": "turn", "items": items})
			if got := taskActivityText(activityTestLink(mergeTaskActivity(link, next), "running")); got != "Next progress" {
				t.Fatal(got)
			}
		}
	}
}

func TestActivityFileNamesHideSensitivePathsAndMarkup(t *testing.T) {
	for _, name := range []string{"/private/.env", "C:\\secret\\credentials.json", "/x/id_rsa", "https://private/file.md"} {
		if got := publicActivityFileName(name); got != "" {
			t.Fatal("sensitive name exposed", got)
		}
	}
	if got := publicActivityFileName("/private/<at>.md"); strings.Contains(got, "<at>") || strings.Contains(got, "private") {
		t.Fatal("markup or path leaked", got)
	}
	a := projectTaskActivity(map[string]any{"id": "turn", "items": []any{map[string]any{"type": "fileChange", "status": "inProgress", "changes": []any{map[string]any{"path": "not-written.go"}}}}})
	if got := taskActivityText(activityTestLink(a, "running")); got != "正在编辑文件" {
		t.Fatal("in-progress edit counted as modified", got)
	}
}
