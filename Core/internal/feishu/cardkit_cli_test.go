package feishu

import (
	"bytes"
	"context"
	"ksfassistant/core/internal/userapproval"
	"ksfassistant/core/internal/usercommand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCardKitWaitsForLocalLeaseWithoutSendingTwice(t *testing.T) {
	runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{}}`, "0")
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	time.AfterFunc(time.Second, release)
	_, err = runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "cardkit", Method: "settings", Params: map[string]string{"card_id": "123"}, Body: map[string]any{"settings": `{"config":{"streaming_mode":false}}`, "sequence": 1, "uuid": "close-1"}})
	if err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
	if strings.Count(string(calls), "executed") != 1 {
		t.Fatal("request did not execute exactly once")
	}
}

func TestCardKitLeaseCancellationDoesNotAcquire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := cardKitExecutionLease(ctx, t.TempDir()); err != context.Canceled || release != nil {
		t.Fatal("cancelled lease acquired", err)
	}
}

func TestCardKitUpdatePassesManagedContract(t *testing.T) {
	args, err := cardKitCommand(MessageCLIRequest{Resource: "cardkit", Method: "content", Params: map[string]string{"card_id": "7355372766134157313", "element_id": "probe_body"}, Body: map[string]any{"content": "累计正文", "sequence": 1, "uuid": "native-20260915-0945-1"}})
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "--data", "-")
	frozen, err := usercommand.FreezeAt(capabilityOutputArguments(args), bytes.NewBufferString(`{"content":"累计正文","sequence":1,"uuid":"native-20260915-0945-1"}`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	frozen.Identity = usercommand.Identity{AppID: "cli_fixture", ApplicationName: "Fixture app", Brand: "feishu", Profile: "default"}
	if _, err := usercommand.Evaluate(frozen); err != nil {
		t.Fatal(err)
	}
}

func TestCardKitRestrictedTextOperation(t *testing.T) {
	runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{}}`, "0")
	_, err := runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "cardkit", Method: "content", Params: map[string]string{"card_id": "7355372766134157313", "element_id": "body_1"}, Body: map[string]any{"content": "累计正文", "sequence": 2, "uuid": "probe-2"}})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(runner.DataRoot, "args"))
	if !strings.Contains(string(args), "PUT\n/open-apis/cardkit/v1/cards/7355372766134157313/elements/body_1/content\n") || strings.Contains(string(args), "累计正文") {
		t.Fatalf("unexpected argv: %s", args)
	}
}

func TestCardKitRawBusinessExecutionIsNotPublic(t *testing.T) {
	runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{}}`, "0")
	_, err := runner.runBusinessCommand(context.Background(), CapabilityDefinition{ID: "im.message.edit", Identity: "bot", Risk: "write"}, []string{"api", "PATCH", "/open-apis/cardkit/v1/cards/123/settings", "--as", "bot", "--data", "-"}, []byte(`{}`), runner.DataRoot, time.Second)
	if err == nil || err.Error() != "cardkit_internal_transport_required" {
		t.Fatal(err)
	}
}

func TestCardKitRejectsInvalidRequestsBeforeExecution(t *testing.T) {
	for _, req := range []MessageCLIRequest{
		{Resource: "cardkit", Method: "delete"},
		{Resource: "cardkit", Method: "content", Params: map[string]string{"card_id": "../messages", "element_id": "x"}},
		{Resource: "cardkit", Method: "content", Params: map[string]string{"card_id": "123", "element_id": "x"}, Body: map[string]any{"content": "x", "sequence": 0, "uuid": "x"}},
		{Resource: "cardkit", Method: "content", Params: map[string]string{"card_id": "123", "element_id": "x"}, Body: map[string]any{"content": "x", "sequence": 1, "uuid": "x", "url": "https://example.com"}},
	} {
		runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{}}`, "0")
		if _, err := runner.CallMessage(context.Background(), req); err == nil {
			t.Fatal("accepted invalid request")
		}
		if _, err := os.Stat(filepath.Join(runner.DataRoot, "calls")); !os.IsNotExist(err) {
			t.Fatal("invalid request reached CLI")
		}
	}
}
