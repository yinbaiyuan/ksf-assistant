package desktop

import (
	"testing"
)

func TestParseObservationPreservesDescriptionsWithoutRawState(t *testing.T) {
	state := map[string]any{
		"threadRuntimeStatus": map[string]any{"type": "active", "activeFlags": []any{"waitingOnUserInput"}},
		"requests":            []any{map[string]any{"method": "item/tool/requestUserInput"}},
		"source":              map[string]any{"vscode": map[string]any{}},
	}
	value, ok := parseObservation(taskKey{hostID: "local", threadID: "thread"}, state)
	if !ok || value.RuntimeStatus != "active" || len(value.ActiveFlags) != 1 || len(value.PendingRequestMethods) != 1 {
		t.Fatalf("unexpected observation: %#v", value)
	}
}
