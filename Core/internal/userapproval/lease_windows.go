package userapproval

import (
	"golang.org/x/sys/windows"
	"os"
)

func tryLockFile(file *os.File) (func(), error) {
	return tryLockFileMode(file, false)
}
func tryLockFileMode(file *os.File, shared bool) (func(), error) {
	handle := windows.Handle(file.Fd())
	var overlapped windows.Overlapped
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if !shared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if err := windows.LockFileEx(handle, flags, 0, 1, 0, &overlapped); err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, &overlapped) }, nil
}
