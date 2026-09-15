package domain

import (
	"crypto/sha256"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const OtherCodexTasksWorkspaceID = "runtime://other-codex-tasks"

type workspaceAggregate struct {
	id             string
	kind           string
	name           string
	path           string
	tasks          []CodexWorkspaceTask
	totalTaskCount int
	latestActivity *time.Time
}

func BuildCodexWorkspaceDashboard(platform, ksfRoot string, threads []CodexThread, codexProjects []CodexProject, observations []TaskObservation, projects ProjectDashboardSnapshot, pinnedWorkspaceIDs []string, now time.Time, connectedOptions ...map[string]bool) CodexWorkspaceSnapshot {
	connected := connectedTaskKeys(connectedOptions)
	workspaceNames := codexWorkspaceNames(platform, codexProjects)
	_, normalizedKSFRoot := normalizedWorkspacePath(ksfRoot, platform)
	pinned := map[string]bool{}
	for _, id := range pinnedWorkspaceIDs {
		pinned[id] = true
	}
	threadsByID := map[string]CodexThread{}
	for _, thread := range threads {
		current, ok := threadsByID[thread.ID]
		if !ok || current.UpdatedAt <= thread.UpdatedAt {
			threadsByID[thread.ID] = thread
		}
	}

	ksfOwned := map[string]bool{}
	ksfOwnedThreads := map[string]bool{}
	unassigned := map[string]ProjectTask{}
	for _, item := range projects.Projects {
		for _, task := range item.Tasks {
			key := task.HostID + ":" + task.ThreadID
			if item.Kind == "project" && item.Project != nil {
				ksfOwned[key] = true
				ksfOwnedThreads[task.ThreadID] = true
			} else if item.Kind == "unassigned" {
				unassigned[key] = task
			}
		}
	}

	aggregates := map[string]*workspaceAggregate{}
	for _, project := range codexProjects {
		for _, root := range project.Roots {
			_, identity := normalizedWorkspacePath(root.Path, platform)
			if identity == "" || workspaceWithinRoot(identity, normalizedKSFRoot) {
				continue
			}
			workspaceForPath(aggregates, root.Path, platform, workspaceNames)
		}
	}
	activeIDs := map[string]bool{}
	for _, observation := range deduplicateObservations(observations) {
		classification := ClassifyTask(observation)
		if classification == "ignored" {
			continue
		}
		activeIDs[observation.ID] = true
		key := observation.HostID + ":" + observation.ID
		if ksfOwned[key] || ksfOwnedThreads[observation.ID] {
			continue
		}
		thread, ok := threadsByID[observation.ID]
		if ok && (thread.ParentThreadID != nil || nonEmpty(thread.AgentNickname)) {
			continue
		}
		createdAt, updatedAt := now, now
		var name *string
		cwd := ""
		if ok {
			createdAt = time.Unix(thread.CreatedAt, 0)
			updatedAt = time.Unix(thread.UpdatedAt, 0)
			name = thread.Name
			cwd = thread.CWD
		}
		if thread.LaunchScope == "projectless" {
			cwd = ""
		} else {
			_, workspaceIdentity := normalizedWorkspacePath(cwd, platform)
			if workspaceWithinRoot(workspaceIdentity, normalizedKSFRoot) {
				continue
			}
		}
		aggregate := workspaceForPath(aggregates, cwd, platform, workspaceNames)
		task := CodexWorkspaceTask{ID: key, ThreadID: observation.ID, TaskKey: PublicTaskKey(observation.ID), HostID: observation.HostID, Name: name, Classification: classification, WaitingReason: WaitingReason(observation), CreatedAt: createdAt, UpdatedAt: updatedAt, WorkspaceID: aggregate.id}
		if source, found := unassigned[key]; found {
			task.TaskRuntime = source.TaskRuntime
		}
		aggregate.tasks = append(aggregate.tasks, task)
		aggregate.totalTaskCount++
		setLatest(&aggregate.latestActivity, updatedAt)
	}

	for _, thread := range threadsByID {
		key := "local:" + thread.ID
		projectless := thread.LaunchScope == "projectless"
		if activeIDs[thread.ID] || ksfOwned[key] || ksfOwnedThreads[thread.ID] || thread.ParentThreadID != nil || nonEmpty(thread.AgentNickname) || thread.Path == nil || (!projectless && strings.TrimSpace(thread.CWD) == "") || !nonEmpty(thread.Name) {
			continue
		}
		createdAt := time.Unix(thread.CreatedAt, 0)
		updatedAt := time.Unix(thread.UpdatedAt, 0)
		cwd := thread.CWD
		if projectless {
			cwd = ""
		} else {
			_, workspaceIdentity := normalizedWorkspacePath(cwd, platform)
			if _, exists := workspaceNames[workspaceIdentity]; !exists || workspaceWithinRoot(workspaceIdentity, normalizedKSFRoot) {
				continue
			}
		}
		aggregate := workspaceForPath(aggregates, cwd, platform, workspaceNames)
		aggregate.tasks = append(aggregate.tasks, CodexWorkspaceTask{ID: key, ThreadID: thread.ID, TaskKey: PublicTaskKey(thread.ID), HostID: "local", Name: thread.Name, Classification: "completed", CreatedAt: createdAt, UpdatedAt: updatedAt, WorkspaceID: aggregate.id})
		aggregate.totalTaskCount++
		setLatest(&aggregate.latestActivity, updatedAt)
	}

	items := make([]CodexWorkspaceItem, 0, len(aggregates))
	for _, aggregate := range aggregates {
		tasks, running, waiting := selectWorkspaceTasks(aggregate.tasks, connected)
		items = append(items, CodexWorkspaceItem{ID: aggregate.id, Kind: aggregate.kind, Name: aggregate.name, Path: aggregate.path, IsPinned: pinned[aggregate.id], Tasks: tasks, RunningCount: running, WaitingCount: waiting, TotalTaskCount: aggregate.totalTaskCount, HiddenTaskCount: aggregate.totalTaskCount - len(tasks), LatestActivity: aggregate.latestActivity})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.IsPinned != right.IsPinned {
			return left.IsPinned
		}
		if (left.WaitingCount > 0) != (right.WaitingCount > 0) {
			return left.WaitingCount > 0
		}
		if (left.RunningCount > 0) != (right.RunningCount > 0) {
			return left.RunningCount > 0
		}
		if left.LatestActivity != nil && right.LatestActivity != nil && !left.LatestActivity.Equal(*right.LatestActivity) {
			return left.LatestActivity.After(*right.LatestActivity)
		}
		if left.LatestActivity != nil && right.LatestActivity == nil {
			return true
		}
		if left.LatestActivity == nil && right.LatestActivity != nil {
			return false
		}
		return left.ID < right.ID
	})
	return CodexWorkspaceSnapshot{Availability: "available", Workspaces: items, ObservedAt: now}
}

func RemoveWorkspaceTasksFromUnassignedProjects(snapshot ProjectDashboardSnapshot, workspaces CodexWorkspaceSnapshot) ProjectDashboardSnapshot {
	workspaceTasks := map[string]bool{}
	for _, workspace := range workspaces.Workspaces {
		for _, task := range workspace.Tasks {
			workspaceTasks[task.HostID+":"+task.ThreadID] = true
		}
	}
	items := make([]ProjectDashboardItem, 0, len(snapshot.Projects))
	for _, item := range snapshot.Projects {
		if item.Kind != "unassigned" {
			items = append(items, item)
			continue
		}
		remaining := make([]ProjectTask, 0, len(item.Tasks))
		for _, task := range item.Tasks {
			if !workspaceTasks[task.HostID+":"+task.ThreadID] {
				remaining = append(remaining, task)
			}
		}
		if len(remaining) > 0 || item.IsPinned {
			item.Tasks = remaining
			items = append(items, item)
		}
	}
	snapshot.Projects = items
	return snapshot
}

func CodexWorkspaceID(platform, value string) string {
	_, identity := normalizedWorkspacePath(value, platform)
	if identity == "" {
		return OtherCodexTasksWorkspaceID
	}
	digest := sha256.Sum256([]byte(identity))
	return "workspace://" + fmt.Sprintf("%x", digest[:])
}

func codexWorkspaceNames(platform string, projects []CodexProject) map[string]string {
	result := map[string]string{}
	for _, project := range projects {
		name := strings.TrimSpace(project.Name)
		for _, root := range project.Roots {
			_, identity := normalizedWorkspacePath(root.Path, platform)
			if identity != "" {
				if _, exists := result[identity]; exists {
					continue
				}
				result[identity] = name
			}
		}
	}
	return result
}

func workspaceWithinRoot(identity, root string) bool {
	if identity == "" || root == "" {
		return false
	}
	return identity == root || strings.HasPrefix(identity, strings.TrimSuffix(root, "/")+"/")
}

func workspaceForPath(aggregates map[string]*workspaceAggregate, value, platform string, names map[string]string) *workspaceAggregate {
	displayPath, identity := normalizedWorkspacePath(value, platform)
	id := CodexWorkspaceID(platform, value)
	kind := "other"
	name := "其他任务"
	if identity != "" {
		kind = "workspace"
		name = workspaceBaseName(displayPath, platform)
		if projectName := names[identity]; projectName != "" {
			name = projectName
		}
	}
	if aggregate := aggregates[id]; aggregate != nil {
		return aggregate
	}
	aggregate := &workspaceAggregate{id: id, kind: kind, name: name, path: displayPath, tasks: []CodexWorkspaceTask{}}
	aggregates[id] = aggregate
	return aggregate
}

func normalizedWorkspacePath(value, platform string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if platform == "windows" {
		forward := strings.ReplaceAll(value, "\\", "/")
		clean := path.Clean(forward)
		return strings.ReplaceAll(clean, "/", "\\"), strings.ToLower(clean)
	}
	clean := filepath.Clean(value)
	return clean, clean
}

func workspaceBaseName(value, platform string) string {
	if platform == "windows" {
		value = strings.ReplaceAll(value, "\\", "/")
		return path.Base(value)
	}
	return filepath.Base(value)
}

func selectWorkspaceTasks(values []CodexWorkspaceTask, connectedOptions ...map[string]bool) ([]CodexWorkspaceTask, int, int) {
	connected := connectedTaskKeys(connectedOptions)
	active := make([]CodexWorkspaceTask, 0, len(values))
	completed := make([]CodexWorkspaceTask, 0, len(values))
	running, waiting := 0, 0
	for _, value := range values {
		switch value.Classification {
		case "waiting":
			waiting++
			active = append(active, value)
		case "running":
			running++
			active = append(active, value)
		case "completed":
			if connected[value.TaskKey] {
				active = append(active, value)
			} else {
				completed = append(completed, value)
			}
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Classification != active[j].Classification {
			rank := map[string]int{"waiting": 0, "running": 1, "completed": 2}
			return rank[active[i].Classification] < rank[active[j].Classification]
		}
		if !active[i].UpdatedAt.Equal(active[j].UpdatedAt) {
			return active[i].UpdatedAt.After(active[j].UpdatedAt)
		}
		return active[i].ID < active[j].ID
	})
	sort.SliceStable(completed, func(i, j int) bool {
		if !completed[i].UpdatedAt.Equal(completed[j].UpdatedAt) {
			return completed[i].UpdatedAt.After(completed[j].UpdatedAt)
		}
		return completed[i].ID < completed[j].ID
	})
	remaining := 10 - len(active)
	if remaining > 0 {
		if remaining > len(completed) {
			remaining = len(completed)
		}
		active = append(active, completed[:remaining]...)
	}
	return active, running, waiting
}
