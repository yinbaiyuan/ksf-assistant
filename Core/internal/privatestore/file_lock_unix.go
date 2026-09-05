//go:build !windows

package privatestore

import (
	"os"

	"golang.org/x/sys/unix"
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
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN) //nolint:errcheck
	return action()
}
