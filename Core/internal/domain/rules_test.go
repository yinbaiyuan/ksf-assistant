package domain

import (
	"reflect"
	"testing"
	"time"
)

func pointer[T any](value T) *T { return &value }

func TestPinnedUnassignedRemainsOneVisibleGroupWithoutTasks(t *testing.T) {
	items := BuildProjectDashboard(nil, nil, nil, nil, map[string]bool{UnassignedProjectID: true}, nil, nil, time.Now())
	if len(items) != 1 || items[0].Kind != "unassigned" || !items[0].IsPinned || items[0].Tasks == nil {
		t.Fatalf("unexpected pinned group: %#v", items)
	}
	snapshot := RemoveWorkspaceTasksFromUnassignedProjects(ProjectDashboardSnapshot{Projects: items}, CodexWorkspaceSnapshot{})
	if len(SelectHomeProjects(snapshot.Projects)) != 1 {
		t.Fatal("pinned empty group disappeared")
	}
	if items := BuildProjectDashboard(nil, nil, nil, nil, nil, nil, nil, time.Now()); len(items) != 0 {
		t.Fatal("unpinned empty group retained")
	}
}

func TestProjectDashboardDoesNotMutateActivityObservations(t *testing.T) {
	observations := []TaskObservation{
		{ID: "running", HostID: "local", RuntimeStatus: "active"},
		{ID: "waiting", HostID: "local", RuntimeStatus: "active", ActiveFlags: []string{"waitingOnUserInput"}},
	}
	want := append([]TaskObservation(nil), observations...)
	_ = BuildProjectDashboard(nil, nil, nil, observations, nil, nil, nil, time.Now())
	if !reflect.DeepEqual(observations, want) {
		t.Fatalf("dashboard construction mutated activity observations: %#v", observations)
	}
}

func TestNormalizeRateLimitsKeepsCodexFirstAndDropsPrivateCreditState(t *testing.T) {
	response := RateLimitsResponse{
		RateLimits: RateLimitBucket{LimitID: pointer("codex"), Primary: &RateLimitWindow{UsedPercent: 22}},
		RateLimitsByLimitID: map[string]RateLimitBucket{
			"other": {LimitID: pointer("other"), LimitName: pointer("Zeta"), Credits: &CreditsSnapshot{HasCredits: true}},
		},
	}
	values := NormalizeRateLimits(response)
	if len(values) != 2 || *values[0].LimitID != "codex" || values[1].Credits != nil {
		t.Fatalf("unexpected normalized buckets: %#v", values)
	}
	if remaining := HeadlineRemaining(values[0]); remaining == nil || *remaining != 78 {
		t.Fatalf("unexpected remaining value: %#v", remaining)
	}
}

func TestTaskClassificationMatchesDesktopRules(t *testing.T) {
	running := TaskObservation{ID: "1", HostID: "local", RuntimeStatus: "active"}
	waiting := TaskObservation{ID: "2", HostID: "local", RuntimeStatus: "active", ActiveFlags: []string{"waitingOnUserInput"}}
	internal := TaskObservation{ID: "3", HostID: "local", RuntimeStatus: "active", AgentNickname: pointer("worker")}
	if ClassifyTask(running) != "running" || ClassifyTask(waiting) != "waiting" || ClassifyTask(internal) != "ignored" {
		t.Fatal("task classification diverged")
	}
	if reason := WaitingReason(waiting); reason == nil || *reason != "userInput" {
		t.Fatalf("unexpected waiting reason: %#v", reason)
	}
	snapshot := SummarizeActivity([]TaskObservation{running, waiting, internal}, time.Unix(10, 0))
	if snapshot.RunningCount != 1 || snapshot.WaitingCount != 1 {
		t.Fatalf("unexpected activity: %#v", snapshot)
	}
}

func TestProjectDashboardPrefersProjectionAndKeepsUnassignedActiveTasks(t *testing.T) {
	now := time.Unix(100, 0)
	project := Project{ID: "project-a", Name: "A", EngineeringMappings: []EngineeringMapping{{ID: "repo-a", RootPath: "/work/a"}}}
	threads := []CodexThread{
		{ID: "bound", CWD: "/other", CreatedAt: 10, UpdatedAt: 20, Path: pointer("/tmp/bound.jsonl")},
		{ID: "cwd", CWD: "/work/a", CreatedAt: 11, UpdatedAt: 21, Path: pointer("/tmp/cwd.jsonl")},
		{ID: "free", CWD: "/free", CreatedAt: 12, UpdatedAt: 22},
	}
	projections := map[string]TaskProjection{"bound": {Bindings: []TaskBinding{{ProjectCard: "project-a", BoundAt: now.Format(time.RFC3339)}}}}
	observations := []TaskObservation{
		{ID: "bound", HostID: "local", RuntimeStatus: "active"},
		{ID: "free", HostID: "local", RuntimeStatus: "active"},
	}
	items := BuildProjectDashboard([]Project{project}, threads, projections, observations, map[string]bool{"project-a": true}, nil, nil, now)
	if len(items) != 2 || items[0].Kind != "unassigned" || items[1].ID != "project-a" || len(items[1].Tasks) != 2 {
		t.Fatalf("unexpected dashboard: %#v", items)
	}
	if len(SelectHomeProjects(items)) != 2 {
		t.Fatal("active and pinned projects must both remain visible")
	}
}

func TestPinnedProjectWithoutTasksSerializesAnEmptyTaskList(t *testing.T) {
	project := Project{ID: "project-a", Name: "A"}
	items := BuildProjectDashboard(
		[]Project{project}, nil, nil, nil,
		map[string]bool{"project-a": true}, nil, nil, time.Unix(100, 0),
	)
	if len(items) != 1 || items[0].Tasks == nil || len(items[0].Tasks) != 0 {
		t.Fatalf("empty pinned project must keep a non-nil task list: %#v", items)
	}
}
