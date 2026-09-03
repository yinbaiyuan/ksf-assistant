package codex

import (
	"path/filepath"
	"testing"

	"codexusagebar/core/internal/domain"
)

func TestObservationsExcludePresentationAndPreserveWaitingFlags(t *testing.T) {
	name := "task"
	values := Observations([]domain.CodexThread{{ID: "thread", Name: &name, Status: domain.ThreadStatus{Type: "active", ActiveFlags: []string{"waitingOnUserInput"}}}})
	if len(values) != 1 || values[0].RuntimeStatus != "active" || len(values[0].ActiveFlags) != 1 {
		t.Fatalf("unexpected observations: %#v", values)
	}
}

func TestAppendUnderIgnoresMissingRoot(t *testing.T) {
	values := appendUnder([]string{"existing"}, "", "relative.exe")
	if len(values) != 1 || values[0] != "existing" {
		t.Fatalf("unexpected candidates: %#v", values)
	}

	values = appendUnder(values, filepath.Join("C:", "Apps"), filepath.Join("Codex", "codex.exe"))
	if len(values) != 2 || values[1] != filepath.Join("C:", "Apps", "Codex", "codex.exe") {
		t.Fatalf("unexpected rooted candidate: %#v", values)
	}
}
