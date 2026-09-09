package tokens

import (
	"strings"
	"time"

	"ksfassistant/core/internal/domain"
)

type workspaceAccumulator struct {
	total     int64
	today     int64
	since     time.Time
	uncounted int
}

func ReadWorkspaceUsage(platform string, workspaces []domain.CodexWorkspaceItem, threads []domain.CodexThread, projects domain.ProjectDashboardSnapshot, now time.Time) map[string]domain.ProjectUsageSummary {
	accumulators := map[string]*workspaceAccumulator{}
	for _, workspace := range workspaces {
		if workspace.Kind == "workspace" && workspace.Path != "" {
			accumulators[workspace.ID] = &workspaceAccumulator{since: now}
		}
	}
	if len(accumulators) == 0 {
		return map[string]domain.ProjectUsageSummary{}
	}
	visibleTaskIDs := map[string]bool{}
	for _, workspace := range workspaces {
		for _, task := range workspace.Tasks {
			visibleTaskIDs[task.ThreadID] = true
		}
	}

	metadata := map[string]domain.CodexThread{}
	sources := newTurnCatalog(nil)
	for _, thread := range threads {
		metadata[thread.ID] = thread
		if thread.Path != nil {
			sources.paths[thread.ID] = *thread.Path
		}
	}
	ksfOwned := map[string]bool{}
	for _, project := range projects.Projects {
		if project.Kind != "project" || project.Project == nil {
			continue
		}
		for _, task := range project.Tasks {
			ksfOwned[task.ThreadID] = true
		}
	}

	today := startOfDay(now)
	for _, thread := range threads {
		if thread.ParentThreadID == nil && (thread.Name == nil || strings.TrimSpace(*thread.Name) == "") && !visibleTaskIDs[thread.ID] {
			continue
		}
		workspaceID := inheritedWorkspaceID(thread.ID, platform, metadata)
		value := accumulators[workspaceID]
		if value == nil || inheritedKSFOwnership(thread.ID, metadata, ksfOwned) {
			continue
		}
		createdAt := time.Unix(thread.CreatedAt, 0)
		if createdAt.Before(value.since) {
			value.since = createdAt
		}
		if thread.Path == nil {
			value.uncounted++
			continue
		}
		samples, err := tokenSamples(*thread.Path, sources)
		if err != nil {
			value.uncounted++
			continue
		}
		for _, sample := range deltas(samples) {
			if sample.Total <= 0 {
				continue
			}
			value.total += sample.Total
			if !sample.At.Before(today) && !sample.At.After(now) {
				value.today += sample.Total
			}
		}
	}

	result := map[string]domain.ProjectUsageSummary{}
	for id, value := range accumulators {
		result[id] = domain.ProjectUsageSummary{
			CumulativeTokens:     value.total,
			TodayTokens:          value.today,
			TrackingStartedAt:    value.since,
			IsComplete:           value.uncounted == 0,
			UncountedThreadCount: value.uncounted,
		}
	}
	return result
}

func inheritedWorkspaceID(threadID, platform string, metadata map[string]domain.CodexThread) string {
	visited := map[string]bool{}
	for threadID != "" && !visited[threadID] {
		visited[threadID] = true
		thread, ok := metadata[threadID]
		if !ok {
			return ""
		}
		if thread.LaunchScope == "projectless" {
			return domain.OtherCodexTasksWorkspaceID
		}
		if id := domain.CodexWorkspaceID(platform, thread.CWD); id != domain.OtherCodexTasksWorkspaceID {
			return id
		}
		if thread.ParentThreadID == nil {
			return ""
		}
		threadID = *thread.ParentThreadID
	}
	return ""
}

func inheritedKSFOwnership(threadID string, metadata map[string]domain.CodexThread, owned map[string]bool) bool {
	visited := map[string]bool{}
	for threadID != "" && !visited[threadID] {
		visited[threadID] = true
		if owned[threadID] {
			return true
		}
		thread, ok := metadata[threadID]
		if !ok || thread.ParentThreadID == nil {
			return false
		}
		threadID = *thread.ParentThreadID
	}
	return false
}
