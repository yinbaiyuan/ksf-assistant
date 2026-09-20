package service

import (
	"testing"
	"time"

	"ksfassistant/core/internal/domain"
)

func TestMergeActivityObservationsKeepsFallbackTasksMissingFromDesktop(t *testing.T) {
	desktop := []domain.TaskObservation{
		{ID: "waiting", HostID: "local", RuntimeStatus: "active", ActiveFlags: []string{"waitingOnUserInput"}},
	}
	fallback := []domain.TaskObservation{
		{ID: "waiting", HostID: "local", RuntimeStatus: "active"},
		{ID: "running", HostID: "local", RuntimeStatus: "active"},
	}

	merged := mergeActivityObservations(desktop, fallback)
	snapshot := domain.SummarizeActivity(merged, time.Now())
	if snapshot.RunningCount != 1 || snapshot.WaitingCount != 1 {
		t.Fatalf("partial Desktop snapshot hid fallback task: %+v", snapshot)
	}
	if len(merged) != 2 || len(merged[0].ActiveFlags) != 1 {
		t.Fatalf("Desktop observation did not retain precedence: %#v", merged)
	}
}

func TestMergeActivityObservationsDoesNotResurrectDesktopTerminalTask(t *testing.T) {
	desktop := []domain.TaskObservation{{ID: "task", HostID: "local", RuntimeStatus: "idle"}}
	fallback := []domain.TaskObservation{{ID: "task", HostID: "local", RuntimeStatus: "active"}}

	merged := mergeActivityObservations(desktop, fallback)
	snapshot := domain.SummarizeActivity(merged, time.Now())
	if snapshot.RunningCount != 0 || snapshot.WaitingCount != 0 || len(merged) != 1 || merged[0].RuntimeStatus != "idle" {
		t.Fatalf("fallback replaced authoritative Desktop state: %+v %#v", snapshot, merged)
	}
}

func TestLocalTaskCandidatesIncludesUnassignedTopLevelThreads(t *testing.T) {
	path := "/tmp/thread.jsonl"
	parent := "parent"
	nickname := "worker"
	threads := []domain.CodexThread{
		{ID: "unassigned", Path: &path},
		{ID: "child", Path: &path, ParentThreadID: &parent},
		{ID: "agent", Path: &path, AgentNickname: &nickname},
		{ID: "presentation-only"},
	}

	got := localTaskCandidates(threads)
	if len(got) != 1 || got[0] != "unassigned" {
		t.Fatalf("unexpected Desktop subscription candidates: %#v", got)
	}
}

func TestMergeCodexThreadsKeepsRecentStatusAndCompleteHistory(t *testing.T) {
	recent := []domain.CodexThread{{ID: "active", Status: domain.ThreadStatus{Type: "active"}}}
	complete := []domain.CodexThread{
		{ID: "active", Status: domain.ThreadStatus{Type: "idle"}},
		{ID: "older", Status: domain.ThreadStatus{Type: "idle"}},
	}
	got := mergeCodexThreads(recent, complete)
	if len(got) != 2 || got[0].Status.Type != "active" || got[1].ID != "older" {
		t.Fatalf("recent activity did not overlay complete history: %#v", got)
	}
}
