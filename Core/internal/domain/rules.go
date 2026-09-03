package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const UnassignedProjectID = "runtime://unassigned-tasks"

func PublicTaskKey(threadID string) string {
	hash := sha256.Sum256([]byte(threadID))
	return hex.EncodeToString(hash[:10])
}

func NormalizeRateLimits(response RateLimitsResponse) []RateLimitBucket {
	buckets := map[string]RateLimitBucket{}
	for key, bucket := range response.RateLimitsByLimitID {
		id := key
		if bucket.LimitID != nil && *bucket.LimitID != "" {
			id = *bucket.LimitID
		}
		buckets[id] = cacheBucket(bucket)
	}
	rootID := "codex"
	if response.RateLimits.LimitID != nil && *response.RateLimits.LimitID != "" {
		rootID = *response.RateLimits.LimitID
	}
	if _, ok := buckets[rootID]; !ok {
		buckets[rootID] = cacheBucket(response.RateLimits)
	}
	result := make([]RateLimitBucket, 0, len(buckets))
	for _, bucket := range buckets {
		result = append(result, bucket)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := bucketID(result[i]), bucketID(result[j])
		if left == "codex" {
			return true
		}
		if right == "codex" {
			return false
		}
		return strings.ToLower(bucketName(result[i])) < strings.ToLower(bucketName(result[j]))
	})
	return result
}

func HeadlineRemaining(bucket RateLimitBucket) *int {
	values := []int{}
	if bucket.Primary != nil {
		values = append(values, bucket.Primary.RemainingPercent())
	}
	if bucket.Secondary != nil {
		values = append(values, bucket.Secondary.RemainingPercent())
	}
	if len(values) == 0 {
		return nil
	}
	value := values[0]
	for _, candidate := range values[1:] {
		if candidate < value {
			value = candidate
		}
	}
	return &value
}

func GeneralBucket(buckets []RateLimitBucket) *RateLimitBucket {
	for i := range buckets {
		if bucketID(buckets[i]) == "codex" {
			return &buckets[i]
		}
	}
	return nil
}

func cacheBucket(bucket RateLimitBucket) RateLimitBucket {
	bucket.Credits = nil
	bucket.IndividualLimit = nil
	return bucket
}

func bucketID(bucket RateLimitBucket) string {
	if bucket.LimitID != nil {
		return *bucket.LimitID
	}
	if bucket.LimitName != nil {
		return *bucket.LimitName
	}
	return "unknown"
}

func bucketName(bucket RateLimitBucket) string {
	if bucketID(bucket) == "codex" {
		return "Codex"
	}
	if bucket.LimitName != nil {
		return *bucket.LimitName
	}
	return bucketID(bucket)
}

func ClassifyTask(observation TaskObservation) string {
	if isInternalTask(observation) {
		return "ignored"
	}
	if observation.HasPendingPlanImplementation || len(observation.ActiveFlags) > 0 || len(observation.PendingRequestMethods) > 0 {
		return "waiting"
	}
	if observation.RuntimeStatus == "active" {
		return "running"
	}
	return "ignored"
}

func WaitingReason(observation TaskObservation) *string {
	if ClassifyTask(observation) != "waiting" {
		return nil
	}
	for _, flag := range observation.ActiveFlags {
		if flag == "waitingOnApproval" {
			return stringPtr("approval")
		}
	}
	for _, method := range observation.PendingRequestMethods {
		value := strings.ToLower(method)
		if strings.Contains(value, "requestapproval") || (strings.Contains(value, "permission") && strings.Contains(value, "request")) {
			return stringPtr("approval")
		}
	}
	if observation.HasPendingPlanImplementation {
		return stringPtr("planConfirmation")
	}
	for _, flag := range observation.ActiveFlags {
		if flag == "waitingOnUserInput" {
			return stringPtr("userInput")
		}
	}
	for _, method := range observation.PendingRequestMethods {
		value := strings.ToLower(method)
		if strings.Contains(value, "requestuserinfo") || strings.Contains(value, "requestoptionpicker") || strings.Contains(value, "requestsetupcodexcontextpicker") || strings.Contains(value, "elicitation") {
			return stringPtr("userInput")
		}
	}
	return stringPtr("actionRequired")
}

func SummarizeActivity(observations []TaskObservation, observedAt time.Time) TaskActivitySnapshot {
	classifications := map[string]string{}
	for _, observation := range observations {
		classification := ClassifyTask(observation)
		if classification == "ignored" {
			continue
		}
		key := observation.HostID + ":" + observation.ID
		if rank(classification) > rank(classifications[key]) {
			classifications[key] = classification
		}
	}
	running, waiting := 0, 0
	for _, classification := range classifications {
		if classification == "running" {
			running++
		}
		if classification == "waiting" {
			waiting++
		}
	}
	return TaskActivitySnapshot{RunningCount: running, WaitingCount: waiting, ObservedAt: observedAt, Availability: "available", Observations: observations}
}

func BuildProjectDashboard(catalog []Project, threads []CodexThread, projections map[string]TaskProjection, observations []TaskObservation, pinned map[string]bool, usage map[string]ProjectUsageSummary, launchActions map[string]ProjectLaunchAction, now time.Time) []ProjectDashboardItem {
	projectsByID := map[string]Project{}
	for _, project := range catalog {
		projectsByID[project.ID] = project
	}
	threadsByID := map[string]CodexThread{}
	for _, thread := range threads {
		current, ok := threadsByID[thread.ID]
		if !ok || current.UpdatedAt <= thread.UpdatedAt {
			threadsByID[thread.ID] = thread
		}
	}
	type aggregate struct {
		tasks          []ProjectTask
		latestActivity *time.Time
		engineeringID  *string
	}
	aggregates := map[string]*aggregate{}
	unassigned := &aggregate{}
	activeIDs := map[string]bool{}

	for _, observation := range deduplicateObservations(observations) {
		classification := ClassifyTask(observation)
		if classification == "ignored" {
			continue
		}
		thread, ok := threadsByID[observation.ID]
		if ok && (thread.ParentThreadID != nil || nonEmpty(thread.AgentNickname)) {
			continue
		}
		activeIDs[observation.ID] = true
		projectID, engineeringID := assignedProject(thread, ok, catalog, projections)
		if _, exists := projectsByID[projectID]; !exists {
			projectID = UnassignedProjectID
			engineeringID = nil
		}
		target := unassigned
		if projectID != UnassignedProjectID {
			if aggregates[projectID] == nil {
				aggregates[projectID] = &aggregate{}
			}
			target = aggregates[projectID]
		}
		created := now
		updated := now
		var name *string
		if ok {
			created = time.Unix(thread.CreatedAt, 0)
			updated = time.Unix(thread.UpdatedAt, 0)
			name = thread.Name
		}
		var route *RouteSummary
		if projection, found := projections[observation.ID]; found {
			if binding := projection.CurrentBinding(); binding != nil {
				route = binding.Route
			}
		}
		target.tasks = append(target.tasks, ProjectTask{ID: observation.HostID + ":" + observation.ID, ThreadID: observation.ID, TaskKey: PublicTaskKey(observation.ID), HostID: observation.HostID, Name: name, Classification: classification, WaitingReason: WaitingReason(observation), Route: route, CreatedAt: created, ProjectID: projectID})
		setLatest(&target.latestActivity, updated)
		if engineeringID != nil {
			target.engineeringID = engineeringID
		}
	}

	for _, thread := range threadsByID {
		if activeIDs[thread.ID] || thread.ParentThreadID != nil || nonEmpty(thread.AgentNickname) || thread.Path == nil {
			continue
		}
		projectID, engineeringID := assignedProject(thread, true, catalog, projections)
		if _, ok := projectsByID[projectID]; !ok {
			continue
		}
		if aggregates[projectID] == nil {
			aggregates[projectID] = &aggregate{}
		}
		target := aggregates[projectID]
		var route *RouteSummary
		if projection, found := projections[thread.ID]; found {
			if binding := projection.CurrentBinding(); binding != nil {
				route = binding.Route
			}
		}
		target.tasks = append(target.tasks, ProjectTask{ID: "local:" + thread.ID, ThreadID: thread.ID, TaskKey: PublicTaskKey(thread.ID), HostID: "local", Name: thread.Name, Classification: "completed", Route: route, CreatedAt: time.Unix(thread.CreatedAt, 0), ProjectID: projectID})
		updated := time.Unix(thread.UpdatedAt, 0)
		setLatest(&target.latestActivity, updated)
		if engineeringID != nil {
			target.engineeringID = engineeringID
		}
	}

	ids := []string{}
	seen := map[string]bool{}
	for _, project := range catalog {
		if aggregates[project.ID] != nil || pinned[project.ID] {
			ids = append(ids, project.ID)
			seen[project.ID] = true
		}
	}
	for id := range pinned {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	result := []ProjectDashboardItem{}
	for _, id := range ids {
		group := aggregates[id]
		if group == nil {
			group = &aggregate{tasks: []ProjectTask{}}
		} else if group.tasks == nil {
			group.tasks = []ProjectTask{}
		}
		sortTasks(group.tasks)
		project, available := projectsByID[id]
		item := ProjectDashboardItem{ID: id, Kind: "project", IsPinned: pinned[id], Tasks: group.tasks, LatestActivity: group.latestActivity, PreferredEngineeringID: group.engineeringID}
		if available {
			item.Project = &project
		} else {
			item.Kind = "unavailablePinned"
		}
		if value, ok := usage[id]; ok {
			item.Usage = &value
		}
		if action, ok := launchActions[id]; ok {
			item.LaunchAction = &action
		}
		result = append(result, item)
	}
	if len(unassigned.tasks) > 0 {
		sortTasks(unassigned.tasks)
		result = append([]ProjectDashboardItem{{ID: UnassignedProjectID, Kind: "unassigned", Tasks: unassigned.tasks, LatestActivity: unassigned.latestActivity}}, result...)
	}
	return result
}

func SelectHomeProjects(items []ProjectDashboardItem) []ProjectDashboardItem {
	result := []ProjectDashboardItem{}
	for _, item := range items {
		running := 0
		for _, task := range item.Tasks {
			if task.Classification == "running" || task.Classification == "waiting" {
				running++
			}
		}
		if item.IsPinned || running > 0 {
			result = append(result, item)
		}
	}
	return result
}

func isInternalTask(observation TaskObservation) bool {
	return nonEmpty(observation.AgentNickname) || (observation.SourceKind != nil && strings.Contains(strings.ToLower(*observation.SourceKind), "subagent"))
}

func rank(value string) int {
	switch value {
	case "waiting":
		return 3
	case "running":
		return 2
	case "completed":
		return 1
	default:
		return 0
	}
}

func nonEmpty(value *string) bool    { return value != nil && *value != "" }
func stringPtr(value string) *string { return &value }

func normalizedPath(value string) string {
	clean := filepath.Clean(value)
	return strings.ToLower(strings.TrimSuffix(clean, string(filepath.Separator)))
}

func assignedProject(thread CodexThread, hasThread bool, projects []Project, projections map[string]TaskProjection) (string, *string) {
	if projection, ok := projections[thread.ID]; ok {
		if binding := projection.CurrentBinding(); binding != nil {
			return binding.ProjectCard, nil
		}
	}
	if !hasThread {
		return "", nil
	}
	current := normalizedPath(thread.CWD)
	for _, project := range projects {
		for _, mapping := range project.EngineeringMappings {
			if normalizedPath(mapping.RootPath) == current {
				id := mapping.ID
				return project.ID, &id
			}
		}
	}
	return "", nil
}

func deduplicateObservations(values []TaskObservation) []TaskObservation {
	result := map[string]TaskObservation{}
	for _, value := range values {
		if ClassifyTask(value) == "ignored" {
			continue
		}
		key := value.HostID + ":" + value.ID
		current, exists := result[key]
		if !exists {
			result[key] = value
			continue
		}
		if value.RuntimeStatus == "active" {
			current.RuntimeStatus = "active"
		}
		current.ActiveFlags = unique(append(current.ActiveFlags, value.ActiveFlags...))
		current.PendingRequestMethods = unique(append(current.PendingRequestMethods, value.PendingRequestMethods...))
		current.HasPendingPlanImplementation = current.HasPendingPlanImplementation || value.HasPendingPlanImplementation
		if current.AgentNickname == nil {
			current.AgentNickname = value.AgentNickname
		}
		if current.SourceKind == nil {
			current.SourceKind = value.SourceKind
		}
		result[key] = current
	}
	values = values[:0]
	for _, value := range result {
		values = append(values, value)
	}
	return values
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func setLatest(target **time.Time, value time.Time) {
	if *target == nil || value.After(**target) {
		copy := value
		*target = &copy
	}
}

func sortTasks(tasks []ProjectTask) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})
}
