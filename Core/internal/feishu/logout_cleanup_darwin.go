//go:build darwin

package feishu

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

func purgeManagedPlatformCredentialStorage() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	storage := filepath.Join(home, "Library", "Application Support", ManagedLarkCLIKeyService)
	if err := os.RemoveAll(storage); err != nil {
		return err
	}
	command := exec.Command("/usr/bin/security", "delete-generic-password", "-s", ManagedLarkCLIKeyService, "-a", "master.key")
	if err := command.Run(); err != nil {
		// Exit 44 means the item does not exist and is already clean.
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 44 {
			return errors.New("无法删除 KSFAssistant 飞书专属主密钥")
		}
	}
	return nil
}
