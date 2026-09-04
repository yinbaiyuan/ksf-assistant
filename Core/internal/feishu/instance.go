package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type InstanceLock struct {
	path string
	file *os.File
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
	path := filepath.Join(root, "bridge.pid")
	var existing instanceRecord
	if missing, err := readInstanceRecord(path, &existing); err == nil && !missing {
		if processAlive(existing.PID) {
			return nil, fmt.Errorf("another feishu bridge instance is already running: pid %d", existing.PID)
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
	return &InstanceLock{path: path, file: file}, nil
}

func (lock *InstanceLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := lock.file.Close()
	lock.file = nil
	var current instanceRecord
	if missing, readErr := readInstanceRecord(lock.path, &current); readErr == nil && !missing && current.PID == os.Getpid() {
		if removeErr := os.Remove(lock.path); err == nil {
			err = removeErr
		}
	}
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
		return true, false, 0, record.StartedAt, errors.New("invalid Feishu bridge pid")
	}
	return true, processAlive(record.PID), record.PID, record.StartedAt, nil
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
		return false, errors.New("unsafe Feishu bridge pid file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return false, errors.New("writable Feishu bridge pid file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if json.Unmarshal(data, target) != nil {
		return false, errors.New("invalid Feishu bridge pid file")
	}
	return false, nil
}
