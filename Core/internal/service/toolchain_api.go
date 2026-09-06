package service

import (
	"errors"
	"os"
	"path/filepath"

	"ksfassistant/core/internal/toolchain"
)

type ToolchainInstallRequest struct {
	Confirm bool `json:"confirm"`
}

type ToolchainStatus struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Version       string                  `json:"version"`
	Installed     bool                    `json:"installed"`
	Healthy       bool                    `json:"healthy"`
	Skills        []toolchain.SkillStatus `json:"skills"`
	ProblemCount  int                     `json:"problemCount"`
}

type desktopToolchain interface {
	Status() (toolchain.Status, error)
	Install() (toolchain.Status, error)
}

func (service *Service) desktopToolchain() (desktopToolchain, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("无法定位官方工具链，请重新安装应用")
	}
	manager, err := toolchain.New(toolchain.Config{
		ResourcesDir: filepath.Dir(filepath.Dir(filepath.Dir(executable))),
		HomeDir:      service.home,
		Profile:      "default",
		ConfigDir:    os.Getenv("LARKSUITE_CLI_CONFIG_DIR"),
		DataRoot:     service.feishuDataRoot,
	})
	if err != nil {
		return nil, errors.New("官方工具链配置不可用，请重新安装应用")
	}
	return manager, nil
}

func (service *Service) ToolchainStatus() (ToolchainStatus, error) {
	manager, err := service.desktopToolchain()
	if err != nil {
		return ToolchainStatus{}, err
	}
	return readDesktopToolchain(manager, false)
}

func (service *Service) InstallToolchain(request ToolchainInstallRequest) (ToolchainStatus, error) {
	if !request.Confirm {
		return ToolchainStatus{}, errors.New("安装官方工具链需要明确确认")
	}
	manager, err := service.desktopToolchain()
	if err != nil {
		return ToolchainStatus{}, err
	}
	return readDesktopToolchain(manager, true)
}

func readDesktopToolchain(manager desktopToolchain, install bool) (ToolchainStatus, error) {
	var status toolchain.Status
	var err error
	if install {
		status, err = manager.Install()
	} else {
		status, err = manager.Status()
	}
	if err != nil {
		return ToolchainStatus{}, errors.New("官方工具链操作未完成，请检查安装或文件冲突后重试")
	}
	skills := append([]toolchain.SkillStatus{}, status.Skills...)
	return ToolchainStatus{
		SchemaVersion: 1, Version: status.Version, Installed: status.Installed,
		Healthy: status.Healthy, Skills: skills, ProblemCount: len(status.Problems),
	}, nil
}
