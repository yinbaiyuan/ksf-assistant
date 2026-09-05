package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

func waitHealthDetail(t *testing.T, runtime *Runtime, wanted string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if health := runtime.Health(); health.State == "degraded" && strings.Contains(health.Detail, wanted) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing health error %s: %#v", wanted, runtime.Health())
}

func TestClaimPersistenceFailureIsVisibleAndDoesNotDispatch(t *testing.T) {
	messages := &fakeFeishuPort{}
	runtime := testRuntime(t, &fakeCorePort{}, messages)
	event := messageEvent("claim-failure", "chat")
	_, _, digest, _ := decodeEvent(event)
	runtime.inbox.file.Events = []inboxEvent{{Event: event, Digest: digest, State: "pending", Partition: 0, AcceptedAt: time.Now()}}
	if err := runtime.inbox.save(runtime.inbox.file); err != nil {
		t.Fatal(err)
	}
	original := runtime.inbox.path
	blocked := filepath.Join(runtime.dataRoot, "cannot-replace-directory")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime.inbox.path = blocked
	if err := runtime.ResumeEvents(); err != nil {
		t.Fatal(err)
	}
	waitHealthDetail(t, runtime, "claim persistence failed")
	if messages.calls.Load() != 0 {
		t.Fatal("failed claim dispatched work")
	}
	runtime.inbox.mu.Lock()
	runtime.inbox.path = original
	runtime.inbox.mu.Unlock()
	waitInboxState(t, runtime, event.ID, "completed")
	if health := runtime.Health(); health.State != "ready" {
		t.Fatalf("recovered persistence remained unhealthy: %#v", health)
	}
}

func TestOutcomePersistenceRetriesOnlyWriteNotHandler(t *testing.T) {
	var runtime *Runtime
	original := ""
	messages := &fakeFeishuPort{reply: func(ctx context.Context, id string) (string, error) {
		runtime.inbox.mu.Lock()
		runtime.inbox.path = filepath.Join(runtime.dataRoot, "blocked-outcome")
		runtime.inbox.mu.Unlock()
		return "accepted", nil
	}}
	runtime = testRuntime(t, &fakeCorePort{}, messages)
	original = runtime.inbox.path
	if err := os.Mkdir(filepath.Join(runtime.dataRoot, "blocked-outcome"), 0o700); err != nil {
		t.Fatal(err)
	}
	event := messageEvent("outcome-write", "chat")
	if _, err := runtime.AcceptEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	waitHealthDetail(t, runtime, "outcome persistence failed")
	runtime.inbox.mu.Lock()
	runtime.inbox.path = original
	runtime.inbox.mu.Unlock()
	waitInboxState(t, runtime, event.ID, "completed")
	if messages.calls.Load() != 1 {
		t.Fatal("outcome persistence retry reran handler")
	}
}

func TestInboxRetentionKeepsThirtyDayDedupeAndAllPendingWork(t *testing.T) {
	runtime := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	now := time.Now().UTC()
	runtime.inbox.file.Events = []inboxEvent{
		{Event: feishuprotocol.Event{ID: "old-completed"}, State: "completed", FinishedAt: now.Add(-31 * 24 * time.Hour)},
		{Event: feishuprotocol.Event{ID: "old-unknown"}, State: "outcome_unknown", FinishedAt: now.Add(-31 * 24 * time.Hour)},
		{Event: feishuprotocol.Event{ID: "recent-tombstone"}, State: "completed", FinishedAt: now.Add(-29 * 24 * time.Hour)},
		{Event: feishuprotocol.Event{ID: "pending"}, State: "pending", AcceptedAt: now.Add(-60 * 24 * time.Hour)},
	}
	if err := runtime.inbox.save(runtime.inbox.file); err != nil {
		t.Fatal(err)
	}
	if err := runtime.inbox.prune(now); err != nil {
		t.Fatal(err)
	}
	if len(runtime.inbox.file.Events) != 2 || runtime.inbox.file.Events[0].Event.ID != "recent-tombstone" || runtime.inbox.file.Events[1].Event.ID != "pending" {
		t.Fatalf("incorrect retention: %#v", runtime.inbox.file.Events)
	}
}

func TestResumeActiveStartsOneMaintenanceWorkerAndCleansStores(t *testing.T) {
	runtime := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	link, err := runtime.links.Upsert("expired-thread", "expired", "", "me")
	if err != nil {
		t.Fatal(err)
	}
	file, err := runtime.links.Load()
	if err != nil {
		t.Fatal(err)
	}
	file.Links[0].LinkState = "released"
	file.Links[0].UpdatedAt = time.Now().Add(-8 * 24 * time.Hour)
	if err := runtime.links.Save(file); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if err := runtime.ResumeActive(); err != nil {
			t.Fatal(err)
		}
	}
	runtime.watchMu.Lock()
	_, present := runtime.watchers["maintenance"]
	count := len(runtime.watchers)
	runtime.watchMu.Unlock()
	if !present || count != eventWorkerCount+1 {
		t.Fatalf("maintenance duplicated or missing: %d", count)
	}
	runtime.runMaintenanceCycle(context.Background())
	if _, found, err := runtime.links.FindByID(link.ID); err != nil || found {
		t.Fatalf("task cleanup not run: found=%v err=%v", found, err)
	}
	runtime.Close()
	if runtime.Health().State != "stopped" {
		t.Fatal("closed runtime still ready")
	}
}

func TestAcceptancePersistenceFailureAndCapacityAreVisible(t *testing.T) {
	runtime := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	if err := os.Mkdir(runtime.inbox.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AcceptEvent(context.Background(), messageEvent("health-write-failure", "chat")); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	waitHealthDetail(t, runtime, "acceptance persistence failed")
}
