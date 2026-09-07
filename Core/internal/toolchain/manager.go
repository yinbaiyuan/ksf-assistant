package toolchain

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ksfassistant/core/internal/usercommand"
)

type receipt struct {
	SchemaVersion     int               `json:"schemaVersion"`
	Version           string            `json:"version"`
	ExecutionManifest string            `json:"executionManifest,omitempty"`
	Adaptation        *Adaptation       `json:"adaptation,omitempty"`
	ResourcesDir      string            `json:"resourcesDir"`
	Profile           string            `json:"profile"`
	ConfigDir         string            `json:"configDir"`
	DataRoot          string            `json:"dataRoot,omitempty"`
	Skills            []Skill           `json:"skills"`
	Files             map[string]string `json:"files"`
}

func (manager *Manager) launcherPath() string {
	return filepath.Join(manager.config.StateDir, "bin", "ksfas-lark"+suffix())
}

func (manager *Manager) skillRoot() string {
	return filepath.Join(manager.config.HomeDir, ".agents", "skills")
}

func (manager *Manager) currentLauncher(owned *receipt) error {
	expected, err := fileHash(filepath.Join(manager.config.ResourcesDir, "runtime", "toolchain", platform(), "ksf-assistant-toolchain"+suffix()))
	if err != nil {
		return errors.New("launcher_source_unavailable")
	}
	for _, name := range []string{"ksfas-lark", "lark-cli", "ksf-assistant-task"} {
		if owned.Files["bin/"+name+suffix()] != expected {
			return errors.New("launcher_update_required")
		}
	}
	return nil
}

func (manager *Manager) ownership() (*receipt, error) {
	var owned receipt
	err := readJSON(filepath.Join(manager.config.StateDir, "receipt.json"), &owned)
	if errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Lstat(manager.config.StateDir); !errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("state_collision")
		}
		return nil, nil
	}
	if err != nil || owned.SchemaVersion != 1 || owned.Version == "" || len(owned.Files) != 4 || owned.Files["launcher.json"] == "" || owned.Files["bin/ksfas-lark"+suffix()] == "" || owned.Files["bin/lark-cli"+suffix()] == "" || owned.Files["bin/ksf-assistant-task"+suffix()] == "" {
		return nil, errors.New("invalid_receipt")
	}
	if !owned.Adaptation.valid(owned.Version) {
		return nil, errors.New("invalid_receipt")
	}
	seen := map[string]bool{}
	for _, skill := range owned.Skills {
		if !safeName.MatchString(skill.Name) || seen[skill.Name] || len(skill.Files) == 0 || skill.Files["SKILL.md"] == "" {
			return nil, errors.New("invalid_receipt")
		}
		seen[skill.Name] = true
		for name, hash := range skill.Files {
			if !validRelative(name) || !digestPattern.MatchString(hash) {
				return nil, errors.New("invalid_receipt")
			}
		}
	}
	return &owned, nil
}

func (manager *Manager) verifyState(owned *receipt) error {
	files := map[string]string{}
	for name, hash := range owned.Files {
		files[name] = hash
	}
	hash, err := fileHash(filepath.Join(manager.config.StateDir, "receipt.json"))
	if err != nil {
		return errors.New("invalid_receipt")
	}
	files["receipt.json"] = hash
	return verifyTree(manager.config.StateDir, files)
}

func (manager *Manager) Status() (status Status, resultErr error) {
	defer func() { status.finalizeInstallation() }()
	status = Status{Version: Version, ExecutionManifest: usercommand.ManifestDigest(), Skills: []SkillStatus{}, LauncherPath: manager.launcherPath(), Profile: manager.config.Profile, ConfigDir: manager.config.ConfigDir, Problems: []string{}}
	status.TaskLauncherPath = filepath.Join(manager.config.StateDir, "bin", "ksf-assistant-task"+suffix())
	status.Brand = "feishu"
	manifest, err := manager.manifest()
	if err != nil {
		status.Problems = append(status.Problems, err.Error())
		return status, nil
	}
	if manifest.Adaptation != nil {
		status.SkillsAdapterRevision = manifest.Adaptation.Revision
		status.SkillsAdapterDigest = manifest.Adaptation.Digest
	}
	if _, err := manager.binary(); err != nil {
		status.Problems = append(status.Problems, err.Error())
	}
	if _, err := manager.taskBinary(); err != nil {
		status.Problems = append(status.Problems, err.Error())
	}
	owned, err := manager.ownership()
	if err != nil {
		status.Problems = append(status.Problems, err.Error())
	}
	managed := map[string]Skill{}
	if owned != nil {
		status.Installed = true
		for _, skill := range owned.Skills {
			managed[skill.Name] = skill
		}
		state, details := manager.inspectState(owned)
		if state != "managed" {
			status.Problems = append(status.Problems, "launcher_"+state)
			status.InstallationDetails = append(status.InstallationDetails, details...)
		}
		for _, previous := range owned.Skills {
			current := false
			for _, desired := range manifest.Skills {
				if desired.Name == previous.Name {
					current = true
					break
				}
			}
			if current {
				continue
			}
			state, details := inspectOwned(filepath.Join(manager.skillRoot(), previous.Name), previous.Files)
			if state != "managed" {
				status.Skills = append(status.Skills, SkillStatus{Name: previous.Name, State: state, Details: details})
				status.Problems = append(status.Problems, "skill_"+state)
			}
		}
		if err := manager.currentLauncher(owned); err != nil {
			status.Problems = append(status.Problems, err.Error())
		}
		if owned.ExecutionManifest != usercommand.ManifestDigest() {
			status.Problems = append(status.Problems, "execution_manifest_changed")
		}
		if owned.Version != Version || owned.ResourcesDir != manager.config.ResourcesDir || owned.Profile != manager.config.Profile || owned.ConfigDir != manager.config.ConfigDir || owned.DataRoot != manager.config.DataRoot {
			status.Problems = append(status.Problems, "configuration_changed")
		}
		if !sameSkills(owned.Skills, manifest.Skills) {
			status.Problems = append(status.Problems, "skills_manifest_changed")
		}
		if !sameAdaptation(owned.Adaptation, manifest.Adaptation) {
			status.Problems = append(status.Problems, "skills_adapter_changed")
		}
	}
	for _, skill := range manifest.Skills {
		state := "absent"
		var details []string
		root := filepath.Join(manager.skillRoot(), skill.Name)
		if previous, exists := managed[skill.Name]; exists {
			state = "managed"
			state, details = inspectOwned(root, previous.Files)
		} else if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
			if err != nil {
				state = "check_failed"
			} else {
				state = "collision"
			}
			details = []string{state + ": ."}
		}
		if strings.HasPrefix(skill.Name, "ksf-lark-") {
			legacy := strings.TrimPrefix(skill.Name, "ksf-")
			if _, ownedLegacy := managed[legacy]; !ownedLegacy {
				if _, err := os.Lstat(filepath.Join(manager.skillRoot(), legacy)); !errors.Is(err, fs.ErrNotExist) {
					state = "collision"
					details = append(details, "旧版未受管："+legacy)
				}
			}
		}
		status.Skills = append(status.Skills, SkillStatus{Name: skill.Name, State: state, Details: details})
		if state != "managed" {
			status.Problems = append(status.Problems, "skill_"+state)
		}
	}
	if _, err := os.Lstat(manager.config.StateDir + ".lock"); !errors.Is(err, fs.ErrNotExist) {
		status.Problems = append(status.Problems, "transaction_pending")
	}
	status.Problems = unique(status.Problems)
	status.Healthy = status.Installed && len(status.Problems) == 0
	return status, nil
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}

func (manager *Manager) Install() (Status, error) {
	if err := manager.change(true); err != nil {
		return Status{}, err
	}
	return manager.Status()
}

func (manager *Manager) Uninstall() (Status, error) {
	if err := manager.change(false); err != nil {
		return Status{}, err
	}
	return manager.Status()
}

type replacement struct {
	OldDirectories []string          `json:"oldDirectories,omitempty"`
	Target         string            `json:"target"`
	Stage          string            `json:"stage"`
	Backup         string            `json:"backup"`
	Original       bool              `json:"original"`
	OldMoved       bool              `json:"oldMoved"`
	NewMoved       bool              `json:"newMoved"`
	OldFiles       map[string]string `json:"oldFiles,omitempty"`
	NewFiles       map[string]string `json:"newFiles,omitempty"`
}

func sameAdaptation(left, right *Adaptation) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sameSkills(left, right []Skill) bool {
	if len(left) != len(right) {
		return false
	}
	lookup := map[string]map[string]string{}
	for _, skill := range left {
		lookup[skill.Name] = skill.Files
	}
	for _, skill := range right {
		files := lookup[skill.Name]
		if len(files) != len(skill.Files) {
			return false
		}
		for name, hash := range skill.Files {
			if files[name] != hash {
				return false
			}
		}
	}
	return true
}

func treeHashes(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errors.New("transaction_snapshot_failed")
		}
		if entry.IsDir() {
			return nil
		}
		hash, err := fileHash(name)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, name)
		files[filepath.ToSlash(relative)] = hash
		return nil
	})
	return files, err
}

func writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("serialization_failed")
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("write_failed")
	}
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("write_failed")
	}
	return nil
}

func copyFile(source, destination string, mode fs.FileMode) error {
	if err := noSymlinks(source); err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return errors.New("source_unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return errors.New("stage_failed")
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return errors.New("stage_failed")
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("stage_failed")
	}
	return nil
}

func (manager *Manager) change(install bool) error {
	if err := noSymlinks(manager.config.StateDir); err != nil {
		return err
	}
	if err := noSymlinks(manager.skillRoot()); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manager.config.StateDir), 0o700); err != nil {
		return errors.New("state_unavailable")
	}
	lock := manager.config.StateDir + ".lock"
	if err := os.Mkdir(lock, 0o700); err != nil {
		return errors.New("transaction_busy_or_interrupted")
	}
	keepLock := false
	defer func() {
		if !keepLock {
			_ = os.RemoveAll(lock)
		}
	}()
	owned, err := manager.ownership()
	if err != nil {
		return err
	}
	if owned != nil {
		state, _ := manager.inspectState(owned)
		if state != "managed" && !(install && state == "missing") {
			return errors.New("launcher_" + state)
		}
		for _, skill := range owned.Skills {
			state, _ := inspectOwned(filepath.Join(manager.skillRoot(), skill.Name), skill.Files)
			if state != "managed" && !(install && state == "missing") {
				return errors.New("owned_skill_edited")
			}
		}
	}
	if !install && owned == nil {
		return nil
	}
	var manifest Manifest
	if install {
		manifest, err = manager.manifest()
		if err != nil {
			return err
		}
		if _, err := manager.binary(); err != nil {
			return err
		}
		if _, err := manager.taskBinary(); err != nil {
			return err
		}
	}
	oldSkills := map[string]Skill{}
	if owned != nil {
		for _, skill := range owned.Skills {
			oldSkills[skill.Name] = skill
		}
	}
	newSkills := map[string]Skill{}
	for _, skill := range manifest.Skills {
		newSkills[skill.Name] = skill
	}
	names := map[string]bool{}
	for name := range oldSkills {
		names[name] = true
	}
	for name := range newSkills {
		names[name] = true
	}
	ordered := []string{}
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		if strings.HasPrefix(name, "ksf-lark-") {
			legacy := strings.TrimPrefix(name, "ksf-")
			if _, wasOwned := oldSkills[legacy]; !wasOwned {
				if _, err := os.Lstat(filepath.Join(manager.skillRoot(), legacy)); !errors.Is(err, fs.ErrNotExist) {
					return errors.New("legacy_skill_collision")
				}
			}
		}
		if _, wasOwned := oldSkills[name]; !wasOwned {
			if _, err := os.Lstat(filepath.Join(manager.skillRoot(), name)); !errors.Is(err, fs.ErrNotExist) {
				return errors.New("skill_collision")
			}
		}
	}
	if err := os.MkdirAll(manager.skillRoot(), 0o700); err != nil {
		return errors.New("skills_root_unavailable")
	}
	changes := []*replacement{}
	defer func() {
		if !keepLock {
			for _, change := range changes {
				if change.Stage != "" {
					_ = os.RemoveAll(filepath.Dir(change.Stage))
				}
			}
		}
	}()
	stage := func(target string, exists bool) (*replacement, error) {
		container, err := os.MkdirTemp(filepath.Dir(target), ".ksfas-transaction-")
		if err != nil {
			return nil, errors.New("stage_failed")
		}
		change := &replacement{Target: target, Stage: filepath.Join(container, "new"), Backup: filepath.Join(container, "previous"), Original: exists}
		changes = append(changes, change)
		return change, nil
	}
	for _, name := range ordered {
		_, exists := oldSkills[name]
		if exists {
			_, e := os.Lstat(filepath.Join(manager.skillRoot(), name))
			exists = !errors.Is(e, fs.ErrNotExist)
		}
		change, err := stage(filepath.Join(manager.skillRoot(), name), exists)
		if err != nil {
			return err
		}
		if skill, exists := newSkills[name]; exists {
			for relative := range skill.Files {
				if err := copyFile(filepath.Join(manager.config.ResourcesDir, "runtime", "lark-skills", "skills", name, filepath.FromSlash(relative)), filepath.Join(change.Stage, filepath.FromSlash(relative)), 0o644); err != nil {
					return err
				}
			}
			if err := verifyTree(change.Stage, skill.Files); err != nil {
				return errors.New("stage_checksum_mismatch")
			}
		}
	}
	stateChange, err := stage(manager.config.StateDir, owned != nil)
	if err != nil {
		return err
	}
	if install {
		binary := filepath.Join(manager.config.ResourcesDir, "runtime", "toolchain", platform(), "ksf-assistant-toolchain"+suffix())
		for _, name := range []string{"ksfas-lark", "lark-cli", "ksf-assistant-task"} {
			if err := copyFile(binary, filepath.Join(stateChange.Stage, "bin", name+suffix()), 0o755); err != nil {
				return err
			}
		}
		settings := launcherSettings{SchemaVersion: 1, ResourcesDir: manager.config.ResourcesDir, HomeDir: manager.config.HomeDir, StateDir: manager.config.StateDir, Profile: manager.config.Profile, ConfigDir: manager.config.ConfigDir, DataRoot: manager.config.DataRoot, Brand: "feishu", ExecutionManifest: usercommand.ManifestDigest()}
		if err := writeJSON(filepath.Join(stateChange.Stage, "launcher.json"), settings); err != nil {
			return err
		}
		record := receipt{SchemaVersion: 1, Version: Version, ResourcesDir: manager.config.ResourcesDir, Profile: manager.config.Profile, ConfigDir: manager.config.ConfigDir, DataRoot: manager.config.DataRoot, Skills: manifest.Skills, Files: map[string]string{}, ExecutionManifest: usercommand.ManifestDigest()}
		record.Adaptation = manifest.Adaptation
		for _, name := range []string{"bin/ksfas-lark" + suffix(), "bin/lark-cli" + suffix(), "bin/ksf-assistant-task" + suffix(), "launcher.json"} {
			hash, err := fileHash(filepath.Join(stateChange.Stage, filepath.FromSlash(name)))
			if err != nil {
				return err
			}
			record.Files[name] = hash
		}
		if err := writeJSON(filepath.Join(stateChange.Stage, "receipt.json"), record); err != nil {
			return err
		}
	}
	for _, change := range changes {
		if change.Original {
			change.OldFiles, err = treeHashes(change.Target)
			change.OldDirectories = treeDirectories(change.Target)
			if err != nil {
				return err
			}
		}
		if _, exists := os.Lstat(change.Stage); exists == nil {
			change.NewFiles, err = treeHashes(change.Stage)
			if err != nil {
				return err
			}
		}
	}
	if install {
		if err := manager.backupInstallation(changes); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(lock, "journal.json"), changes); err != nil {
		return err
	}
	rollback := func() error {
		for index := len(changes) - 1; index >= 0; index-- {
			change := changes[index]
			if change.OldMoved && !sameTreeSnapshot(change.Backup, change.OldFiles, change.OldDirectories) {
				return errors.New("rollback_backup_edited")
			}
			if change.NewMoved {
				if verifyTree(change.Target, change.NewFiles) != nil {
					return errors.New("rollback_target_edited")
				}
				if err := os.Rename(change.Target, change.Stage); err != nil {
					return errors.New("rollback_failed")
				}
			}
			if change.OldMoved {
				if err := os.Rename(change.Backup, change.Target); err != nil {
					return errors.New("rollback_failed")
				}
			}
		}
		return nil
	}
	fail := func() error {
		if err := rollback(); err != nil {
			keepLock = true
			return errors.New("rollback_failed_preserved_backups")
		}
		return errors.New("transaction_failed_rolled_back")
	}
	for index, change := range changes {
		if manager.beforeCommit != nil {
			if err := manager.beforeCommit(index); err != nil {
				return fail()
			}
		}
		if noSymlinks(change.Target) != nil {
			return fail()
		}
		if change.Original {
			var state string
			if change.Target == manager.config.StateDir {
				state, _ = manager.inspectState(owned)
			} else {
				state, _ = inspectOwned(change.Target, oldSkills[filepath.Base(change.Target)].Files)
			}
			if state != "managed" && !(install && state == "missing") {
				return fail()
			}
			if !sameTreeSnapshot(change.Target, change.OldFiles, change.OldDirectories) {
				return fail()
			}

			if err := os.Rename(change.Target, change.Backup); err != nil {
				return fail()
			}
			change.OldMoved = true
		} else if _, err := os.Lstat(change.Target); !errors.Is(err, fs.ErrNotExist) {
			return fail()
		}
		if _, err := os.Lstat(change.Stage); err == nil {
			if err := os.Rename(change.Stage, change.Target); err != nil {
				return fail()
			}
			change.NewMoved = true
		}
	}
	return nil
}
