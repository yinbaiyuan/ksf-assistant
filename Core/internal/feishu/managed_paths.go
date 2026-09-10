package feishu

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const ManagedLarkCLIKeyService = "ksfassistant-lark-cli"

// ManagedLarkCLIConfigDir is the single credential root owned by
// KSFAssistant. It intentionally ignores LARKSUITE_CLI_CONFIG_DIR so a parent
// shell cannot redirect managed authorization into the user's own CLI state.
func ManagedLarkCLIConfigDir(dataRoot string) (string, error) {
	if strings.TrimSpace(dataRoot) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("managed lark-cli home is unavailable")
		}
		dataRoot = filepath.Join(home, ".config", "feishu-bridge")
	}
	if !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot || strings.ContainsAny(dataRoot, "\x00\r\n") {
		return "", errors.New("managed Feishu data root is invalid")
	}
	return filepath.Join(dataRoot, "lark-cli"), nil
}
