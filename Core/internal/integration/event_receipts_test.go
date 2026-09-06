package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ksfassistant/core/internal/privatestore"
)

func completedInboxFixture(t *testing.T, count int) eventInboxFile {
	t.Helper()
	file := eventInboxFile{SchemaVersion: 1}
	for index := 0; index < count; index++ {
		event := messageEvent(fmt.Sprintf("completed-%d", index), "fixture")
		_, _, digest, err := decodeEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		event.Payload = nil
		file.Events = append(file.Events, inboxEvent{Event: event, Digest: digest, State: "completed", AcceptedAt: time.Now(), FinishedAt: time.Now()})
	}
	return file
}

func TestTerminalReceiptsDoNotConsumeLiveInboxCapacity(t *testing.T) {
	root := t.TempDir()
	original := completedInboxFixture(t, eventInboxMaxRecords)
	if err := privatestore.WriteJSON(filepath.Join(root, "integration-events-v1.json"), original); err != nil {
		t.Fatal(err)
	}
	messages := &fakeFeishuPort{}
	runtime, err := NewRuntime(root, messages, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if len(runtime.inbox.file.Events) != recentEventReceipts {
		t.Fatal("terminal history was not compacted")
	}
	for _, event := range []struct {
		id, chat string
		accepted bool
	}{{"completed-0", "fixture", true}, {"completed-0", "conflict", false}, {"new-event", "fixture", true}} {
		result, err := runtime.AcceptEvent(context.Background(), messageEvent(event.id, event.chat))
		if result.Accepted != event.accepted || (err == nil) != event.accepted {
			t.Fatalf("event=%s accepted=%v err=%v", event.id, result.Accepted, err)
		}
	}
	waitInboxState(t, runtime, "new-event", "completed")
	if messages.calls.Load() != 1 {
		t.Fatal("archived event replayed")
	}
	var backup eventInboxFile
	if missing, err := privatestore.ReadJSON(filepath.Join(root, "integration-event-backups", "inbox-v1.json"), &backup); missing || err != nil || len(backup.Events) != eventInboxMaxRecords {
		t.Fatalf("migration backup missing: %v", err)
	}
}

func TestReceiptArchiveFailurePreservesInboxAndBlocksMigration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "integration-events-v1.json")
	if err := privatestore.WriteJSON(path, completedInboxFixture(t, recentEventReceipts+1)); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	if err := os.WriteFile(filepath.Join(root, "integration-event-receipts-v1"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newEventInbox(root); err == nil {
		t.Fatal("unsafe archive accepted")
	}
	current, _ := os.ReadFile(path)
	if string(original) != string(current) {
		t.Fatal("failed migration changed active file")
	}
	if _, err := os.Stat(filepath.Join(root, "integration-event-receipts-v1-migration.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed migration marked complete")
	}
}

func TestArchivedReceiptWinsCrashBeforeLiveReplacement(t *testing.T) {
	root := t.TempDir()
	inbox, err := newEventInbox(root)
	if err != nil {
		t.Fatal(err)
	}
	event := messageEvent("cross-file-crash", "chat")
	_, _, digest, _ := decodeEvent(event)
	terminal := inboxEvent{Event: event, Digest: digest, State: "outcome_unknown", Phase: "executing", FinishedAt: time.Now()}
	if err := inbox.archive(terminal); err != nil {
		t.Fatal(err)
	}
	stale := terminal
	stale.State, stale.Phase = "pending", "preparing"
	if err := privatestore.WriteJSON(inbox.path, eventInboxFile{SchemaVersion: 1, Events: []inboxEvent{stale}}); err != nil {
		t.Fatal(err)
	}
	restarted, err := newEventInbox(root)
	if err != nil || restarted.file.Events[0].State != "outcome_unknown" || len(restarted.file.Events[0].Event.Payload) == 0 {
		t.Fatalf("archived outcome lost: %v", err)
	}
	if err := os.WriteFile(restarted.receiptPath(event.ID), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newEventInbox(root); err == nil {
		t.Fatal("corrupt receipt accepted")
	}
}

func TestEventPhaseRecoveryAndBoundedPreExecutionRetries(t *testing.T) {
	for _, phase := range []string{"preparing", "executing", ""} {
		t.Run("restart-"+phase, func(t *testing.T) {
			root := t.TempDir()
			event := messageEvent("phase-event", "chat")
			_, _, digest, _ := decodeEvent(event)
			file := eventInboxFile{SchemaVersion: 1, Events: []inboxEvent{{Event: event, Digest: digest, State: "running", Phase: phase, Attempts: 1}}}
			if err := privatestore.WriteJSON(filepath.Join(root, "integration-events-v1.json"), file); err != nil {
				t.Fatal(err)
			}
			inbox, err := newEventInbox(root)
			if err != nil {
				t.Fatal(err)
			}
			want := "outcome_unknown"
			if phase == "preparing" {
				want = "pending"
			}
			if inbox.file.Events[0].State != want || len(inbox.file.Events[0].Event.Payload) == 0 {
				t.Fatal("unsafe phase recovery")
			}
		})
	}
	runtime := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	event := messageEvent("retry-exhausted", "chat")
	_, _, digest, _ := decodeEvent(event)
	if err := runtime.inbox.save(eventInboxFile{SchemaVersion: 1, Events: []inboxEvent{{Event: event, Digest: digest, State: "pending"}}}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		record, found, err := runtime.inbox.claim(0)
		if err != nil || !found || record.Attempts != attempt {
			t.Fatalf("claim=%#v err=%v", record, err)
		}
		if err := runtime.inbox.finish(record, errors.New("temporary"), false); err != nil {
			t.Fatal(err)
		}
		if attempt < 5 {
			if _, found, _ := runtime.inbox.claim(0); found {
				t.Fatal("backoff ignored")
			}
			runtime.inbox.file.Events[0].RetryAt = time.Time{}
		}
	}
	if record := runtime.inbox.file.Events[0]; record.State != "failed" || len(record.Event.Payload) == 0 || runtime.Health().State != "degraded" {
		t.Fatal("exhausted event lost or hidden")
	}
}
