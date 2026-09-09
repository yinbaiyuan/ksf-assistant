package tokens

import (
	"path/filepath"
	"testing"
	"time"

	"ksfassistant/core/internal/domain"
)

func TestWorkspaceUsageCountsOrdinaryLineageAndExcludesKSFOwnedThreads(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	workspacePath := filepath.Join(root, "ordinary")
	workspaceID := domain.CodexWorkspaceID("darwin", workspacePath)
	rootLog := filepath.Join(root, "root.jsonl")
	childLog := filepath.Join(root, "child.jsonl")
	boundLog := filepath.Join(root, "bound.jsonl")
	writeTokenFixture(t, rootLog, []string{tokenLine("2026-09-09T01:00:00Z", 100, 80, 30, 20)})
	writeTokenFixture(t, childLog, []string{tokenLine("2026-09-09T02:00:00Z", 50, 40, 15, 10)})
	writeTokenFixture(t, boundLog, []string{tokenLine("2026-09-09T03:00:00Z", 200, 160, 60, 40)})
	parentID := "ordinary"
	ordinaryName := "Ordinary"
	boundName := "Bound"
	missingName := "Missing"
	threads := []domain.CodexThread{
		{ID: parentID, Name: &ordinaryName, CWD: workspacePath, Path: &rootLog, CreatedAt: now.Add(-time.Hour).Unix()},
		{ID: "child", ParentThreadID: &parentID, Path: &childLog, CreatedAt: now.Add(-30 * time.Minute).Unix()},
		{ID: "bound", Name: &boundName, CWD: workspacePath, Path: &boundLog, CreatedAt: now.Add(-20 * time.Minute).Unix()},
		{ID: "missing", Name: &missingName, CWD: workspacePath, CreatedAt: now.Add(-10 * time.Minute).Unix()},
	}
	project := domain.Project{ID: "ksf", Name: "KSF"}
	projects := domain.ProjectDashboardSnapshot{Projects: []domain.ProjectDashboardItem{{
		ID: "ksf", Kind: "project", Project: &project, Tasks: []domain.ProjectTask{{ThreadID: "bound", HostID: "local"}},
	}}}

	usage := ReadWorkspaceUsage("darwin", []domain.CodexWorkspaceItem{{ID: workspaceID, Kind: "workspace", Path: workspacePath}}, threads, projects, now)[workspaceID]
	if usage.CumulativeTokens != 150 || usage.TodayTokens != 150 || usage.IsComplete || usage.UncountedThreadCount != 1 {
		t.Fatalf("unexpected workspace usage: %#v", usage)
	}
}

func TestWorkspaceUsageExcludesCompletedUnnamedThreadsButCountsVisibleUnnamedTasks(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	workspacePath := filepath.Join(root, "ordinary")
	workspaceID := domain.CodexWorkspaceID("darwin", workspacePath)
	activeLog := filepath.Join(root, "active.jsonl")
	staleLog := filepath.Join(root, "stale.jsonl")
	writeTokenFixture(t, activeLog, []string{tokenLine("2026-09-09T01:00:00Z", 100, 80, 30, 20)})
	writeTokenFixture(t, staleLog, []string{tokenLine("2026-09-09T02:00:00Z", 200, 160, 60, 40)})
	threads := []domain.CodexThread{
		{ID: "active", CWD: workspacePath, Path: &activeLog, CreatedAt: now.Add(-time.Hour).Unix()},
		{ID: "stale", CWD: workspacePath, Path: &staleLog, CreatedAt: now.Add(-2 * time.Hour).Unix()},
	}
	workspaces := []domain.CodexWorkspaceItem{{
		ID: workspaceID, Kind: "workspace", Path: workspacePath,
		Tasks: []domain.CodexWorkspaceTask{{ThreadID: "active", Classification: "running"}},
	}}

	usage := ReadWorkspaceUsage("darwin", workspaces, threads, domain.ProjectDashboardSnapshot{}, now)[workspaceID]
	if usage.CumulativeTokens != 100 || usage.TodayTokens != 100 || !usage.IsComplete || usage.UncountedThreadCount != 0 {
		t.Fatalf("unexpected workspace usage: %#v", usage)
	}
}
