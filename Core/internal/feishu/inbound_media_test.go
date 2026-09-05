package feishu

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInboundMessageDetailsExtractsPostResourcesWithoutKeysInPrompt(t *testing.T) {
	message := InboundMessage{MessageID: "om_test", MessageType: "post", Raw: map[string]any{"message": map[string]any{"content": `{"title":"周报","content":[[{"tag":"text","text":"请总结"},{"tag":"img","image_key":"img_private"},{"tag":"img","image_key":"img_private"}]]}`}}}
	text, resources, err := inboundMessageDetails(message)
	if err != nil {
		t.Fatal(err)
	}
	if text != "周报\n请总结" || len(resources) != 1 || resources[0].ResourceType != "image" {
		t.Fatalf("unexpected details: %q %#v", text, resources)
	}

}

func TestStageInboundMessageUsesPrivateScopedDirectoryAndCleans(t *testing.T) {
	root := t.TempDir()
	message := InboundMessage{MessageID: "om_private", MessageType: "file", Raw: map[string]any{"message": map[string]any{"content": `{"file_key":"file_private","file_name":"../../report.txt"}`}}}
	staged, err := StageInboundMessage(context.Background(), root, fakeResourceDownloader{}, message, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.Assets) != 1 || staged.Assets[0].DisplayName != "report.txt" || !strings.HasPrefix(staged.Assets[0].LocalPath, filepath.Join(root, "private-cache", "inbound-assets")) {
		t.Fatalf("unexpected staged asset: %#v", staged)
	}
	info, err := os.Stat(staged.Assets[0].LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("asset is not private: %#o", info.Mode().Perm())
	}
	if err := CleanupInboundAssets(root, staged.CleanupDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged.CleanupDir); !os.IsNotExist(err) {
		t.Fatal("staged directory was not removed")
	}
}

func TestStageInboundMessageRejectsOversizeAndRemovesPartialData(t *testing.T) {
	root := t.TempDir()
	message := InboundMessage{MessageID: "om_large", MessageType: "image", Raw: map[string]any{"message": map[string]any{"content": `{"image_key":"img_large"}`}}}
	if _, err := StageInboundMessage(context.Background(), root, fakeResourceDownloader{}, message, 4); err == nil {
		t.Fatal("oversize resource was accepted")
	}
	assetRoot := filepath.Join(root, "private-cache", "inbound-assets")
	entries, _ := os.ReadDir(assetRoot)
	if len(entries) != 0 {
		t.Fatalf("partial asset data remains: %#v", entries)
	}
}

type fakeResourceDownloader struct{}

func (fakeResourceDownloader) DownloadMessageResource(ctx context.Context, messageID, fileKey, kind, output string, timeout time.Duration) error {
	return os.WriteFile(output, []byte("private attachment"), 0o600)
}
