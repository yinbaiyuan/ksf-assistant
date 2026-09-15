//go:build !windows

package userapproval

import (
	"golang.org/x/sys/unix"
	"os"
)

func tryLockFile(file *os.File) (func(), error) {
	return tryLockFileMode(file, false)
}
func tryLockFileMode(file *os.File, shared bool) (func(), error) {
	mode := unix.LOCK_EX
	if shared {
		mode = unix.LOCK_SH
	}
	if err := unix.Flock(int(file.Fd()), mode|unix.LOCK_NB); err != nil {
		return nil, err
	}
	return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }, nil
}
