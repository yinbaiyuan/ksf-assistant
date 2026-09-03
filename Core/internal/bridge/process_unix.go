//go:build !windows

package bridge

import "os/exec"

func setHiddenWindow(_ *exec.Cmd) {}
