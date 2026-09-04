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
