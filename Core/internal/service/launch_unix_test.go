//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"testing"

	"ksfassistant/core/internal/domain"
)

func TestResolveProjectLaunchActionAcceptsOnlySafeStartScript(t *testing.T) {
	root := t.TempDir()
	project := domain.Project{ID: "project", ProjectDirectory: root}
	if _, ok := resolveProjectLaunchAction(project); ok {
		t.Fatal("missing start script should not resolve")
	}

	script := filepath.Join(root, "start.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	canonicalScript := filepath.Join(canonicalRoot, "start.sh")
	action, ok := resolveProjectLaunchAction(project)
	if !ok || action.ScriptPath != canonicalScript || action.WorkingDirectory != canonicalRoot {
		t.Fatalf("unexpected launch action: %#v %v", action, ok)
	}

	if err := os.Chmod(script, 0o722); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolveProjectLaunchAction(project); ok {
		t.Fatal("group-writable start script should not resolve")
	}
}
