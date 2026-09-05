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

func TestUnifiedExecutorRoutesGovernedWhiteboardThroughDocbox(t *testing.T) {
	root := t.TempDir()
	config := DefaultClientConfig()
	config.DocumentTargets["design"] = DocumentTarget{Kind: "docx_token", Value: "docx_private"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"doc": "design", "content": "graph TD; A-->B", "doc-format": "mermaid"}
	ctx, operationID := reviewRunningBoundary(t, root, "docs.whiteboard.insert", input)
	transport := &reviewDocumentTransport{}
	executor := UnifiedCapabilityExecutor{DataRoot: root, Documents: transport}
	result, err := executor.ExecuteWithOptions(ctx, "docs.whiteboard.insert", input, CapabilityExecutionOptions{OperationID: operationID, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result["capabilityId"] != "docs.whiteboard.insert" || transport.writes.Load() != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	reviewAssertNoChildWork(t, root, "docbox")
}

func TestUnifiedExecutorRejectsUnboundDirectWrite(t *testing.T) {
	sender := &reviewConcurrentSender{}
	executor := UnifiedCapabilityExecutor{DataRoot: t.TempDir(), Sender: sender}
	_, err := executor.ExecuteWithOptions(context.Background(), "im.sdk.message.send", map[string]any{}, CapabilityExecutionOptions{})
	if err == nil || sender.calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", err, sender.calls.Load())
	}
}
