//go:build windows

package bridge

import (
	"os/exec"
	"syscall"
)

func setHiddenWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
