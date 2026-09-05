package feishu

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWorkRepositoryMigratesLegacyQueueOnceWithoutReplayingProcessedSideEffects(t *testing.T) {
	root := t.TempDir()
	box := NewActionbox(root)
	created := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	requests := []ActionRequest{
		{ID: "ACT-LEGACY-001", CreatedAt: created},
		{ID: "ACT-LEGACY-002", CreatedAt: created.Add(time.Second)},
		{ID: "ACT-LEGACY-003", CreatedAt: created.Add(2 * time.Second)},
	}
	for _, request := range requests {
		if err := appendPrivateJSONL(box.queuePath(), request); err != nil {
			t.Fatal(err)
		}
	}
	if err := appendPrivateJSONL(box.resultPath(), ActionResult{ID: requests[0].ID, Status: "succeeded", CompletedAt: created.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(box.statePath(), queueState{SchemaVersion: 2, ProcessedLineCount: 2}); err != nil {
		t.Fatal(err)
	}

	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		t.Fatal(err)
	}
	index, err := box.repository.readIndex()
	if err != nil || index.Pending != 1 || index.Running != 0 || index.Terminal != 2 || index.Processed != 2 {
		t.Fatalf("index=%#v err=%v", index, err)
	}
	var completed ActionResult
	if found, err := box.repository.findResult(requests[0].ID, &completed); err != nil || !found || completed.Status != "succeeded" {
		t.Fatalf("completed=%#v found=%v err=%v", completed, found, err)
	}
	var missingResult ActionResult
	if found, err := box.repository.findResult(requests[1].ID, &missingResult); err != nil || !found || missingResult.Status != string(OperationOutcomeUnknown) || missingResult.Error != "legacy_result_missing" {
		t.Fatalf("missing result=%#v found=%v err=%v", missingResult, found, err)
	}
	var pending WorkItemV3
	if missing, err := readPrivateJSON(box.repository.path("pending", requests[2].ID), &pending); err != nil || missing || len(pending.Request) == 0 {
		t.Fatalf("pending=%#v missing=%v err=%v", pending, missing, err)
	}
	legacyRoot := filepath.Join(root, "private-cache", "workbox-v3", "legacy-v2", "actionbox")
	for _, path := range []string{box.queuePath(), box.resultPath(), box.statePath(), box.queuePath() + ".lock", box.resultPath() + ".lock"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy source was not archived: %s (%v)", path, err)
		}
		if _, err := os.Lstat(filepath.Join(legacyRoot, filepath.Base(path))); err != nil {
			t.Fatalf("legacy archive missing %s: %v", path, err)
		}
	}
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		t.Fatalf("completed migration was not idempotent: %v", err)
	}
}

func TestWorkRepositoryRejectsLegacySymlinkWithoutMovingSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	box := NewOutbox(root)
	target := filepath.Join(root, "outside.jsonl")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(box.queuePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, box.queuePath()); err != nil {
		t.Fatal(err)
	}
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err == nil {
		t.Fatal("legacy symlink was accepted")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "{}\n" {
		t.Fatalf("symlink target changed: %q err=%v", data, err)
	}
	if _, err := os.Lstat(box.repository.migrationMarker()); !os.IsNotExist(err) {
		t.Fatalf("failed migration wrote completion marker: %v", err)
	}
}

func TestWorkRepositoryPendingHeadsUseCreatedAtForFIFO(t *testing.T) {
	root := t.TempDir()
	repo := newWorkRepository(root, "docbox")
	now := time.Now().UTC()
	if err := repo.enqueue("DOC-Z", map[string]any{"id": "DOC-Z"}, "same", "lark-cli", "standard", "never", now); err != nil {
		t.Fatal(err)
	}
	if err := repo.enqueue("DOC-A", map[string]any{"id": "DOC-A"}, "same", "lark-cli", "standard", "never", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	heads, err := repo.pendingHeads()
	if err != nil || len(heads) != 1 || heads[0].ID != "DOC-Z" {
		t.Fatalf("heads=%#v err=%v", heads, err)
	}
}

func TestWorkSchedulerHandlerFailureTerminatesClaimAsOutcomeUnknown(t *testing.T) {
	root := t.TempDir()
	repo := newWorkRepository(root, "outbox")
	if err := repo.enqueue("OUT-FAIL", map[string]any{"id": "OUT-FAIL"}, "target", "go-sdk", "standard", "never", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scheduler := NewWorkScheduler(root)
	scheduler.queues = []*scheduledWorkQueue{{kind: "outbox", repo: repo, migrate: func() error { return nil }, handle: func(context.Context, WorkItemV3) error {
		return os.ErrInvalid
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var result OutboxResult
		found, err := repo.findResult("OUT-FAIL", &result)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			if result.Status != string(OperationOutcomeUnknown) || result.Error != "work_handler_failed" {
				t.Fatalf("result=%#v", result)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("failed handler left a running item")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
