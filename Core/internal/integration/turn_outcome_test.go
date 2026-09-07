package integration

import "testing"

func TestTurnOutcomeDoesNotReportEmptyOrErroredCompletionAsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []any
		err   any
		want  string
	}{
		{"user only", []any{map[string]any{"type": "userMessage", "text": "hello"}}, nil, "failed"},
		{"empty", nil, nil, "failed"},
		{"commentary only", []any{map[string]any{"type": "agentMessage", "text": "Working", "phase": "commentary"}}, nil, "failed"},
		{"final", []any{map[string]any{"type": "agentMessage", "text": "Hello", "phase": "final_answer"}}, nil, "completed"},
		{"legacy final", []any{map[string]any{"type": "agentMessage", "text": "Hello"}}, nil, "completed"},
		{"error overrides output", []any{map[string]any{"type": "agentMessage", "text": "Partial"}}, map[string]any{"message": "model requires newer Codex"}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turn := map[string]any{"id": "current", "status": "completed", "items": tc.items, "error": tc.err}
			snapshot := map[string]any{"turns": []any{map[string]any{"id": "old", "status": "completed", "items": []any{map[string]any{"type": "agentMessage", "text": "Old success"}}}, turn}}
			state, detail := bridgeTurnProjection(snapshot, "current")
			if state != tc.want || detail == "" {
				t.Fatalf("bridge: %q %q, want %s with explanation", state, detail, tc.want)
			}
			p := projectDesktopTaskLink(snapshot)
			if p.TurnState != tc.want || p.Detail != detail {
				t.Fatalf("desktop and bridge disagree: %#v versus %q %q", p, state, detail)
			}
		})
	}
}
