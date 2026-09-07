package toolchain

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeAdaptationJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func adaptFixture(t *testing.T, manager *Manager) map[string]any {
	t.Helper()
	root := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills")
	manifestPath := filepath.Join(root, "manifest.json")
	var manifest map[string]any
	if err := readJSON(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	upstreamHash, err := fileHash(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "skills", "lark-doc", "SKILL.md")
	if err := os.WriteFile(file, []byte("adapted lark-doc uses ksfas-lark"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(file)
	if err != nil {
		t.Fatal(err)
	}
	manifest["skills"] = []Skill{{Name: "lark-doc", Files: map[string]string{"SKILL.md": hash}}}
	reportPath := filepath.Join(root, "adaptation-report.json")
	writeAdaptationJSON(t, reportPath, map[string]any{"schemaVersion": 1, "revision": "ksfas-entry-v1"})
	digest, err := fileHash(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest["adaptation"] = map[string]any{"schemaVersion": 1, "revision": "ksfas-entry-v1", "digest": digest, "upstreamVersion": Version, "upstreamManifestSha256": upstreamHash}
	writeAdaptationJSON(t, manifestPath, manifest)
	return manifest
}

func TestAdaptationMetadataRequiresExplicitUpgrade(t *testing.T) {
	manager := fixture(t)
	manifest := adaptFixture(t, manager)
	if status, err := manager.Install(); err != nil || !status.Healthy {
		t.Fatalf("adapted install: %+v %v", status, err)
	}
	root := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills")
	reportPath := filepath.Join(root, "adaptation-report.json")
	writeAdaptationJSON(t, reportPath, map[string]any{"schemaVersion": 1, "revision": "ksfas-entry-v1", "newReport": true})
	digest, err := fileHash(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest["adaptation"].(map[string]any)["digest"] = digest
	writeAdaptationJSON(t, filepath.Join(root, "manifest.json"), manifest)
	before, err := treeHashes(manager.config.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status()
	if err != nil || !status.Installed || status.Healthy || !strings.Contains(strings.Join(status.Problems, ","), "skills_adapter_changed") {
		t.Fatalf("same CLI and skills but changed adaptation hidden: %+v %v", status, err)
	}
	after, err := treeHashes(manager.config.HomeDir)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("status modified installed files: %v", err)
	}
	if status, err := manager.Install(); err != nil || !status.Healthy {
		t.Fatalf("explicit adaptation upgrade: %+v %v", status, err)
	}
}

func snapshotAdaptationHome(t *testing.T, manager *Manager) map[string]string {
	t.Helper()
	files, err := treeHashes(manager.config.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := filepath.Rel(manager.config.HomeDir, manager.config.StateDir+".backups")
	for name := range files {
		if strings.HasPrefix(name, filepath.ToSlash(prefix)+"/") {
			delete(files, name)
		}
	}
	return files
}

func assertAdaptationHome(t *testing.T, manager *Manager, expected map[string]string) {
	t.Helper()
	if actual := snapshotAdaptationHome(t, manager); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("installation changed unexpectedly: before=%v after=%v", expected, actual)
	}
}

func assertAdaptationMetadata(t *testing.T, manager *Manager, expected any) {
	t.Helper()
	var record map[string]any
	if err := readJSON(filepath.Join(manager.config.StateDir, "receipt.json"), &record); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record["adaptation"], expected) {
		t.Fatalf("receipt adaptation: got %v want %v", record["adaptation"], expected)
	}
	status, err := manager.Status()
	if err != nil || !status.Healthy {
		t.Fatalf("status: %+v %v", status, err)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var serialized map[string]any
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatal(err)
	}
	if expected == nil {
		for _, name := range []string{"skillsAdapterRevision", "skillsAdapterDigest"} {
			if _, present := serialized[name]; present {
				t.Fatalf("legacy status includes %s", name)
			}
		}
	} else {
		metadata := expected.(map[string]any)
		if serialized["skillsAdapterRevision"] != metadata["revision"] || serialized["skillsAdapterDigest"] != metadata["digest"] {
			t.Fatalf("status adaptation: %s", data)
		}
	}
}

func TestAdaptationMigrationRepeatInstallAndLegacyRollback(t *testing.T) {
	manager := fixture(t)
	if _, err := manager.Install(); err != nil {
		t.Fatal(err)
	}
	assertAdaptationMetadata(t, manager, nil)
	unmanaged := filepath.Join(manager.skillRoot(), "user-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(unmanaged), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmanaged, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotAdaptationHome(t, manager)
	legacyResources := manager.config.ResourcesDir
	adapted := fixture(t)
	manifest := adaptFixture(t, adapted)
	manager.config.ResourcesDir = adapted.config.ResourcesDir
	status, err := manager.Status()
	if err != nil || status.Healthy || !strings.Contains(strings.Join(status.Problems, ","), "skills_adapter_changed") {
		t.Fatalf("migration not flagged: %+v %v", status, err)
	}
	assertAdaptationHome(t, manager, before)
	for attempt := 0; attempt < 2; attempt++ {
		if status, err := manager.Install(); err != nil || !status.Healthy {
			t.Fatalf("adapted installation %d: %+v %v", attempt, status, err)
		}
		metadata := manifest["adaptation"].(map[string]any)
		metadata["schemaVersion"] = float64(1)
		assertAdaptationMetadata(t, manager, metadata)
		if _, err := os.Stat(filepath.Join(manager.skillRoot(), "ksfas")); !os.IsNotExist(err) {
			t.Fatalf("owned ksfas was not removed: %v", err)
		}
		owned, err := manager.ownership()
		if err != nil || owned == nil || len(owned.Skills) != 1 || owned.Skills[0].Name != "lark-doc" {
			t.Fatalf("adapted owned skills: %+v %v", owned, err)
		}
		if err := verifyTree(filepath.Join(manager.skillRoot(), "lark-doc"), owned.Skills[0].Files); err != nil {
			t.Fatalf("receipt must verify final adapted hashes: %v", err)
		}
		if data, err := os.ReadFile(unmanaged); err != nil || string(data) != "user-owned" {
			t.Fatalf("unowned skill changed: %q %v", data, err)
		}
	}
	manager.config.ResourcesDir = legacyResources
	if status, err := manager.Status(); err != nil || status.Healthy {
		t.Fatalf("rollback must require installation: %+v %v", status, err)
	}
	if status, err := manager.Install(); err != nil || !status.Healthy {
		t.Fatalf("explicit legacy rollback: %+v %v", status, err)
	}
	assertAdaptationMetadata(t, manager, nil)
	assertAdaptationHome(t, manager, before)
}

func TestAdaptationMigrationRefusesUserChanges(t *testing.T) {
	for _, mode := range []string{"removed-edit", "removed-extra-file", "removed-empty-directory", "retained-edit", "collision"} {
		t.Run(mode, func(t *testing.T) {
			manager := fixture(t)
			if _, err := manager.Install(); err != nil {
				t.Fatal(err)
			}
			manifest := adaptFixture(t, manager)
			root := filepath.Join(manager.skillRoot(), "ksfas")
			if mode == "retained-edit" {
				root = filepath.Join(manager.skillRoot(), "lark-doc")
			}
			expectedError := "owned_skill_edited"
			switch mode {
			case "removed-empty-directory":
				if err := os.Mkdir(filepath.Join(root, "notes"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "collision":
				root = filepath.Join(manager.skillRoot(), "lark-new")
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("user-owned"), 0o600); err != nil {
					t.Fatal(err)
				}
				skill := manifest["skills"].([]Skill)[0]
				source := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills")
				if err := copyFile(filepath.Join(source, "skills", skill.Name, "SKILL.md"), filepath.Join(source, "skills", "lark-new", "SKILL.md"), 0o644); err != nil {
					t.Fatal(err)
				}
				manifest["skills"] = append(manifest["skills"].([]Skill), Skill{Name: "lark-new", Files: skill.Files})
				writeAdaptationJSON(t, filepath.Join(source, "manifest.json"), manifest)
				expectedError = "skill_collision"
			default:
				name := "SKILL.md"
				if mode == "removed-extra-file" {
					name = "notes.txt"
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte("user changes"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotAdaptationHome(t, manager)
			if _, err := manager.Install(); err == nil || err.Error() != expectedError {
				t.Fatalf("migration accepted %s: %v", mode, err)
			}
			assertAdaptationHome(t, manager, before)
		})
	}
}

func TestAdaptationMigrationTransactionRollback(t *testing.T) {
	for boundary := 0; boundary < 3; boundary++ {
		manager := fixture(t)
		if _, err := manager.Install(); err != nil {
			t.Fatal(err)
		}
		before := snapshotAdaptationHome(t, manager)
		adaptFixture(t, manager)
		manager.beforeCommit = func(index int) error {
			if index == boundary {
				return errors.New("injected")
			}
			return nil
		}
		if _, err := manager.Install(); err == nil || err.Error() != "transaction_failed_rolled_back" {
			t.Fatalf("boundary %d: %v", boundary, err)
		}
		assertAdaptationHome(t, manager, before)
		manager.beforeCommit = nil
		if status, err := manager.Install(); err != nil || !status.Healthy {
			t.Fatalf("retry migration: %+v %v", status, err)
		}
	}
}

func TestAdaptationInvalidMetadataAndReportRefused(t *testing.T) {
	for _, mode := range []string{"schema", "revision", "digest", "upstream-version", "upstream-hash", "report-edited", "report-missing", "report-symlink", "final-file-edited"} {
		t.Run(mode, func(t *testing.T) {
			manager := fixture(t)
			if _, err := manager.Install(); err != nil {
				t.Fatal(err)
			}
			manifest := adaptFixture(t, manager)
			metadata := manifest["adaptation"].(map[string]any)
			root := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills")
			reportPath := filepath.Join(root, "adaptation-report.json")
			expectedError := "invalid_adaptation_manifest"
			switch mode {
			case "schema":
				metadata["schemaVersion"] = 2
			case "revision":
				metadata["revision"] = ""
			case "digest":
				metadata["digest"] = "not-a-hash"
			case "upstream-version":
				metadata["upstreamVersion"] = "1.0.92"
			case "upstream-hash":
				metadata["upstreamManifestSha256"] = ""
			case "report-edited":
				writeAdaptationJSON(t, reportPath, map[string]any{"tampered": true})
				expectedError = "adaptation_checksum_mismatch"
			case "report-missing", "report-symlink":
				if err := os.Remove(reportPath); err != nil {
					t.Fatal(err)
				}
				if mode == "report-symlink" {
					if err := os.Symlink(filepath.Join(root, "LICENSE"), reportPath); err != nil {
						t.Skip("symlinks unavailable")
					}
				}
				expectedError = "adaptation_checksum_mismatch"
			case "final-file-edited":
				if err := os.WriteFile(filepath.Join(root, "skills", "lark-doc", "SKILL.md"), []byte("tampered"), 0o644); err != nil {
					t.Fatal(err)
				}
				expectedError = "skill_checksum_mismatch"
			}
			writeAdaptationJSON(t, filepath.Join(root, "manifest.json"), manifest)
			before := snapshotAdaptationHome(t, manager)
			if status, err := manager.Status(); err != nil || status.Healthy || !reflect.DeepEqual(status.Problems, []string{expectedError}) {
				t.Fatalf("invalid bundle status: %+v %v", status, err)
			}
			if _, err := manager.Install(); err == nil || err.Error() != expectedError {
				t.Fatalf("invalid bundle accepted: %v", err)
			}
			assertAdaptationHome(t, manager, before)
		})
	}
}

func TestAdaptationIdentityComparedIndependentlyOfSkillHashes(t *testing.T) {
	for _, field := range []string{"revision", "upstreamManifestSha256", "absent"} {
		t.Run(field, func(t *testing.T) {
			manager := fixture(t)
			manifest := adaptFixture(t, manager)
			if _, err := manager.Install(); err != nil {
				t.Fatal(err)
			}
			metadata := manifest["adaptation"].(map[string]any)
			switch field {
			case "revision":
				metadata[field] = "ksfas-entry-v2"
			case "upstreamManifestSha256":
				metadata[field] = strings.Repeat("a", 64)
			case "absent":
				delete(manifest, "adaptation")
			}
			writeAdaptationJSON(t, filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills", "manifest.json"), manifest)
			if status, err := manager.Status(); err != nil || status.Healthy || !reflect.DeepEqual(status.Problems, []string{"skills_adapter_changed"}) {
				t.Fatalf("identity change hidden: %+v %v", status, err)
			}
			if status, err := manager.Install(); err != nil || !status.Healthy {
				t.Fatalf("identity upgrade: %+v %v", status, err)
			}
			if field == "absent" {
				manifest["adaptation"] = metadata
				writeAdaptationJSON(t, filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills", "manifest.json"), manifest)
				if status, err := manager.Status(); err != nil || status.Healthy || !reflect.DeepEqual(status.Problems, []string{"skills_adapter_changed"}) {
					t.Fatalf("new adaptation hidden: %+v %v", status, err)
				}
			}
		})
	}
}

func TestAdaptationNeverRemovesUnownedKSFAS(t *testing.T) {
	manager := fixture(t)
	adaptFixture(t, manager)
	root := filepath.Join(manager.skillRoot(), "ksfas")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "SKILL.md")
	if err := os.WriteFile(file, []byte("user-owned ksfas"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := manager.Install(); err != nil || !status.Healthy {
		t.Fatalf("adapted install with unrelated ksfas: %+v %v", status, err)
	}
	if _, err := manager.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "user-owned ksfas" {
		t.Fatalf("unowned ksfas removed or changed: %q %v", data, err)
	}
}

func TestAdaptationLegacyRollbackRefusesEditsAndCollision(t *testing.T) {
	for _, mode := range []string{"edited", "collision"} {
		t.Run(mode, func(t *testing.T) {
			manager := fixture(t)
			legacy := fixture(t)
			adaptFixture(t, manager)
			if _, err := manager.Install(); err != nil {
				t.Fatal(err)
			}
			name := "lark-doc"
			expectedError := "owned_skill_edited"
			if mode == "collision" {
				name = "ksfas"
				expectedError = "skill_collision"
				if err := os.Mkdir(filepath.Join(manager.skillRoot(), name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(manager.skillRoot(), name, "SKILL.md"), []byte("user changes"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotAdaptationHome(t, manager)
			manager.config.ResourcesDir = legacy.config.ResourcesDir
			if _, err := manager.Install(); err == nil || err.Error() != expectedError {
				t.Fatalf("legacy rollback accepted user changes: %v", err)
			}
			assertAdaptationHome(t, manager, before)
		})
	}
}

func TestAdaptationInvalidReceiptRefused(t *testing.T) {
	manager := fixture(t)
	adaptFixture(t, manager)
	if _, err := manager.Install(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(manager.config.StateDir, "receipt.json")
	var record map[string]any
	if err := readJSON(name, &record); err != nil {
		t.Fatal(err)
	}
	record["adaptation"].(map[string]any)["digest"] = "invalid"
	writeAdaptationJSON(t, name, record)
	before := snapshotAdaptationHome(t, manager)
	if status, err := manager.Status(); err != nil || status.Healthy || !strings.Contains(strings.Join(status.Problems, ","), "invalid_receipt") {
		t.Fatalf("invalid receipt status: %+v %v", status, err)
	}
	for _, action := range []func() (Status, error){manager.Install, manager.Uninstall} {
		if _, err := action(); err == nil || err.Error() != "invalid_receipt" {
			t.Fatalf("invalid receipt accepted: %v", err)
		}
		assertAdaptationHome(t, manager, before)
	}
}
