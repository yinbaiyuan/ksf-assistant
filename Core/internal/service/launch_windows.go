//go:build windows

package service

import (
	"os"
	"path/filepath"

	"ksfassistant/core/internal/domain"
)

func resolveProjectLaunchAction(project domain.Project) (domain.ProjectLaunchAction, bool) {
	root, err := canonicalDirectory(project.ProjectDirectory)
	if err != nil {
		return domain.ProjectLaunchAction{}, false
	}
	script := filepath.Join(root, "start.ps1")
	info, err := os.Lstat(script)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return domain.ProjectLaunchAction{}, false
	}
	return domain.ProjectLaunchAction{
		ID: project.ID + ":start.ps1", Title: "启动项目", ScriptPath: script,
		WorkingDirectory: root, Kind: "powershell",
	}, true
}

func projectLaunchUnavailableMessage() string {
	return "项目目录中没有通过安全校验的 start.ps1"
}
