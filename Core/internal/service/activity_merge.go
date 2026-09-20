package service

import "ksfassistant/core/internal/domain"

// mergeCodexThreads overlays the recent activity page onto the complete
// historical list without dropping older tasks needed by project dashboards.
func mergeCodexThreads(recent, complete []domain.CodexThread) []domain.CodexThread {
	merged := make([]domain.CodexThread, 0, len(recent)+len(complete))
	seen := make(map[string]bool, len(recent)+len(complete))
	for _, values := range [][]domain.CodexThread{recent, complete} {
		for _, thread := range values {
			if thread.ID == "" || seen[thread.ID] {
				continue
			}
			seen[thread.ID] = true
			merged = append(merged, thread)
		}
	}
	return merged
}

// mergeActivityObservations combines the authoritative per-task Desktop view
// with the app-server task index. Desktop wins for tasks it currently knows so
// waiting flags and terminal transitions cannot be overwritten by a coarser
// app-server status; app-server only fills gaps in a partial Desktop snapshot.
func mergeActivityObservations(desktop, fallback []domain.TaskObservation) []domain.TaskObservation {
	merged := make([]domain.TaskObservation, 0, len(desktop)+len(fallback))
	seen := make(map[string]bool, len(desktop)+len(fallback))
	appendUnique := func(values []domain.TaskObservation) {
		for _, observation := range values {
			key := observation.HostID + "\x00" + observation.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, observation)
		}
	}
	appendUnique(desktop)
	appendUnique(fallback)
	return merged
}
