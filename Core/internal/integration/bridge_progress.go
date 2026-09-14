package integration

// Bridge-owned turns share the same public content projection and durable
// latest-card mailbox as Desktop turns, without changing ownership semantics.
func (runtime *Runtime) projectBridgeProgress(link TaskLink, snapshot map[string]any, turnID string) {
	if thread, ok := snapshot["thread"].(map[string]any); ok {
		snapshot = thread
	}
	normalized := normalizeDesktopState(snapshot)
	turns, _ := normalized["turns"].([]any)
	for _, raw := range turns {
		turn, _ := raw.(map[string]any)
		if cleanString(turn["id"]) != turnID {
			continue
		}
		incoming := desktopTurnProgressSegments(turn)
		if len(incoming) == 0 {
			return
		}
		updated, err := runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			if value.ActiveTurnID != turnID || value.TurnState != "running" {
				return
			}
			var existing []taskProgressSegment
			if value.ExtraString("progressTurnId") == turnID {
				value.ExtraValue("progressSegments", &existing)
			}
			segments := mergeTaskProgressSegments(existing, incoming)
			value.SetExtraString("progressTurnId", turnID)
			value.SetExtraString("latestInputTurnId", turnID)
			value.SetExtraValue("progressSegments", fitTaskLinkProgressSegments(*value, segments))
			value.Detail = boundedPublicText(taskProgressText(segments), 900)
			value.SetExtraValue("cardSyncPending", true)
		})
		if err == nil {
			runtime.deliverTaskCard(updated.ID)
		}
		return
	}
}
