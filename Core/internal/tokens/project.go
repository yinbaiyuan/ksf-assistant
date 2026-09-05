package tokens

import (
	"os"
	"time"

	"ksfassistant/core/internal/domain"
)

type transition struct {
	ProjectID string
	BoundAt   time.Time
}

type projectAccumulator struct {
	total     int64
	today     int64
	since     time.Time
	uncounted int
}

func ReadProjectUsage(projectIDs []string, threads []domain.CodexThread, projections map[string]domain.TaskProjection, trackingStartedAt, now time.Time) map[string]domain.ProjectUsageSummary {
	metadata := map[string]domain.CodexThread{}
	for _, thread := range threads {
		metadata[thread.ID] = thread
	}
	timelines := map[string][]transition{}
	for threadID, projection := range projections {
		values := []transition{}
		for _, binding := range projection.Bindings {
			if at, err := time.Parse(time.RFC3339Nano, binding.BoundAt); err == nil {
				values = append(values, transition{ProjectID: binding.ProjectCard, BoundAt: at})
			}
		}
		timelines[threadID] = values
	}
	accumulators := map[string]*projectAccumulator{}
	for _, id := range projectIDs {
		accumulators[id] = &projectAccumulator{since: trackingStartedAt}
	}
	today := startOfDay(now)
	for _, thread := range threads {
		values := inheritedTimeline(thread.ID, timelines, metadata)
		if len(values) == 0 {
			continue
		}
		affected := map[string]bool{}
		for _, item := range values {
			affected[item.ProjectID] = true
		}
		if thread.Path == nil {
			markIncomplete(affected, accumulators, trackingStartedAt)
			continue
		}
		if _, err := os.Stat(*thread.Path); err != nil {
			markIncomplete(affected, accumulators, trackingStartedAt)
			continue
		}
		samples, err := tokenSamples(*thread.Path)
		if err != nil {
			markIncomplete(affected, accumulators, trackingStartedAt)
			continue
		}
		var previous *cumulative
		for _, sample := range samples {
			total := sample.Total
			hadBaseline := previous != nil
			if previous != nil && sample.Total >= previous.Total {
				total = sample.Total - previous.Total
			}
			copy := sample
			previous = &copy
			if total <= 0 {
				continue
			}
			binding := latestTransition(values, sample.At)
			if binding == nil {
				continue
			}
			value := accumulators[binding.ProjectID]
			if value == nil {
				value = &projectAccumulator{since: binding.BoundAt}
				accumulators[binding.ProjectID] = value
			}
			if !hadBaseline && binding.BoundAt.After(time.Unix(thread.CreatedAt, 0)) {
				value.uncounted++
				if binding.BoundAt.Before(value.since) {
					value.since = binding.BoundAt
				}
				continue
			}
			value.total += total
			if !sample.At.Before(today) && !sample.At.After(now) {
				value.today += total
			}
			if binding.BoundAt.Before(value.since) {
				value.since = binding.BoundAt
			}
		}
	}
	result := map[string]domain.ProjectUsageSummary{}
	for id, value := range accumulators {
		result[id] = domain.ProjectUsageSummary{CumulativeTokens: value.total, TodayTokens: value.today, TrackingStartedAt: value.since, IsComplete: value.uncounted == 0, UncountedThreadCount: value.uncounted}
	}
	return result
}

func inheritedTimeline(threadID string, timelines map[string][]transition, metadata map[string]domain.CodexThread) []transition {
	visited := map[string]bool{}
	current := threadID
	for current != "" && !visited[current] {
		visited[current] = true
		if values, ok := timelines[current]; ok {
			return values
		}
		thread, ok := metadata[current]
		if !ok || thread.ParentThreadID == nil {
			break
		}
		current = *thread.ParentThreadID
	}
	return nil
}

func latestTransition(values []transition, at time.Time) *transition {
	var result *transition
	for index := range values {
		if !values[index].BoundAt.After(at) {
			copy := values[index]
			result = &copy
		}
	}
	return result
}

func markIncomplete(projects map[string]bool, values map[string]*projectAccumulator, since time.Time) {
	for project := range projects {
		value := values[project]
		if value == nil {
			value = &projectAccumulator{since: since}
			values[project] = value
		}
		value.uncounted++
	}
}
