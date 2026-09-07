package toolchain

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepairMissingTreeAndRollbackPreservesMissingState(t *testing.T) {
	for _, whole := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "tree"}[whole], func(t *testing.T) {
			m := fixture(t)
			if _, e := m.Install(); e != nil {
				t.Fatal(e)
			}
			root := filepath.Join(m.skillRoot(), "lark-doc")
			if whole {
				os.RemoveAll(root)
			} else {
				os.Remove(filepath.Join(root, "SKILL.md"))
			}
			status, _ := m.Status()
			if status.InstallationState != "incomplete" {
				t.Fatal(status)
			}
			m.beforeCommit = func(i int) error {
				if i == 2 {
					return errors.New("injected")
				}
				return nil
			}
			if _, e := m.Install(); e == nil {
				t.Fatal("expected rollback")
			}
			if _, e := os.Stat(filepath.Join(root, "SKILL.md")); !os.IsNotExist(e) {
				t.Fatal("rollback changed missing state")
			}
			m.beforeCommit = nil
			if s, e := m.Install(); e != nil || !s.Healthy {
				t.Fatal(s, e)
			}
		})
	}
}
func TestNamespacedMigrationOnlyRemovesOwnedUnmodifiedSkills(t *testing.T) {
	m := fixture(t)
	if _, e := m.Install(); e != nil {
		t.Fatal(e)
	}
	manifestPath := filepath.Join(m.config.ResourcesDir, "runtime/lark-skills/manifest.json")
	var manifest Manifest
	readJSON(manifestPath, &manifest)
	for i := range manifest.Skills {
		old := manifest.Skills[i].Name
		os.Rename(filepath.Join(m.config.ResourcesDir, "runtime/lark-skills/skills", old), filepath.Join(m.config.ResourcesDir, "runtime/lark-skills/skills", "ksf-"+old))
		manifest.Skills[i].Name = "ksf-" + old
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(manifestPath, data, 0600)
	s, _ := m.Status()
	if s.InstallationState != "update" {
		t.Fatal(s)
	}
	if _, e := m.Install(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(m.skillRoot(), "lark-doc")); !os.IsNotExist(e) {
		t.Fatal("legacy duplicate remains")
	}
	if _, e := os.Stat(filepath.Join(m.skillRoot(), "ksf-lark-doc/SKILL.md")); e != nil {
		t.Fatal(e)
	}
	archives, _ := filepath.Glob(m.config.StateDir + ".backups/*.zip")
	if len(archives) != 1 {
		t.Fatal("missing private migration backup", archives)
	}
	backup, e := zip.OpenReader(archives[0])
	if e != nil {
		t.Fatal(e)
	}
	defer backup.Close()
	found := false
	for _, f := range backup.File {
		if f.Name == "skills/lark-doc/SKILL.md" {
			found = true
		}
	}
	if !found {
		t.Fatal("backup lost legacy skill")
	}

}
func TestMissingPlusModifiedIsNotRepairable(t *testing.T) {
	m := fixture(t)
	m.Install()
	os.Remove(filepath.Join(m.skillRoot(), "lark-doc/SKILL.md"))
	os.WriteFile(filepath.Join(m.skillRoot(), "ksfas/SKILL.md"), []byte("user content"), 0600)
	s, _ := m.Status()
	if s.InstallationTitle != "文件有变化" || s.InstallationAction != "" {
		t.Fatal(s)
	}
	if _, e := m.Install(); e == nil {
		t.Fatal("overwrote user changes")
	}
}
