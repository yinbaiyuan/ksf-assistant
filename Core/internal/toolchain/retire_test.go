package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetireRemovesOwnedSkillsAndKeepsTaskLauncher(t *testing.T) {
	m := fixture(t)
	if _, err := m.Install(); err != nil {
		t.Fatal(err)
	}
	// The new bundle no longer needs an installable Skills payload.
	if err := os.RemoveAll(filepath.Join(m.config.ResourcesDir, "runtime", "lark-skills")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Retire(); err != nil {
		t.Fatal(err)
	}
	r, err := m.ownership()
	if err != nil || len(r.Skills) != 0 || m.verifyState(r) != nil {
		t.Fatal("invalid retired ownership", err)
	}
	if _, err := os.Stat(filepath.Join(m.skillRoot(), "lark-doc")); !os.IsNotExist(err) {
		t.Fatal("owned Skill survived", err)
	}
	if _, err := os.Stat(filepath.Join(m.config.StateDir, "bin", "ksf-assistant-task"+suffix())); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Retire(); err != nil {
		t.Fatal("non-idempotent retirement", err)
	}
}

func TestRetirePreservesEditedSkills(t *testing.T) {
	m := fixture(t)
	if _, err := m.Install(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(m.skillRoot(), "lark-doc", "SKILL.md")
	if err := os.WriteFile(file, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Retire(); err == nil {
		t.Fatal("retired edited Skill")
	}
	b, _ := os.ReadFile(file)
	if string(b) != "user edit" {
		t.Fatal("user edit lost")
	}
}
