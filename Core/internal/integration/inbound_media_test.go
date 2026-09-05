package integration

import (
	"strings"
	"testing"
)

func TestInboundAssetsPromptDoesNotExposeResourceKeys(t *testing.T) {
	prompt := InboundAssetsPrompt("请总结", StagedInboundMessage{Assets: []InboundAsset{{MessageType: "post", ResourceType: "image", DisplayName: "image", LocalPath: "/tmp/image", SizeBytes: 3}}})
	if !strings.Contains(prompt, "/tmp/image") || strings.Contains(prompt, "img_private") {
		t.Fatalf("unsafe prompt: %s", prompt)
	}
}
