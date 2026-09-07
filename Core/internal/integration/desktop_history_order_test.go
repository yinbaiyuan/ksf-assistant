package integration

import (
	"encoding/json"
	"testing"
)

func historyTurn(id, text string, timestamp float64) map[string]any {
	turn := map[string]any{"turnId": id, "status": "completed", "items": []any{map[string]any{"type": "agentMessage", "text": text, "phase": "final_answer"}}}
	if timestamp > 0 {
		turn["turnStartedAtMs"] = timestamp
	}
	return turn
}
func TestCanonicalHistoryOrderDoesNotReplayUnindexedOrOldTurns(t *testing.T) {
	old := historyTurn("old", "old token calculation", 9000)
	latest := historyTurn("latest", "current capability list", 0)
	orphan := historyTurn("orphan", "unindexed old result", 99999)
	state := map[string]any{"turns": []any{old}, "turnHistory": map[string]any{"kind": "canonical", "history": map[string]any{
		"entitiesByKey": map[string]any{"a": old, "b": latest, "orphan": orphan},
		"islands":       []any{map[string]any{"entries": []any{map[string]any{"value": "a"}, map[string]any{"value": "b"}}}},
	}}}
	for i := 0; i < 100; i++ {
		p := projectDesktopTaskLink(state)
		if p.TurnID != "latest" || p.Detail != "current capability list" {
			t.Fatalf("iteration %d replayed %q: %q", i, p.TurnID, p.Detail)
		}
	}
}
func TestCanonicalMissingEntityDoesNotProjectOlderTurnAsLatest(t *testing.T) {
	state := map[string]any{"turnHistory": map[string]any{"kind": "canonical", "history": map[string]any{
		"entitiesByKey": map[string]any{"a": historyTurn("old", "old", 1)},
		"islands":       []any{map[string]any{"entries": []any{map[string]any{"value": "a"}, map[string]any{"value": "missing"}}}},
	}}}
	if p := projectDesktopTaskLink(state); p.TurnID != "" {
		t.Fatalf("incomplete history projected %q", p.TurnID)
	}
}
func TestDirectTurnArrayRetainsItsOrderWithoutTimestampInference(t *testing.T) {
	state := map[string]any{"turns": []any{historyTurn("old", "old", 99), historyTurn("latest", "latest", 0)}}
	if p := projectDesktopTaskLink(state); p.TurnID != "latest" {
		t.Fatalf("timestamp overrode explicit order: %q", p.TurnID)
	}
}

func TestLegacyHistoryAcceptsDesktopJSONNumberTimestamps(t *testing.T) {
	old := historyTurn("old", "old", 0)
	old["turnStartedAtMs"] = json.Number("1000")
	latest := historyTurn("latest", "latest", 0)
	latest["turnStartedAtMs"] = json.Number("2000")
	state := map[string]any{"turnHistory": map[string]any{"history": map[string]any{"entitiesByKey": map[string]any{"old": old, "latest": latest}}}}
	if p := projectDesktopTaskLink(state); p.TurnID != "latest" {
		t.Fatalf("Desktop numeric timestamp lost: %q", p.TurnID)
	}
}
