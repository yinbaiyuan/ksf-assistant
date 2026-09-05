//go:build !windows

package localipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func normalizeIdentity(root string) string { return root }

func endpointDirectory(root string) string {
	return filepath.Join("/tmp", fmt.Sprintf("ksfa-%d", os.Getuid()), rootHash(root)[:32])
}

func privatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("local IPC endpoint must belong to current user and not be a symlink")
	}
	if directory {
		if !info.IsDir() || info.Mode().Perm() != 0700 {
			return errors.New("local IPC directory must have mode 0700")
		}
	} else if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return errors.New("local IPC socket must have mode 0600")
	}
	return nil
}

func endpointPath(root string, create bool) (string, error) {
	directory := endpointDirectory(root)
	for _, path := range []string{filepath.Dir(directory), directory} {
		if create {
			if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
				return "", err
			}
		}
		if err := privatePath(path, true); err != nil {
			return "", err
		}
	}
	return filepath.Join(directory, "rpc.sock"), nil
}

func listenEndpoint(root string) (net.Listener, func() error, error) {
	path, err := endpointPath(root, true)
	if err != nil {
		return nil, nil, err
	}
	descriptor, err := unix.Open(filepath.Join(filepath.Dir(path), "listener.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, nil, err
	}
	lock := os.NewFile(uintptr(descriptor), "localipc-listener-lock")
	info, err := lock.Stat()
	if err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		_ = lock.Close()
		return nil, nil, errors.New("local IPC listener lock is not private")
	}
	if err := unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, nil, fmt.Errorf("local IPC listener already owned: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		if err := privatePath(path, false); err != nil {
			_ = lock.Close()
			return nil, nil, err
		}
		if err := os.Remove(path); err != nil {
			_ = lock.Close()
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		_ = lock.Close()
		return nil, nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		_ = lock.Close()
		return nil, nil, err
	}
	return listener, lock.Close, nil
}

func dialEndpoint(ctx context.Context, root string) (net.Conn, error) {
	path, err := endpointPath(root, false)
	if err != nil {
		return nil, err
	}
	if err := privatePath(path, false); err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
