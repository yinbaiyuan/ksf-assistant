package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"ksfassistant/core/internal/domain"
)

func TestLocateExecutablePrefersDesktopBundleOverStaleCLI(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bundle discovery")
	}
	home := t.TempDir()
	cli := filepath.Join(home, ".local", "bin", "codex")
	bundled := filepath.Join(home, "Applications", "Codex.app", "Contents", "Resources", "codex")
	for _, path := range []string{cli, bundled} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(cli))
	t.Setenv("CODEX_BIN", "")
	got, err := LocateExecutable(home)
	if err != nil || got != bundled {
		t.Fatalf("selected %q, %v; want desktop runtime %q", got, err, bundled)
	}
	t.Setenv("CODEX_BIN", cli)
	got, err = LocateExecutable(home)
	if err != nil || got != cli {
		t.Fatalf("explicit override ignored: %q, %v", got, err)
	}
}

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
