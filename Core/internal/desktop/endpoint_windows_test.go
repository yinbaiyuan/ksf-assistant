//go:build windows

package desktop

import "testing"

func TestDefaultEndpointUsesCodexDesktopCurrentUserPipeOnWindows(t *testing.T) {
	t.Setenv("CODEX_DESKTOP_IPC_PATH", "")
	if got, want := DefaultEndpoint(`C:\Users\fixture`), `\\.\pipe\codex-ipc`; got != want {
		t.Fatalf("DefaultEndpoint() = %q, want %q", got, want)
	}
}

func TestDefaultEndpointKeepsExplicitWindowsOverride(t *testing.T) {
	t.Setenv("CODEX_DESKTOP_IPC_PATH", `\\.\pipe\private-codex-ipc`)
	if got, want := DefaultEndpoint(`C:\Users\fixture`), `\\.\pipe\private-codex-ipc`; got != want {
		t.Fatalf("DefaultEndpoint() = %q, want %q", got, want)
	}
}
