//go:build windows

package feishu

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockInstanceFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

func instanceProcessAlive(pid int) (bool, error) {
	if pid <= 0 || pid > 1<<31-1 {
		return false, errors.New("invalid Feishu instance pid")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, err
	}
	const stillActive = 259
	return exitCode == stillActive, nil
}
