package service

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/taskruntime"
)

func TestTaskRuntimeEnrichesOnlyExistingLocalDesktopTasks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("governance"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	store, err := taskruntime.Open(root, taskruntime.Options{KeyDirectory: filepath.Join(t.TempDir(), "key"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	revision := uint64(0)
	for _, identity := range []string{"known-local-task", "not-in-desktop-task", "known-remote-task"} {
		_, _, _, err := store.Report(context.Background(), taskruntime.Request{Protocol: taskruntime.Protocol, Version: 1, Host: "codex", ThreadID: identity, EventID: "create", ExpectedRevision: &revision, State: &taskruntime.ReportedState{Scope: "unresolved", ReportedStatus: "completed"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	old := &domain.RouteSummary{Jobs: []domain.RouteJob{}, Abilities: []domain.RouteAbility{}}
	project := domain.Project{ID: "old-project", Name: "Old"}
	dashboard := domain.ProjectDashboardSnapshot{Catalog: []domain.Project{project}, Projects: []domain.ProjectDashboardItem{{ID: project.ID, Kind: "project", Project: &project, Tasks: []domain.ProjectTask{
		{ID: "local:known-local-task", ThreadID: "known-local-task", HostID: "local", Classification: "running", ProjectID: project.ID, Route: old},
		{ID: "local:no-record-task", ThreadID: "no-record-task", HostID: "local", Classification: "waiting", ProjectID: project.ID, Route: old},
		{ID: "remote:known-remote-task", ThreadID: "known-remote-task", HostID: "remote", Classification: "running", ProjectID: project.ID, Route: old},
	}}}}
	activity := domain.TaskActivitySnapshot{Availability: "available", ObservedAt: now, RunningCount: 2, WaitingCount: 1, Observations: []domain.TaskObservation{
		{ID: "known-local-task", HostID: "local", RuntimeStatus: "active"},
		{ID: "no-record-task", HostID: "local", RuntimeStatus: "active"},
		{ID: "known-remote-task", HostID: "remote", RuntimeStatus: "active"},
	}}
	beforeActivity := activity
	applyTaskRuntime(context.Background(), root, store, &dashboard, activity)
	if !reflect.DeepEqual(activity, beforeActivity) {
		t.Fatal("runtime modified Desktop activity/counts")
	}
	tasks := map[string]domain.ProjectTask{}
	for _, group := range dashboard.Projects {
		for _, task := range group.Tasks {
			tasks[task.ThreadID] = task
		}
	}
	if len(tasks) != 3 {
		t.Fatal("runtime changed task count", len(tasks))
	}
	local := tasks["known-local-task"]
	if local.Classification != "running" || local.ProjectID != domain.UnassignedProjectID || local.Route != nil || local.TaskRuntime == nil || local.TaskRuntime.Scope != "unresolved" || local.TaskRuntime.ReportedStatus != "completed" || local.TaskRuntime.ObservedStatus != "active" || !local.TaskRuntime.ObservedAt.Equal(now) {
		t.Fatalf("reported/observed or unresolved binding conflated: %+v", local)
	}
	for _, identity := range []string{"no-record-task", "known-remote-task"} {
		if tasks[identity].Route != old || tasks[identity].TaskRuntime != nil || tasks[identity].ProjectID != project.ID {
			t.Fatal("fallback or remote task changed", identity)
		}
	}
	if _, exists := tasks["not-in-desktop-task"]; exists {
		t.Fatal("runtime-only record discovered as task")
	}
	if Version != taskruntime.SoftwareVersion {
		t.Fatal("Core and task binary versions differ")
	}
}

func TestRuntimeProjectUsesExplicitCardNotReferenceCount(t *testing.T) {
	root := t.TempDir()
	alpha := domain.Project{ID: "alpha-id", CardPath: filepath.Join(root, "10项目", "Alpha", "项目记忆卡.md")}
	beta := domain.Project{ID: "beta-id", CardPath: "10项目/Beta/项目记忆卡.md"}
	view := taskruntime.View{Snapshot: taskruntime.Snapshot{State: taskruntime.ReportedState{Scope: "project", ProjectCard: beta.CardPath}, ProjectCard: beta.CardPath}, RouteFreshness: "current"}
	if actual := runtimeProjectID(root, []domain.Project{alpha, beta}, view); actual != beta.ID {
		t.Fatal("explicit main project ignored", actual)
	}
	for _, scope := range []string{"ksf", "unresolved"} {
		view.State.Scope = scope
		if actual := runtimeProjectID(root, []domain.Project{alpha, beta}, view); actual != domain.UnassignedProjectID {
			t.Fatal("reference root bound as project", actual)
		}
	}
	view.State.Scope, view.RouteFreshness = "project", "stale"
	if actual := runtimeProjectID(root, []domain.Project{alpha, beta}, view); actual != domain.UnassignedProjectID {
		t.Fatal("stale receipt bound to project", actual)
	}
}

func TestRuntimeRouteRetainsGovernancePolicies(t *testing.T) {
	var receipt taskruntime.Receipt
	receipt.Projection.Category = taskruntime.Category{CategoryID: "development", Name: "Development", ValidationStatus: "草案", ContextPolicy: "route-only"}
	receipt.Projection.Jobs = []taskruntime.Job{{JobID: "engineer", Name: "Engineer", Role: "main", ContextPolicy: "progressive", ValidationStatus: "待验证"}}
	receipt.Projection.Abilities = []taskruntime.Ability{{AbilityID: "testing", JobID: "engineer", ResponsibilityID: "R1", ContextPolicy: "progressive", ValidationStatus: "待验证"}}
	route := runtimeRoute(receipt)
	if *route.Category.ContextPolicy != "route-only" || *route.Category.ValidationStatus != "草案" || *route.Abilities[0].ResponsibilityID != "R1" || *route.Jobs[0].Role != "main" {
		t.Fatal("runtime projection changed root governance semantics")
	}
}

func TestOfflineDesktopNeverConsumesReports(t *testing.T) {
	dashboard := domain.ProjectDashboardSnapshot{Projects: []domain.ProjectDashboardItem{{ID: "project", Tasks: []domain.ProjectTask{{ThreadID: "test-task", HostID: "local"}}}}}
	before := dashboard
	applyTaskRuntime(context.Background(), "", nil, &dashboard, domain.TaskActivitySnapshot{Availability: "offline"})
	if !reflect.DeepEqual(before, dashboard) {
		t.Fatal("offline task reporting altered dashboard")
	}
}
