package feishu

import "testing"

func TestEventInboxStoresOnlyMetadataAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	inbox := NewEventInbox(root)
	payload := []byte(`{"header":{"event_id":"evt-secret"},"event":{"text":"private message"}}`)
	record, created, err := inbox.Put("im.message.receive_v1", payload)
	if err != nil || !created {
		t.Fatalf("put failed: %v", err)
	}
	if record.TranscriptContentStored || record.EventFingerprint == "" || record.ContentFingerprint == "" {
		t.Fatalf("unsafe record: %#v", record)
	}
	if _, created, err = inbox.Put("im.message.receive_v1", payload); err != nil || created {
		t.Fatalf("duplicate accepted: %v", err)
	}
	if _, created, err = NewEventInbox(root).Put("im.message.receive_v1", payload); err != nil || created {
		t.Fatalf("duplicate accepted after restart: %v", err)
	}
}

func TestApprovalEventStoresOnlyFingerprintedMetadata(t *testing.T) {
	root := t.TempDir()
	inbox := NewEventInbox(root)
	payload := []byte(`{"header":{"event_id":"evt-approval"},"event":{"instance_code":"instance-secret","task_id":"task-secret","status":"APPROVED","form":"private form"}}`)
	record, created, err := inbox.Put(ApprovalTaskStatusChangedEvent, payload)
	if err != nil || !created {
		t.Fatalf("put approval event failed: created=%v err=%v", created, err)
	}
	if record.ResourceFingerprint == "" || record.ContentFingerprint != "" || record.ContentLength != 0 || record.TranscriptContentStored {
		t.Fatalf("unsafe approval event record: %#v", record)
	}
	items, err := inbox.Recent(1)
	if err != nil || len(items) != 1 {
		t.Fatalf("read approval event metadata failed: %#v %v", items, err)
	}
}
