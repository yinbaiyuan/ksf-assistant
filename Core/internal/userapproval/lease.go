package userapproval

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"ksfassistant/core/internal/privatestore"
)

func TryExecutionLease(root string) (func(), error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("approval_authorization_root_invalid")
	}
	if err := privatestore.EnsureDirectory(root); err != nil {
		return nil, errors.New("approval_authorization_unavailable")
	}
	path := filepath.Join(root, "user-authorization-execution-v1.lock")
	before, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("approval_authorization_unavailable")
	}
	if err == nil && (!before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("approval_authorization_unavailable")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, errors.New("approval_authorization_unavailable")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || before != nil && !os.SameFile(before, info) {
		file.Close()
		return nil, errors.New("approval_authorization_unavailable")
	}
	after, err := os.Lstat(path)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, after) {
		file.Close()
		return nil, errors.New("approval_authorization_unavailable")
	}
	unlock, err := tryLockFile(file)
	if err != nil {
		file.Close()
		return nil, errors.New("approval_authorization_busy")
	}
	var once sync.Once
	return func() { once.Do(func() { unlock(); file.Close() }) }, nil
}
