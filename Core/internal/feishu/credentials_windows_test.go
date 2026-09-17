//go:build windows

package feishu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsOfficialCredentialReaderAcceptsOnlyLegacyBOMCompatibility(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "official-sdk.json")
	fixture := []byte("\xef\xbb\xbf{\"schemaVersion\":1,\"appId\":\"cli_current\",\"brand\":\"feishu\",\"protectedSecret\":\"00\"}\n")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	var stored windowsOfficialCredentialFile
	missing, err := readWindowsOfficialCredentialFile(path, &stored)
	if err != nil || missing || stored.AppID != "cli_current" {
		t.Fatalf("legacy BOM credential was not read: missing=%v stored=%+v err=%v", missing, stored, err)
	}
	if err := os.WriteFile(path, append(fixture[:len(fixture)-2], []byte(",\"unknown\":true}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	stored = windowsOfficialCredentialFile{}
	if _, err := readWindowsOfficialCredentialFile(path, &stored); err == nil {
		t.Fatal("unknown credential field was accepted")
	}
}
