package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"codexusagebar/core/internal/domain"
)

func TestTaskKeyMatchesBridgeContract(t *testing.T) {
	if value := domain.PublicTaskKey("thread-1"); value != "4b0a5fefc328e6b9257b" {
		t.Fatalf("unexpected task key: %s", value)
	}
}

func TestPrivateQRStaysInsideBridgeDataRoot(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", dataRoot)
	authRoot := filepath.Join(dataRoot, "auth")
	if err := os.MkdirAll(authRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	inside := filepath.Join(authRoot, "user-oauth.png")
	if err := os.WriteFile(inside, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateQR(inside); err != nil {
		t.Fatalf("expected private QR to pass: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "user-oauth.png")
	if err := os.WriteFile(outside, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateQR(outside); err == nil {
		t.Fatal("expected QR outside private data root to be rejected")
	}
}
