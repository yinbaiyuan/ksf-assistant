//go:build !windows

package codex

import "os/exec"

func setHiddenWindow(_ *exec.Cmd) {}
