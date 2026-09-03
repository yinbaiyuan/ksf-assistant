//go:build windows

package codex

import (
	"os/exec"
	"syscall"
)

func setHiddenWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
