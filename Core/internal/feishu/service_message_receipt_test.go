package feishu

import (
	"context"
	"ksfassistant/core/internal/feishuprotocol"
	"testing"
)

func TestReadOnlyMessageReceiptNeverExecutesOrReprepares(t *testing.T) {
	client := &transportClientFixture{}
	_, transport := newTransportFixture(t, client)
	request := feishuprotocol.MessageRequest{TargetType: "open_id", TargetID: "ou_fixture", Format: "text", Content: "fixture", IdempotencyKey: "receipt-test"}
	id, state, err := transport.MessageReceipt(request.IdempotencyKey)
	if err != nil || state != "not_submitted" || id != "" {
		t.Fatal(state, err)
	}
	confirmTransportSend(t, transport, request)
	before := client.calls.Load()
	for i := 0; i < 3; i++ {
		id, state, err = transport.MessageReceipt(request.IdempotencyKey)
		if err != nil || state != "completed" || id == "" {
			t.Fatal(state, err)
		}
	}
	if client.calls.Load() != before {
		t.Fatal("receipt lookup performed another send")
	}
	// A second unknown key does not create a transport operation.
	_, _, _ = transport.MessageReceipt("never-written")
	_ = context.Background()
}
