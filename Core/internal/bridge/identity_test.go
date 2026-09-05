package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKSFExternalSupportContractRemainsStable(t *testing.T) {
	t.Setenv("CODEX_USAGE_BAR_SUPPORT_DIR", "")
	root, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	wanted := "CODEX_USAGE_BAR_SUPPORT_DIR=" + filepath.Join(root, "CodexUsageBar")
	found := false
	for _, entry := range bridgeEnvironment() {
		if entry == wanted {
			found = true
		}
	}
	if !found {
		t.Fatal("external KSF support directory contract changed")
	}
	t.Setenv("CODEX_USAGE_BAR_SUPPORT_DIR", t.TempDir())
	for _, entry := range bridgeEnvironment() {
		if strings.HasPrefix(entry, "CODEX_USAGE_BAR_SUPPORT_DIR=") && entry != "CODEX_USAGE_BAR_SUPPORT_DIR="+os.Getenv("CODEX_USAGE_BAR_SUPPORT_DIR") {
			t.Fatal("explicit KSF support directory was overwritten")
		}
	}
}
