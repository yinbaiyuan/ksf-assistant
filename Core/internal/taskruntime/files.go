package taskruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ksfassistant/core/internal/privatestore"
)

func validText(value string, maximum int) bool {
	return utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsFunc(value, unicode.IsControl)
}

func relativePath(value string) bool {
	if value == "" || !validText(value, 1024) || strings.ContainsAny(value, "\\:") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." || strings.TrimSpace(component) != component {
			return false
		}
	}
	return true
}

func safePath(root, relative string, createParents bool) (string, error) {
	if !relativePath(relative) {
		return "", fail("unsafe_path", "path must be a bounded workspace-relative path")
	}
	path := root
	parts := strings.Split(relative, "/")
	for index, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if index == len(parts)-1 {
				return path, nil
			}
			if !createParents {
				return "", os.ErrNotExist
			}
			if err = os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(path)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (index < len(parts)-1 && !info.IsDir()) {
			return "", fail("unsafe_path", "symlinks and non-directory ancestors are forbidden")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
			return "", fail("unsafe_path", "path is writable by other users")
		}
	}
	return path, nil
}

func readFile(path string, maximum int64, private bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximum || !safeFileInfo(info) {
		return nil, fail("unsafe_path", "file must be regular, bounded and not hard-linked")
	}
	if private && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fail("unsafe_path", "private file permissions must be 0600")
	}
	file, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !safeFileInfo(opened) || opened.Size() > maximum || (private && runtime.GOOS != "windows" && opened.Mode().Perm()&0o077 != 0) {
		return nil, fail("unsafe_path", "opened file is not safe and bounded")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fail("limit_exceeded", "file exceeds size limit")
	}
	return data, nil
}

func privateDirectory(path string) error {
	if err := privatestore.EnsureDirectory(path); err != nil {
		return fail("unsafe_path", "cannot secure private directory")
	}
	return nil
}

func withLock(ctx context.Context, path string, action func() error) error {
	if ctx.Err() != nil {
		return fail("busy", "task operation cancelled or timed out")
	}
	file, err := openNoFollow(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fail("unsafe_path", "cannot open lock file safely")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !safeFileInfo(info) || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return fail("unsafe_path", "unsafe lock file")
	}
	for {
		locked, err := tryLock(file)
		if err != nil {
			return fail("io_error", "cannot lock task record")
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			return fail("busy", "task lock wait cancelled or timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer unlock(file)
	if ctx.Err() != nil {
		return fail("busy", "task operation cancelled or timed out")
	}
	return action()
}

func atomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".task-*.tmp")
	if err != nil {
		return fail("io_error", "cannot create atomic task file")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return fail("io_error", "cannot sync task file")
	}
	if err = replaceFile(temporary, path); err != nil {
		return fail("io_error", "cannot atomically replace task file")
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return fail("io_error", "task committed but directory sync failed; retry identical event")
	}
	return nil
}
