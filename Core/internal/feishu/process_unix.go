//go:build !windows

package feishu

import (
	"os"
	"os/exec"
	"syscall"
)

type processTree struct{ processGroupID int }

func prepareProcessTree(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcessTree(command *exec.Cmd) (processTree, error) {
	return processTree{processGroupID: command.Process.Pid}, nil
}

func interruptProcessTree(tree processTree, command *exec.Cmd) {
	if tree.processGroupID > 0 {
		_ = syscall.Kill(-tree.processGroupID, syscall.SIGINT)
		return
	}
	_ = command.Process.Signal(os.Interrupt)
}

func killProcessTree(tree processTree, command *exec.Cmd) {
	if tree.processGroupID > 0 {
		_ = syscall.Kill(-tree.processGroupID, syscall.SIGKILL)
		return
	}
	_ = command.Process.Kill()
}

func closeProcessTree(tree processTree) {
	if tree.processGroupID > 0 {
		_ = syscall.Kill(-tree.processGroupID, syscall.SIGKILL)
	}
}
