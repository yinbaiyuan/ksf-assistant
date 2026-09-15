package domain

import (
	"fmt"
	"testing"
	"time"
)

func TestConnectedCompletedUnassignedTaskRemainsWithoutPin(t *testing.T) {
	threads := []CodexThread{{ID: "loose", CWD: "/ksf", Path: pointer("/sessions/loose.jsonl"), Name: pointer("已完成任务"), CreatedAt: 1, UpdatedAt: 2}}
	keys := map[string]bool{PublicTaskKey("loose"): true}
	items := BuildProjectDashboard(nil, threads, nil, nil, nil, nil, nil, time.Now(), keys)
	if len(items) != 1 || items[0].Kind != "unassigned" || items[0].IsPinned || len(items[0].Tasks) != 1 || items[0].Tasks[0].Classification != "completed" {
		t.Fatalf("lost linked task: %#v", items)
	}
	if len(SelectHomeProjects(items, keys)) != 1 {
		t.Fatal("linked group hidden")
	}
	if len(SelectHomeProjects(items)) != 0 {
		t.Fatal("disconnected group retained")
	}
	if len(BuildProjectDashboard(nil, threads, nil, nil, nil, nil, nil, time.Now())) != 0 {
		t.Fatal("unlinked historical task retained")
	}
}

func TestConnectedCompletedWorkspaceTaskSurvivesHistoryLimit(t *testing.T) {
	tasks := []CodexWorkspaceTask{}
	for i := 0; i < 15; i++ {
		id := fmt.Sprint(i)
		tasks = append(tasks, CodexWorkspaceTask{ID: id, TaskKey: id, Classification: "completed", UpdatedAt: time.Unix(int64(i), 0)})
	}
	selected, running, waiting := selectWorkspaceTasks(tasks, map[string]bool{"0": true})
	found := false
	for _, task := range selected {
		if task.TaskKey == "0" {
			found = true
		}
	}
	if !found || len(selected) != 10 || running != 0 || waiting != 0 {
		t.Fatal("linked history lost or counted as running", selected)
	}
	selected, _, _ = selectWorkspaceTasks(tasks)
	for _, task := range selected {
		if task.TaskKey == "0" {
			t.Fatal("disconnected history retained")
		}
	}
}
