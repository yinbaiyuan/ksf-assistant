package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func fixture(t *testing.T) *Manager {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	resources := filepath.Join(root, "App Resources")
	manager, err := New(Config{ResourcesDir: resources, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	skillsRoot := filepath.Join(resources, "runtime", "lark-skills")
	write(filepath.Join(skillsRoot, "LICENSE"), "MIT fixture")
	licenseHash, _ := fileHash(filepath.Join(skillsRoot, "LICENSE"))
	manifest := Manifest{SchemaVersion: 1, Version: Version, License: "MIT", LicenseSHA256: licenseHash}
	for _, name := range []string{"lark-doc", "ksfas"} {
		file := filepath.Join(skillsRoot, "skills", name, "SKILL.md")
		write(file, "fixture "+name)
		hash, _ := fileHash(file)
		manifest.Skills = append(manifest.Skills, Skill{Name: name, Files: map[string]string{"SKILL.md": hash}})
	}
	if err := writeJSON(filepath.Join(skillsRoot, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(resources, "runtime", "lark-cli", platform(), "lark-cli"+suffix())
	write(binary, "#!/bin/sh\nprintf '%s\\n' \"$@\"\n")
	hash, _ := fileHash(binary)
	taskBinary := filepath.Join(resources, "runtime", "task", platform(), "ksf-assistant-task"+suffix())
	write(taskBinary, "#!/bin/sh\nprintf '%s\\n' \"$@\"\ncat\n")
	taskHash, _ := fileHash(taskBinary)
	if err := writeJSON(filepath.Join(resources, "runtime", "lark-cli-runtime.json"), map[string]any{"version": Version, "artifacts": map[string]any{platform(): map[string]any{"executable": "lark-cli" + suffix(), "executableSha256": hash, "taskExecutableSha256": taskHash}}}); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(resources, "runtime", "toolchain", platform(), "ksf-assistant-toolchain"+suffix()), "fixture launcher")
	return manager
}

func TestInstallAndUninstallOwnedDirectories(t *testing.T) {
	manager := fixture(t)
	status, err := manager.Status()
	if err != nil || status.Installed || status.Healthy {
		t.Fatalf("initial status: %+v %v", status, err)
	}
	if _, err := os.Stat(manager.config.HomeDir); !os.IsNotExist(err) {
		t.Fatal("status wrote home")
	}
	status, err = manager.Install()
	if err != nil || !status.Installed || !status.Healthy {
		t.Fatalf("install: %+v %v", status, err)
	}
	status, err = manager.Install()
	if err != nil || !status.Healthy {
		t.Fatalf("repeat install: %+v %v", status, err)
	}
	unmanaged := filepath.Join(manager.skillRoot(), "user-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(unmanaged), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmanaged, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Uninstall()
	if err != nil || status.Installed {
		t.Fatalf("uninstall: %+v %v", status, err)
	}
	data, _ := os.ReadFile(unmanaged)
	if string(data) != "user-owned" {
		t.Fatal("unmanaged file lost")
	}
}

func TestCollisionRefusesWithoutPartialInstall(t *testing.T) {
	manager := fixture(t)
	file := filepath.Join(manager.skillRoot(), "lark-doc", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(); err == nil || err.Error() != "skill_collision" {
		t.Fatalf("collision: %v", err)
	}
	if _, err := os.Stat(manager.config.StateDir); !os.IsNotExist(err) {
		t.Fatal("partial installation")
	}
	if _, err := os.Stat(filepath.Join(manager.skillRoot(), "ksfas")); !os.IsNotExist(err) {
		t.Fatal("partial skill")
	}
}

func TestEditedSkillsAndAddedFilesRefuse(t *testing.T) {
	for _, mode := range []string{"edit", "file", "empty-directory", "missing", "launcher", "task-launcher"} {
		t.Run(mode, func(t *testing.T) {
			manager := fixture(t)
			if _, err := manager.Install(); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(manager.skillRoot(), "lark-doc")
			switch mode {
			case "edit":
				if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("user edit"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("user"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "empty-directory":
				if err := os.Mkdir(filepath.Join(root, "user"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(filepath.Join(root, "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			case "launcher":
				if err := os.WriteFile(manager.launcherPath(), []byte("edit"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "task-launcher":
				if err := os.WriteFile(filepath.Join(manager.config.StateDir, "bin", "ksf-assistant-task"+suffix()), []byte("user edit"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			status, _ := manager.Status()
			if status.Healthy {
				t.Fatal("edited installation marked healthy")
			}
			if mode == "missing" {
				if status.InstallationState != "incomplete" {
					t.Fatal(status.InstallationTitle)
				}
				if repaired, err := manager.Install(); err != nil || !repaired.Healthy {
					t.Fatal("missing-file repair failed", err)
				}
				return
			}
			if _, err := manager.Install(); err == nil {
				t.Fatal("install accepted edits")
			}
			if _, err := manager.Uninstall(); err == nil {
				t.Fatal("uninstall accepted edits")
			}
		})
	}
}

func TestTaskLauncherForwardsStdinWithoutCoreAndChecksBundledBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	manager := fixture(t)
	status, err := manager.Install()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	payload := `{"protocol":"ksfassistant-task-runtime-v1","version":1}`
	code, err := LaunchTask(context.Background(), status.TaskLauncherPath, []string{"doctor", "--root", "/workspace with spaces"}, strings.NewReader(payload), &output, &output)
	if err != nil || code != 0 || output.String() != "doctor\n--root\n/workspace with spaces\n"+payload {
		t.Fatalf("task forwarding: %d %v %s", code, err, output.String())
	}
	binary, _ := manager.taskBinary()
	if err := os.WriteFile(binary, []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchTask(context.Background(), status.TaskLauncherPath, []string{"doctor"}, nil, &output, &output); err == nil {
		t.Fatal("changed bundle accepted")
	}
	status, _ = manager.Status()
	if status.Healthy {
		t.Fatal("changed task binary marked healthy")
	}
}

func TestTaskEntryCollisionNeverOverwritesUser(t *testing.T) {
	manager := fixture(t)
	file := filepath.Join(manager.config.StateDir, "bin", "ksf-assistant-task"+suffix())
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("user CLI"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(); err == nil {
		t.Fatal("unowned task entry overwritten")
	}
	data, _ := os.ReadFile(file)
	if string(data) != "user CLI" {
		t.Fatal("user entry changed")
	}
}

func TestIntegrationUpgradeRequiresExplicitReinstall(t *testing.T) {
	manager := fixture(t)
	if _, err := manager.Install(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills")
	file := filepath.Join(root, "skills", "ksfas", "SKILL.md")
	if err := os.WriteFile(file, []byte("new integration resource"), 0o644); err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := readJSON(filepath.Join(root, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	for index := range manifest.Skills {
		if manifest.Skills[index].Name == "ksfas" {
			manifest.Skills[index].Files["SKILL.md"], _ = fileHash(file)
		}
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	status, _ := manager.Status()
	if status.Healthy || !strings.Contains(strings.Join(status.Problems, ","), "skills_manifest_changed") {
		t.Fatalf("outdated resource hidden: %+v", status)
	}
	status, err := manager.Install()
	if err != nil || !status.Healthy {
		t.Fatalf("explicit upgrade: %+v %v", status, err)
	}
}

func TestRollbackNeverDeletesConcurrentUserEdits(t *testing.T) {
	manager := fixture(t)
	manager.beforeCommit = func(index int) error {
		if index != 1 {
			return nil
		}
		file := filepath.Join(manager.skillRoot(), "ksfas", "SKILL.md")
		if err := os.WriteFile(file, []byte("concurrent user edit"), 0o644); err != nil {
			t.Fatal(err)
		}
		return errors.New("injected")
	}
	if _, err := manager.Install(); err == nil || err.Error() != "rollback_failed_preserved_backups" {
		t.Fatalf("expected preserved transaction: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(manager.skillRoot(), "ksfas", "SKILL.md"))
	if string(data) != "concurrent user edit" {
		t.Fatal("concurrent edit lost")
	}
	if _, err := os.Stat(filepath.Join(manager.config.StateDir+".lock", "journal.json")); err != nil {
		t.Fatal("recovery journal lost")
	}
	if _, err := manager.Uninstall(); err == nil {
		t.Fatal("interrupted transaction ignored")
	}
}

func TestFixedFeishuBrandAndDefaultProfile(t *testing.T) {
	manager := fixture(t)
	config := manager.config
	config.Profile = "another"
	if _, err := New(config); err == nil {
		t.Fatal("nondefault profile accepted")
	}
	if _, err := launchArguments([]string{"docs", "--brand=lark"}, "default"); err == nil {
		t.Fatal("brand override accepted")
	}
	if err := os.MkdirAll(manager.config.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, example := range []struct {
		content string
		valid   bool
	}{
		{`{"apps":[{"name":"default","brand":"feishu","appSecret":"fixture-not-used"}]}`, true},
		{`{"apps":[{"name":"default","brand":"lark"}]}`, false},
		{`{"apps":[{"name":"other","brand":"feishu"}]}`, false},
		{`{"apps":[{"name":"default","brand":"feishu"},{"name":"default","brand":"feishu"}]}`, false},
	} {
		if err := os.WriteFile(filepath.Join(manager.config.ConfigDir, "config.json"), []byte(example.content), 0o600); err != nil {
			t.Fatal(err)
		}
		err := manager.checkBrand([]string{"docs", "+list"})
		if (err == nil) != example.valid {
			t.Fatalf("brand guard %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "fixture") {
			t.Fatal("secret included in error")
		}
	}
}

func TestRollbackAtEveryCommitBoundary(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		for boundary := 0; boundary < 3; boundary++ {
			manager := fixture(t)
			var before []byte
			if upgrade {
				if _, err := manager.Install(); err != nil {
					t.Fatal(err)
				}
				before, _ = os.ReadFile(filepath.Join(manager.config.StateDir, "receipt.json"))
			}
			manager.beforeCommit = func(index int) error {
				if index == boundary {
					return errors.New("injected")
				}
				return nil
			}
			if _, err := manager.Install(); err == nil || err.Error() != "transaction_failed_rolled_back" {
				t.Fatalf("expected rollback: %v", err)
			}
			if upgrade {
				after, _ := os.ReadFile(filepath.Join(manager.config.StateDir, "receipt.json"))
				if !reflect.DeepEqual(before, after) {
					t.Fatal("receipt changed after rollback")
				}
				status, _ := manager.Status()
				if !status.Healthy {
					t.Fatalf("rollback unhealthy %+v", status)
				}
			} else {
				for _, name := range []string{manager.config.StateDir, filepath.Join(manager.skillRoot(), "ksfas"), filepath.Join(manager.skillRoot(), "lark-doc")} {
					if _, err := os.Stat(name); !os.IsNotExist(err) {
						t.Fatal("partial transaction remained")
					}
				}
			}
			entries, _ := os.ReadDir(manager.skillRoot())
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".ksfas-") {
					t.Fatal("staging leaked")
				}
			}
		}
	}
}

func TestUninstallRollbackPreservesOriginal(t *testing.T) {
	manager := fixture(t)
	if _, err := manager.Install(); err != nil {
		t.Fatal(err)
	}
	manager.beforeCommit = func(index int) error {
		if index == 1 {
			return errors.New("injected")
		}
		return nil
	}
	if _, err := manager.Uninstall(); err == nil {
		t.Fatal("expected failure")
	}
	status, _ := manager.Status()
	if !status.Healthy {
		t.Fatalf("uninstall rollback: %+v", status)
	}
}

func TestPathVersionAndSymlinkValidation(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../../x", "a\\b", "C:drive", "a/./b", "", "a/.."} {
		if validRelative(name) {
			t.Fatalf("unsafe path %q", name)
		}
	}
	manager := fixture(t)
	for _, mutate := range []func(*Config){func(config *Config) { config.ResourcesDir = "relative" }, func(config *Config) { config.StateDir = config.HomeDir }, func(config *Config) { config.Profile = "--override" }} {
		config := manager.config
		mutate(&config)
		if _, err := New(config); err == nil {
			t.Fatal("unsafe config")
		}
	}
	manifestPath := filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills", "manifest.json")
	data, _ := os.ReadFile(manifestPath)
	if err := os.WriteFile(manifestPath, []byte(strings.Replace(string(data), Version, "1.0.92", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(); err == nil || err.Error() != "version_mismatch" {
		t.Fatalf("version accepted %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.skillRoot()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(manager.config.ResourcesDir, manager.skillRoot()); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := manager.Install(); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestLaunchFixedProfileAndCredentialEnvironment(t *testing.T) {
	config := Config{HomeDir: "/fixed/home", ConfigDir: "/fixed/config"}
	environment := launchEnvironment([]string{"PATH=/usr/bin", "LARKSUITE_CLI_APP_SECRET=fixture", "LARK_CLI_PROFILE=other", "FEISHU_APP_SECRET=fixture", "LARKSUITE_CLI_DATA_DIR=/other", "HOME=/other", "LD_PRELOAD=bad"}, config)
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"fixture", "other", "LD_PRELOAD", "DATA_DIR"} {
		if strings.Contains(joined, forbidden) {
			t.Fatal("unsafe inherited env")
		}
	}
	if !strings.Contains(joined, "LARKSUITE_CLI_CONFIG_DIR=/fixed/config") || !strings.Contains(joined, "NO_UPDATE_NOTIFIER=1") {
		t.Fatal("missing fixed environment")
	}
	for _, args := range [][]string{{"--profile=other", "docs"}, {"docs", "--profile", "other"}, {"config", "show"}, {"update"}, {"skills", "install"}, {"docs", "--app-secret=x"}} {
		if _, err := launchArguments(args, "default"); err == nil {
			t.Fatalf("override accepted: %v", args)
		}
	}
	args, err := launchArguments([]string{"docs", "+list"}, "default")
	if err != nil || !reflect.DeepEqual(args, []string{"--profile", "default", "docs", "+list"}) {
		t.Fatal("fixed profile missing")
	}
}
