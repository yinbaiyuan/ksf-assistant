package toolchain

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// inspectOwned checks all surviving files before allowing a missing-file repair.
// Extra files/directories and symlinks are conflicts; IO failures are not absence.
func inspectOwned(root string, expected map[string]string) (string, []string) {
	state := "managed"
	details := []string{}
	priority := map[string]int{"managed": 0, "missing": 1, "edited": 2, "collision": 3, "check_failed": 4}
	add := func(kind, name string) {
		if priority[kind] > priority[state] {
			state = kind
		}
		label := map[string]string{"missing": "缺失", "edited": "内容有变化", "collision": "非受管文件或路径冲突", "check_failed": "无法读取"}[kind]
		details = append(details, label+"："+filepath.ToSlash(name))
	}
	if err := noSymlinks(root); err != nil {
		if err.Error() == "symlink_refused" {
			return "collision", []string{"collision: ."}
		}
		return "check_failed", []string{"check_failed: ."}
	}
	dirs := map[string]bool{".": true}
	for name, hash := range expected {
		for parent := filepath.Dir(filepath.FromSlash(name)); parent != "."; parent = filepath.Dir(parent) {
			dirs[parent] = true
		}
		file := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(file)
		if errors.Is(err, fs.ErrNotExist) {
			add("missing", name)
			continue
		}
		if err != nil {
			add("check_failed", name)
			continue
		}
		if !info.Mode().IsRegular() {
			add("collision", name)
			continue
		}
		actual, err := fileHash(file)
		if err != nil {
			add("check_failed", name)
		} else if actual != hash {
			add("edited", name)
		}
	}
	err := filepath.WalkDir(root, func(name string, e fs.DirEntry, err error) error {
		relative, _ := filepath.Rel(root, name)
		if errors.Is(err, fs.ErrNotExist) && name == root {
			return nil
		}
		if err != nil {
			add("check_failed", relative)
			return nil
		}
		if e.IsDir() {
			if !dirs[relative] {
				add("collision", relative)
			}
			return nil
		}
		if e.Type()&os.ModeSymlink != 0 || expected[filepath.ToSlash(relative)] == "" {
			add("collision", relative)
		}
		return nil
	})
	if err != nil {
		add("check_failed", ".")
	}
	sort.Strings(details)
	return state, details
}

func (s *Status) finalizeInstallation() {
	s.InstallationState = "not_installed"
	s.InstallationTitle = "未安装"
	s.InstallationAction = "安装到 Codex"
	blocked := ""
	skillsUpdate := false
	componentUpdate := false
	for _, skill := range s.Skills {
		for _, detail := range skill.Details {
			s.InstallationDetails = append(s.InstallationDetails, skill.Name+"/"+detail)
		}
		if skill.State == "check_failed" {
			blocked = "检查失败"
			break
		}
		if skill.State == "collision" {
			blocked = "安装冲突"
		} else if skill.State == "edited" && blocked == "" {
			blocked = "文件有变化"
		}
	}
	missing := false
	for _, p := range s.Problems {
		switch p {
		case "skills_manifest_changed", "skills_adapter_changed":
			skillsUpdate = true
		case "launcher_update_required", "configuration_changed", "execution_manifest_changed":
			componentUpdate = true
		}
		if strings.HasPrefix(p, "skill_") {
			skillsUpdate = true
		}
		if p == "launcher_missing" || p == "skill_missing" {
			missing = true
		}
		if p == "launcher_edited" && blocked == "" {
			blocked = "文件有变化"
		}
		if p == "state_collision" || p == "launcher_collision" {
			blocked = "安装冲突"
		}
		switch p {
		case "launcher_update_required", "configuration_changed", "execution_manifest_changed", "skills_manifest_changed", "skills_adapter_changed", "skill_absent", "skill_missing", "launcher_missing", "skill_edited", "skill_collision", "launcher_edited", "state_collision", "launcher_collision":
		default:
			if strings.HasPrefix(p, "skill_") && p != "skill_check_failed" {
				blocked = "检查失败"
			} else {
				blocked = "检查失败"
			}
		}
	}
	if blocked != "" {
		s.InstallationState = "blocked"
		s.InstallationTitle = blocked
		s.InstallationAction = ""
		return
	}
	if s.Healthy {
		s.InstallationState = "installed"
		s.InstallationTitle = "已安装"
		s.InstallationAction = ""
		return
	}
	if missing {
		s.InstallationState = "incomplete"
		s.InstallationTitle = "安装不完整"
		s.InstallationAction = "修复安装"
		return
	}
	if s.Installed {
		s.InstallationState = "update"
		switch {
		case skillsUpdate && componentUpdate:
			s.InstallationTitle = "技能与组件有更新"
			s.InstallationAction = "全部更新"
		case skillsUpdate:
			s.InstallationTitle = "技能有更新"
			s.InstallationAction = "更新技能"
		default:
			s.InstallationTitle = "组件有更新"
			s.InstallationAction = "更新组件"
		}
	}
}

func (m *Manager) inspectState(r *receipt) (string, []string) {
	files := map[string]string{}
	for n, h := range r.Files {
		files[n] = h
	}
	h, err := fileHash(filepath.Join(m.config.StateDir, "receipt.json"))
	if err != nil {
		return "check_failed", []string{"receipt.json"}
	}
	files["receipt.json"] = h
	return inspectOwned(m.config.StateDir, files)
}
func treeDirectories(root string) []string {
	dirs := []string{}
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			r, _ := filepath.Rel(root, p)
			dirs = append(dirs, r)
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}
func sameTreeSnapshot(root string, files map[string]string, dirs []string) bool {
	if noSymlinks(root) != nil {
		return false
	}
	actual, err := treeHashes(root)
	if err != nil || len(actual) != len(files) {
		return false
	}
	for n, h := range files {
		if actual[n] != h {
			return false
		}
	}
	return strings.Join(treeDirectories(root), "\n") == strings.Join(dirs, "\n")
}
