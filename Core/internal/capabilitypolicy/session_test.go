package capabilitypolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogoutSurvivesRestartAndRequiresExplicitSignIn(t *testing.T) {
	root := t.TempDir()
	if err := SignOut(root); err != nil {
		t.Fatal(err)
	}
	if err := CheckSession(root); err != ErrSignedOut {
		t.Fatal(err)
	}
	if err := SignOut(root); err != nil {
		t.Fatal(err)
	}
	if err := CheckSession(filepath.Clean(root)); err != ErrSignedOut {
		t.Fatal("restart lost logout")
	}
	if err := SignIn(root); err != nil {
		t.Fatal(err)
	}
	if err := CheckSession(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, SessionFilename), 0700); err != nil {
		t.Fatal(err)
	}
	if CheckSession(root) == nil {
		t.Fatal("malformed state bypassed logout")
	}
}
