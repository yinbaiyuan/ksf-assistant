//go:build windows

package privatestore

import (
	"os"

	"golang.org/x/sys/windows"
)

func WithFileLock(path string, action func() error) error {
	if err := EnsureDirectory(filepathDir(path)); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	var overlapped windows.Overlapped
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return err
	}
	defer windows.UnlockFileEx(handle, 0, 1, 0, &overlapped) //nolint:errcheck
	return action()
}
