package localipc

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func normalizeRoot(root string) (string, error) {
	file, err := os.Open(root)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var path [unix.PathMax]byte
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), unix.F_GETPATH, uintptr(unsafe.Pointer(&path[0])))
	if errno != 0 {
		return "", errno
	}
	return unix.ByteSliceToString(path[:]), nil
}
