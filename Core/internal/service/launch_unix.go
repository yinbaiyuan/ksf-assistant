//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"syscall"

	"ksfassistant/core/internal/domain"
)

func resolveProjectLaunchAction(project domain.Project) (domain.ProjectLaunchAction, bool) {
	root, err := canonicalDirectory(project.ProjectDirectory)
	if err != nil {
		return domain.ProjectLaunchAction{}, false
	}
	script := filepath.Join(root, "start.sh")
	info, err := os.Lstat(script)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return domain.ProjectLaunchAction{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o100 == 0 {
		return domain.ProjectLaunchAction{}, false
	}
	return domain.ProjectLaunchAction{
		ID: project.ID + ":start.sh", Title: "启动项目", ScriptPath: script,
		WorkingDirectory: root, Kind: "shell",
	}, true
}

func projectLaunchUnavailableMessage() string {
	return "项目目录中没有通过安全校验的 start.sh"
}
