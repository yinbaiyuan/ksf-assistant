package feishu

import (
	"context"
	"testing"
	"time"
)

func TestLegacyControlInboxRetiresWithoutExecutingAndPreservesRequest(t *testing.T) {
	inbox := NewControlInbox(t.TempDir())
	request := ControlRequest{ID: "CTL-12345678", Operation: "taskLink.interrupt", TaskKey: "desktop:thread-1", CreatedAt: time.Now().UTC()}
	if err := inbox.Submit(request); err == nil {
		t.Fatal("retired inbox accepted new request")
	}
	request.SchemaVersion = controlInboxSchema
	if err := writePrivateJSON(inbox.requestPath(request.ID), request); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Process(context.Background(), func(_ context.Context, value ControlRequest) error {
		t.Fatal("legacy request executed instead of retirement")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, found, err := inbox.TakeResult(request.ID)
	if err != nil || !found || result.Status != "failed" || result.ErrorClass != "legacy_request_requires_reconfirmation" {
		t.Fatalf("result=%#v found=%v err=%v", result, found, err)
	}
	if _, found, err := inbox.TakeResult(request.ID); err != nil || found {
		t.Fatalf("result must be one-shot: found=%v err=%v", found, err)
	}
	if err := inbox.Retire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := inbox.TakeResult(request.ID); err != nil || found {
		t.Fatal("retirement repeated after completed marker")
	}
	var preserved ControlRequest
	if missing, err := readPrivateJSON(inbox.requestPath(request.ID), &preserved); err != nil || missing || preserved.ID != request.ID {
		t.Fatal("legacy source lost", err)
	}

}

func TestControlInboxRejectsUnknownOperations(t *testing.T) {
	inbox := NewControlInbox(t.TempDir())
	err := inbox.Submit(ControlRequest{ID: "CTL-12345678", Operation: "arbitrary", TaskKey: "x", CreatedAt: time.Now().UTC()})
	if err == nil {
		t.Fatal("unknown control operation must be rejected")
	}
}
