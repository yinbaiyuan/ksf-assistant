package privatestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const maximumPrivateJSONBytes = 1 << 20

func ReadJSON(path string, target any) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumPrivateJSONBytes {
		return false, errors.New("unsafe private JSON file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return false, errors.New("insecure private JSON permissions")
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maximumPrivateJSONBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return false, errors.New("private JSON contain trailing data")
	}
	return false, nil
}

func WriteJSON(path string, value any) error {
	root := filepath.Dir(path)
	if err := EnsureDirectory(root); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := filepath.Join(root, fmt.Sprintf(".%s.%d.tmp", filepath.Base(path), os.Getpid()))
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	clean := true
	defer func() {
		if clean {
			_ = os.Remove(temporary)
		}
	}()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := replacePrivateFile(temporary, path); err != nil {
		return err
	}
	clean = false
	if runtime.GOOS != "windows" {
		return os.Chmod(path, 0o600)
	}
	return securePrivatePath(path, false)
}

func EnsureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe private JSON directory")
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(path, 0o700)
	}
	return securePrivatePath(path, true)
}
