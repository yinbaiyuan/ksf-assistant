package feishu

import (
	"context"
	"testing"
	"time"
)

func TestControlInboxSubmitsProcessesAndReturnsOneShotResult(t *testing.T) {
	inbox := NewControlInbox(t.TempDir())
	request := ControlRequest{ID: "CTL-12345678", Operation: "taskLink.interrupt", TaskKey: "desktop:thread-1", CreatedAt: time.Now().UTC()}
	if err := inbox.Submit(request); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Process(context.Background(), func(_ context.Context, value ControlRequest) error {
		if value.TaskKey != request.TaskKey {
			t.Fatalf("request = %#v", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, found, err := inbox.TakeResult(request.ID)
	if err != nil || !found || result.Status != "succeeded" {
		t.Fatalf("result=%#v found=%v err=%v", result, found, err)
	}
	if _, found, err := inbox.TakeResult(request.ID); err != nil || found {
		t.Fatalf("result must be one-shot: found=%v err=%v", found, err)
	}
}

func TestControlInboxRejectsUnknownOperations(t *testing.T) {
	inbox := NewControlInbox(t.TempDir())
	err := inbox.Submit(ControlRequest{ID: "CTL-12345678", Operation: "arbitrary", TaskKey: "x", CreatedAt: time.Now().UTC()})
	if err == nil {
		t.Fatal("unknown control operation must be rejected")
	}
}
