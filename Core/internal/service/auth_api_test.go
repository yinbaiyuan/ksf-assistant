package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"ksfassistant/core/internal/feishuprotocol"
)

func TestDesktopAuthRejectsExpandedScopesBeforeCallingBridge(t *testing.T) {
	_, err := (&Service{}).StartDesktopFeishuAuth(context.Background(), feishuprotocol.AuthStartRequest{Scope: "all private-value"})
	if err == nil || strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), "服务不可用") {
		t.Fatalf("expanded scopes crossed service boundary: %v", err)
	}
}

func TestDesktopSetupCancelDoesNotResetStateWithoutBridgeAcknowledgment(t *testing.T) {
	root := t.TempDir()
	service := &Service{feishuDataRoot: root}
	state, err := service.CancelFeishuSetup()
	if err == nil || state.Stage != "" {
		t.Fatalf("cancel falsely succeeded: %+v, %v", state, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed cancel wrote local setup state")
	}
}
