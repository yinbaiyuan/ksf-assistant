package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type InstanceLock struct {
	path  string
	file  *os.File
	guard *os.File
}
type instanceRecord struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
}

func AcquireInstanceLock(dataRoot string) (*InstanceLock, error) {
	root := filepath.Join(dataRoot, "logs")
	if err := ensurePrivateDirectory(root); err != nil {
		return nil, err
	}
	guard, err := acquireInstanceGuard(filepath.Join(root, "bridge.instance.lock"))
	if err != nil {
		return nil, err
	}
	keepGuard := false
	defer func() {
		if !keepGuard {
			guard.Close()
		}
	}()
	path := filepath.Join(root, "bridge.pid")
	var existing instanceRecord
	if missing, err := readInstanceRecord(path, &existing); err == nil && !missing {
		alive, err := instanceProcessAlive(existing.PID)
		if err != nil {
			return nil, err
		}
		if alive {
			return nil, fmt.Errorf("another KSFAssistant Feishu instance is already running: pid %d", existing.PID)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	record := instanceRecord{PID: os.Getpid(), StartedAt: time.Now().UTC()}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	keepGuard = true
	return &InstanceLock{path: path, file: file, guard: guard}, nil
}

func acquireInstanceGuard(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if !safeInstanceFile(info) {
			return nil, errors.New("unsafe Feishu instance lock file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockInstanceFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("Feishu instance lock unavailable: %w", err)
	}
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil {
		file.Close()
		return nil, errors.Join(statErr, pathErr)
	}
	if !safeInstanceFile(current) || !os.SameFile(opened, current) {
		file.Close()
		return nil, errors.New("unsafe Feishu instance lock file")
	}
	return file, nil
}

func safeInstanceFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0o022 == 0)
}

func (lock *InstanceLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := lock.file.Close()
	lock.file = nil
	var current instanceRecord
	missing, readErr := readInstanceRecord(lock.path, &current)
	err = errors.Join(err, readErr)
	if readErr == nil && !missing && current.PID == os.Getpid() {
		err = errors.Join(err, os.Remove(lock.path))
	}
	err = errors.Join(err, lock.guard.Close())
	lock.guard = nil
	return err
}

func InstanceStatus(dataRoot string) (present, alive bool, pid int, startedAt time.Time, err error) {
	var record instanceRecord
	missing, err := readInstanceRecord(filepath.Join(dataRoot, "logs", "bridge.pid"), &record)
	if missing {
		return false, false, 0, time.Time{}, nil
	}
	if err != nil {
		return false, false, 0, time.Time{}, err
	}
	if record.PID <= 0 {
		return true, false, 0, record.StartedAt, errors.New("invalid KSFAssistant Feishu pid")
	}
	alive, err = instanceProcessAlive(record.PID)
	return true, alive, record.PID, record.StartedAt, err
}

func readInstanceRecord(path string, target *instanceRecord) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 {
		return false, errors.New("unsafe KSFAssistant Feishu pid file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return false, errors.New("writable KSFAssistant Feishu pid file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if json.Unmarshal(data, target) != nil || target.PID <= 0 || target.PID > 1<<31-1 {
		return false, errors.New("invalid KSFAssistant Feishu pid file")
	}
	return false, nil
}
