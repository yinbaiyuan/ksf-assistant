package feishu

import (
	"context"
	"testing"
	"time"
)

func TestUnifiedExecutorRoutesSDKSendThroughOutboxOnce(t *testing.T) {
	root := t.TempDir()
	sender := &recordingSender{}
	input := map[string]any{"request-id": "OUT-test-123", "target-type": "open_id", "target-id": "ou_test", "format": "text", "text": "hello", "source": "test", "dry-run": false}
	ctx, operationID := reviewRunningBoundary(t, root, "im.sdk.message.send", input)
	executor := UnifiedCapabilityExecutor{DataRoot: root, Sender: sender}
	result, err := executor.ExecuteWithOptions(ctx, "im.sdk.message.send", input, CapabilityExecutionOptions{OperationID: operationID, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 1 || result["verified"] != true {
		t.Fatalf("result=%#v calls=%#v", result, sender.calls)
	}
	reviewAssertNoChildWork(t, root, "outbox")
}

func TestUnifiedExecutorRejectsUnboundDirectWrite(t *testing.T) {
	sender := &reviewConcurrentSender{}
	executor := UnifiedCapabilityExecutor{DataRoot: t.TempDir(), Sender: sender}
	_, err := executor.ExecuteWithOptions(context.Background(), "im.sdk.message.send", map[string]any{}, CapabilityExecutionOptions{})
	if err == nil || sender.calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", err, sender.calls.Load())
	}
}
