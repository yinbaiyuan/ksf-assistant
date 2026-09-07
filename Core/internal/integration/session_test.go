package integration

import (
	"context"
	"ksfassistant/core/internal/capabilitypolicy"
	"testing"
)

func TestSignedOutNeverInvokesCodexForMessagesOrCards(t *testing.T) {
	root := t.TempDir()
	if err := capabilitypolicy.SignOut(root); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{dataRoot: root}
	if err := runtime.HandleMessage(context.Background(), InboundMessage{}); err != capabilitypolicy.ErrSignedOut {
		t.Fatal(err)
	}
	if err := runtime.HandleCard(context.Background(), InboundCardAction{}); err != capabilitypolicy.ErrSignedOut {
		t.Fatal(err)
	}
}
