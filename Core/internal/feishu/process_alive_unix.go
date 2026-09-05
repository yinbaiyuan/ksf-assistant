//go:build !windows

package feishu

import (
	"errors"
	"syscall"
)

func processAlive(pid int) bool {
	alive, err := instanceProcessAlive(pid)
	return alive || err != nil
}

func instanceProcessAlive(pid int) (bool, error) {
	if pid <= 0 || pid > 1<<31-1 {
		return false, errors.New("invalid Feishu instance pid")
	}
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
