package taskruntime

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func openNoFollow(path string, flags int, mode os.FileMode) (*os.File, error) {
	encoded, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	creation := uint32(windows.OPEN_EXISTING)
	if flags&os.O_RDWR != 0 {
		access |= windows.GENERIC_WRITE
	}
	if flags&os.O_CREATE != 0 {
		creation = windows.OPEN_ALWAYS
	}
	handle, err := windows.CreateFile(encoded, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, creation, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(handle)
		return nil, fail("unsafe_path", "reparse points and hard links are forbidden")
	}
	return os.NewFile(uintptr(handle), path), nil
}

func safeFileInfo(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink == 0 }

func tryLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlock(file *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}

func replaceFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func syncDirectory(string) error { return nil }
