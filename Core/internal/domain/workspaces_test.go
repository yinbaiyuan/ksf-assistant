package domain

import (
	"strings"
	"testing"
	"time"
)

func TestCodexWorkspacesGroupByPathAndExcludeKSFOwnedTasks(t *testing.T) {
	now := time.Unix(100, 0)
	ksfProject := Project{ID: "ksf", Name: "KSF"}
	projects := ProjectDashboardSnapshot{Projects: []ProjectDashboardItem{
		{ID: "ksf", Kind: "project", Project: &ksfProject, Tasks: []ProjectTask{{ThreadID: "bound", HostID: "remote"}}},
		{ID: UnassignedProjectID, Kind: "unassigned", Tasks: []ProjectTask{{ThreadID: "active", HostID: "local", TaskRuntime: &TaskRuntimeState{TaskID: "runtime"}}}},
	}}
	threads := []CodexThread{
		{ID: "bound", Name: stringPtr("KSF task"), CWD: "/work/alpha", CreatedAt: 1, UpdatedAt: 10, Path: stringPtr("/tmp/bound.jsonl")},
		{ID: "active", Name: stringPtr("Active"), CWD: "/work/alpha", CreatedAt: 2, UpdatedAt: 20, Path: stringPtr("/tmp/active.jsonl")},
		{ID: "done", Name: stringPtr("Done"), CWD: "/work/alpha", CreatedAt: 3, UpdatedAt: 30, Path: stringPtr("/tmp/done.jsonl")},
		{ID: "other", Name: stringPtr("Other"), CWD: "/work/beta", CreatedAt: 4, UpdatedAt: 40, Path: stringPtr("/tmp/other.jsonl")},
	}
	observations := []TaskObservation{{ID: "bound", HostID: "remote", RuntimeStatus: "active"}, {ID: "active", HostID: "local", RuntimeStatus: "active"}}

	codexProjects := []CodexProject{{ID: "codex-alpha", Name: "测试", Roots: []CodexProjectRoot{{Path: "/work/alpha"}}}}
	snapshot := BuildCodexWorkspaceDashboard("darwin", "", threads, codexProjects, observations, projects, nil, now)
	if snapshot.Availability != "available" || len(snapshot.Workspaces) != 1 {
		t.Fatalf("unexpected workspaces: %#v", snapshot)
	}
	alpha := snapshot.Workspaces[0]
	if alpha.Name != "测试" || alpha.Path != "/work/alpha" || alpha.RunningCount != 1 || alpha.TotalTaskCount != 2 || len(alpha.Tasks) != 2 {
		t.Fatalf("unexpected alpha workspace: %#v", alpha)
	}
	if alpha.Tasks[0].ThreadID != "active" || alpha.Tasks[0].TaskRuntime == nil || alpha.Tasks[1].ThreadID != "done" {
		t.Fatalf("unexpected alpha tasks: %#v", alpha.Tasks)
	}
	for _, workspace := range snapshot.Workspaces {
		for _, task := range workspace.Tasks {
			if task.ThreadID == "bound" {
				t.Fatal("KSF-owned task leaked into Codex workspaces")
			}
		}
	}
}

func TestCodexWorkspacesNormalizeWindowsPathsAndKeepFallbackTasks(t *testing.T) {
	now := time.Unix(100, 0)
	threads := []CodexThread{
		{ID: "one", CWD: `C:\\Work\\Alpha`, CreatedAt: 1, UpdatedAt: 2, Path: stringPtr("one")},
		{ID: "two", CWD: `c:/work/alpha/.`, CreatedAt: 2, UpdatedAt: 3, Path: stringPtr("two")},
		{ID: "child", CWD: `C:\\Work\\Alpha`, ParentThreadID: stringPtr("one"), CreatedAt: 3, UpdatedAt: 4, Path: stringPtr("child")},
		{ID: "no-cwd-completed", CreatedAt: 4, UpdatedAt: 5, Path: stringPtr("no-cwd-completed")},
	}
	observations := []TaskObservation{
		{ID: "remote", HostID: "remote", RuntimeStatus: "active"},
		{ID: "review", HostID: "remote", RuntimeStatus: "active", SourceKind: stringPtr("subAgentReview")},
	}

	codexProjects := []CodexProject{{ID: "alpha", Name: "Alpha Project", Roots: []CodexProjectRoot{{Path: `C:\\Work\\Alpha`}}}}
	snapshot := BuildCodexWorkspaceDashboard("windows", "", threads, codexProjects, observations, ProjectDashboardSnapshot{}, nil, now)
	if len(snapshot.Workspaces) != 2 {
		t.Fatalf("expected one path workspace and one fallback: %#v", snapshot.Workspaces)
	}
	var pathWorkspace, fallback *CodexWorkspaceItem
	for index := range snapshot.Workspaces {
		item := &snapshot.Workspaces[index]
		if item.Kind == "workspace" {
			pathWorkspace = item
		} else {
			fallback = item
		}
	}
	if pathWorkspace == nil || pathWorkspace.Name != "Alpha Project" || pathWorkspace.TotalTaskCount != 2 || !strings.HasPrefix(pathWorkspace.ID, "workspace://") {
		t.Fatalf("unexpected normalized workspace: %#v", pathWorkspace)
	}
	if fallback == nil || fallback.ID != OtherCodexTasksWorkspaceID || len(fallback.Tasks) != 1 || fallback.Tasks[0].ThreadID != "remote" {
		t.Fatalf("unexpected fallback workspace: %#v", fallback)
	}
}

func TestCodexWorkspaceTaskLimitKeepsEveryActiveTask(t *testing.T) {
	now := time.Unix(100, 0)
	threads := []CodexThread{}
	observations := []TaskObservation{}
	for index := 0; index < 12; index++ {
		id := string(rune('a' + index))
		threads = append(threads, CodexThread{ID: id, CWD: "/work/alpha", CreatedAt: int64(index), UpdatedAt: int64(index), Path: stringPtr(id)})
		observations = append(observations, TaskObservation{ID: id, HostID: "local", RuntimeStatus: "active"})
	}
	for index := 0; index < 5; index++ {
		id := "done-" + string(rune('a'+index))
		threads = append(threads, CodexThread{ID: id, CWD: "/work/alpha", CreatedAt: int64(index), UpdatedAt: int64(50 + index), Path: stringPtr(id)})
	}

	codexProjects := []CodexProject{{ID: "alpha", Name: "Alpha", Roots: []CodexProjectRoot{{Path: "/work/alpha"}}}}
	workspace := BuildCodexWorkspaceDashboard("darwin", "", threads, codexProjects, observations, ProjectDashboardSnapshot{}, nil, now).Workspaces[0]
	if len(workspace.Tasks) != 12 || workspace.RunningCount != 12 || workspace.TotalTaskCount != 17 || workspace.HiddenTaskCount != 5 {
		t.Fatalf("active tasks must not be truncated: %#v", workspace)
	}
}

func TestCodexWorkspaceTaskLimitFillsRecentCompletedTasksToTen(t *testing.T) {
	now := time.Unix(100, 0)
	threads := []CodexThread{}
	observations := []TaskObservation{}
	for index := 0; index < 2; index++ {
		id := "active-" + string(rune('a'+index))
		threads = append(threads, CodexThread{ID: id, CWD: "/work/alpha", CreatedAt: int64(index), UpdatedAt: int64(index), Path: stringPtr(id)})
		observations = append(observations, TaskObservation{ID: id, HostID: "local", RuntimeStatus: "active"})
	}
	for index := 0; index < 12; index++ {
		id := "done-" + string(rune('a'+index))
		threads = append(threads, CodexThread{ID: id, CWD: "/work/alpha", CreatedAt: int64(index), UpdatedAt: int64(50 + index), Path: stringPtr(id)})
	}

	codexProjects := []CodexProject{{ID: "alpha", Name: "Alpha", Roots: []CodexProjectRoot{{Path: "/work/alpha"}}}}
	workspace := BuildCodexWorkspaceDashboard("darwin", "", threads, codexProjects, observations, ProjectDashboardSnapshot{}, nil, now).Workspaces[0]
	if len(workspace.Tasks) != 10 || workspace.TotalTaskCount != 14 || workspace.HiddenTaskCount != 4 {
		t.Fatalf("unexpected recent task limit: %#v", workspace)
	}
	if workspace.Tasks[2].ThreadID != "done-l" || workspace.Tasks[9].ThreadID != "done-e" {
		t.Fatalf("completed tasks are not the newest eight: %#v", workspace.Tasks)
	}
}

func TestRemoveUnassignedProjectTasks(t *testing.T) {
	project := Project{ID: "project", Name: "Project"}
	snapshot := RemoveUnassignedProjectTasks(ProjectDashboardSnapshot{Projects: []ProjectDashboardItem{
		{ID: UnassignedProjectID, Kind: "unassigned"},
		{ID: "project", Kind: "project", Project: &project},
	}})
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].ID != "project" {
		t.Fatalf("unexpected project snapshot: %#v", snapshot)
	}
}

func TestCodexWorkspacesExcludeKSFRootAndRemovedCodexProjects(t *testing.T) {
	now := time.Unix(100, 0)
	threads := []CodexThread{
		{ID: "current", CWD: "/work/current", CreatedAt: 1, UpdatedAt: 3, Path: stringPtr("current")},
		{ID: "ksf", CWD: "/work/KSF/nested", CreatedAt: 1, UpdatedAt: 2, Path: stringPtr("ksf")},
		{ID: "removed", CWD: "/work/removed", CreatedAt: 1, UpdatedAt: 1, Path: stringPtr("removed")},
	}
	codexProjects := []CodexProject{
		{ID: "current", Name: "测试", Roots: []CodexProjectRoot{{Path: "/work/current"}}},
		{ID: "ksf", Name: "KSF", Roots: []CodexProjectRoot{{Path: "/work/KSF"}}},
	}

	snapshot := BuildCodexWorkspaceDashboard("darwin", "/work/KSF", threads, codexProjects, nil, ProjectDashboardSnapshot{}, nil, now)
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].Name != "测试" || snapshot.Workspaces[0].Tasks[0].ThreadID != "current" {
		t.Fatalf("unexpected filtered workspaces: %#v", snapshot.Workspaces)
	}
}

func TestCodexWorkspaceDashboardKeepsCurrentEmptyProjectsAndPinnedState(t *testing.T) {
	now := time.Unix(100, 0)
	projects := []CodexProject{
		{ID: "alpha", Name: "Alpha", Roots: []CodexProjectRoot{{Path: "/work/alpha"}}},
		{ID: "beta", Name: "Beta", Roots: []CodexProjectRoot{{Path: "/work/beta"}}},
	}
	first := BuildCodexWorkspaceDashboard("darwin", "", nil, projects, nil, ProjectDashboardSnapshot{}, nil, now)
	if len(first.Workspaces) != 2 || first.Workspaces[0].TotalTaskCount != 0 || first.Workspaces[1].TotalTaskCount != 0 {
		t.Fatalf("current empty Codex projects must remain discoverable: %#v", first.Workspaces)
	}
	pinnedID := first.Workspaces[1].ID
	second := BuildCodexWorkspaceDashboard("darwin", "", nil, projects, nil, ProjectDashboardSnapshot{}, []string{pinnedID}, now)
	if len(second.Workspaces) != 2 || second.Workspaces[0].ID != pinnedID || !second.Workspaces[0].IsPinned || second.Workspaces[1].IsPinned {
		t.Fatalf("pinned workspace must be marked and sorted first: %#v", second.Workspaces)
	}
}
