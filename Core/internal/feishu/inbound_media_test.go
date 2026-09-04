package feishu

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	prompt := InboundAssetsPrompt(text, StagedInboundMessage{Assets: []InboundAsset{{MessageType: "post", ResourceType: "image", DisplayName: "image", LocalPath: "/tmp/image", SizeBytes: 3}}})
	if !strings.Contains(prompt, "/tmp/image") || strings.Contains(prompt, "img_private") {
		t.Fatalf("unsafe prompt: %s", prompt)
	}
}

func TestStageInboundMessageUsesPrivateScopedDirectoryAndCleans(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "fake-lark-cli")
	script := `#!/bin/sh
output=""
previous=""
for value in "$@"; do
  if [ "$previous" = "--output" ]; then output="$value"; fi
  previous="$value"
done
mkdir -p "$(dirname "$output")"
printf 'private attachment' > "$output"
printf '{}\n'
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	message := InboundMessage{MessageID: "om_private", MessageType: "file", Raw: map[string]any{"message": map[string]any{"content": `{"file_key":"file_private","file_name":"../../report.txt"}`}}}
	staged, err := StageInboundMessage(context.Background(), CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root}, message, 1024)
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
	if info.Mode().Perm() != 0o600 {
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
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "fake-lark-cli")
	script := `#!/bin/sh
previous=""
for value in "$@"; do
  if [ "$previous" = "--output" ]; then printf 'too large' > "$value"; fi
  previous="$value"
done
printf '{}\n'
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	message := InboundMessage{MessageID: "om_large", MessageType: "image", Raw: map[string]any{"message": map[string]any{"content": `{"image_key":"img_large"}`}}}
	if _, err := StageInboundMessage(context.Background(), CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root}, message, 4); err == nil {
		t.Fatal("oversize resource was accepted")
	}
	assetRoot := filepath.Join(root, "private-cache", "inbound-assets")
	entries, _ := os.ReadDir(assetRoot)
	if len(entries) != 0 {
		t.Fatalf("partial asset data remains: %#v", entries)
	}
}
