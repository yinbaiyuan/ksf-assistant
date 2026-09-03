//go:build !windows

package bridge

import (
	"os/exec"
	"syscall"
)

type transientProcessTree struct{ processGroupID int }

func prepareTransientProcessTree(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachTransientProcessTree(command *exec.Cmd) (transientProcessTree, error) {
	return transientProcessTree{processGroupID: command.Process.Pid}, nil
}

func terminateTransientProcessTree(tree transientProcessTree, command *exec.Cmd) {
	if tree.processGroupID > 0 {
		_ = syscall.Kill(-tree.processGroupID, syscall.SIGKILL)
		return
	}
	_ = command.Process.Kill()
}

func closeTransientProcessTree(transientProcessTree) {}
