//go:build windows

package feishu

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func purgeManagedPlatformCredentialStorage() error {
	err := registry.DeleteKey(registry.CURRENT_USER, `Software\LarkCli\keychain\`+ManagedLarkCLIKeyService)
	if err == nil || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	return err
}
