package feishu

import (
	"context"
	"testing"
	"time"
)

func TestUnifiedExecutorRoutesSDKSendThroughOutboxOnce(t *testing.T) {
	root := t.TempDir()
	box := NewOutbox(root)
	sender := &recordingSender{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			_ = box.Process(context.Background(), sender, false)
			if len(sender.calls) > 0 {
				return
			}
		}
	}()
	executor := UnifiedCapabilityExecutor{DataRoot: root, LongTail: CapabilityExecutor{DataRoot: root, WorkingDirectory: root}}
	result, err := executor.ExecuteWithOptions(context.Background(), "im.sdk.message.send", map[string]any{
		"request-id": "OUT-test-123", "target-type": "open_id", "target-id": "ou_test",
		"format": "text", "text": "hello", "source": "test", "dry-run": false,
	}, CapabilityExecutionOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if len(sender.calls) != 1 || result["verified"] != true {
		t.Fatalf("result=%#v calls=%#v", result, sender.calls)
	}
}

func TestUnifiedExecutorRoutesGovernedWhiteboardThroughDocbox(t *testing.T) {
	root := t.TempDir()
	config := DefaultClientConfig()
	config.DocumentTargets["design"] = DocumentTarget{Kind: "docx_token", Value: "docx_private"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	box := NewDocbox(root)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			_ = box.Process(context.Background(), CapabilityExecutor{}, true)
			if readQueueHealth(root, "docbox", true).Processed > 0 {
				return
			}
		}
	}()
	executor := UnifiedCapabilityExecutor{DataRoot: root, LongTail: CapabilityExecutor{DataRoot: root, WorkingDirectory: root}}
	result, err := executor.ExecuteWithOptions(context.Background(), "docs.whiteboard.insert", map[string]any{
		"doc": "design", "content": "graph TD; A-->B", "doc-format": "mermaid",
	}, CapabilityExecutionOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if result["capabilityId"] != "docs.whiteboard.insert" {
		t.Fatalf("unexpected result: %#v", result)
	}
}
