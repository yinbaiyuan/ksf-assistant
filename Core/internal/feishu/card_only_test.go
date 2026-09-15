package feishu

import (
	"context"
	"strings"
	"testing"
)

func TestCardOnlyRejectsBusinessAuthorizationBeforeCLI(t *testing.T) {
	_, err := startUserAuthSession(context.Background(), CapabilityExecutor{}, t.TempDir(), "request:0123456789abcdef0123456789abcdef")
	if err == nil || !strings.Contains(err.Error(), "agent_business_authorization_removed") {
		t.Fatal(err)
	}
}

func TestCardOnlyConfirmationRejectsLegacyOperation(t *testing.T) {
	root := t.TempDir()
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	definition, ok := CapabilityByID("im.chat.create")
	if !ok {
		t.Fatal("fixture capability missing")
	}
	view, challenge, err := service.operations.PrepareWithEvidence(definition, map[string]any{"name": "legacy"}, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ConfirmServiceMessage(context.Background(), view.ID, challenge)
	if err == nil || !strings.Contains(err.Error(), "agent_feishu_middleware_removed") {
		t.Fatal(err)
	}
}
