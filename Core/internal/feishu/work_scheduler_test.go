package feishu

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestWorkSchedulerEnforcesCapacityAndExecutionLimits(t *testing.T) {
	root := t.TempDir()
	repo := newWorkRepository(root, "actionbox")
	items := []struct {
		id, conflict, backend, class string
	}{
		{"ACT-001", "target-a", "lark-cli", "long-remote"},
		{"ACT-002", "target-b", "lark-cli", "long-remote"},
		{"ACT-003", "target-c", "lark-cli", "standard"},
		{"ACT-004", "target-d", "lark-cli", "standard"},
		{"ACT-005", "target-e", "service", "standard"},
		{"ACT-006", "target-f", "service", "standard"},
	}
	for _, item := range items {
		if err := repo.enqueue(item.id, map[string]any{"id": item.id}, item.conflict, item.backend, item.class, "never", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan WorkItemV3, len(items))
	release := make(chan struct{})
	var mu sync.Mutex
	active, activeCLI, activeLong := 0, 0, 0
	maxActive, maxCLI, maxLong := 0, 0, 0
	scheduler := NewWorkScheduler(root)
	scheduler.queues = []*scheduledWorkQueue{{kind: "actionbox", repo: repo, migrate: func() error { return nil }, handle: func(_ context.Context, item WorkItemV3) error {
		mu.Lock()
		active++
		if item.Backend == "lark-cli" {
			activeCLI++
		}
		if item.ExecutionClass == "long-remote" {
			activeLong++
		}
		if active > maxActive {
			maxActive = active
		}
		if activeCLI > maxCLI {
			maxCLI = activeCLI
		}
		if activeLong > maxLong {
			maxLong = activeLong
		}
		mu.Unlock()
		started <- item
		<-release
		mu.Lock()
		active--
		if item.Backend == "lark-cli" {
			activeCLI--
		}
		if item.ExecutionClass == "long-remote" {
			activeLong--
		}
		mu.Unlock()
		return repo.finish(item, map[string]any{"id": item.ID, "status": "completed"}, "")
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	for index := 0; index < 4; index++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d workers started", index)
		}
	}
	mu.Lock()
	if maxActive != 4 || maxCLI > 2 || maxLong > 1 {
		t.Fatalf("limits active=%d cli=%d long=%d", maxActive, maxCLI, maxLong)
	}
	mu.Unlock()
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for _, item := range items {
		for {
			var result map[string]any
			found, err := repo.findResult(item.id, &result)
			if err != nil {
				t.Fatal(err)
			}
			if found {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("result %s was not completed", item.id)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestWorkSchedulerSerializesConflictKeyAndPreservesFIFO(t *testing.T) {
	root := t.TempDir()
	repo := newWorkRepository(root, "actionbox")
	for index, conflict := range []string{"same", "same", "other"} {
		id := fmt.Sprintf("ACT-%03d", index+1)
		if err := repo.enqueue(id, map[string]any{"id": id}, conflict, "service", "standard", "never", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan string, 3)
	releaseFirst := make(chan struct{})
	releaseOther := make(chan struct{})
	scheduler := NewWorkScheduler(root)
	scheduler.queues = []*scheduledWorkQueue{{kind: "actionbox", repo: repo, migrate: func() error { return nil }, handle: func(_ context.Context, item WorkItemV3) error {
		started <- item.ID
		switch item.ID {
		case "ACT-001":
			<-releaseFirst
		case "ACT-003":
			<-releaseOther
		}
		return repo.finish(item, map[string]any{"id": item.ID, "status": "completed"}, "")
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	first := <-started
	second := <-started
	if first != "ACT-001" || second != "ACT-003" {
		t.Fatalf("unexpected first dispatches: %s, %s", first, second)
	}
	select {
	case id := <-started:
		t.Fatalf("conflicting item started early: %s", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case id := <-started:
		if id != "ACT-002" {
			t.Fatalf("FIFO violated: %s", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second conflicting item did not start")
	}
	close(releaseOther)
	deadline := time.Now().Add(2 * time.Second)
	for _, id := range []string{"ACT-001", "ACT-002", "ACT-003"} {
		for {
			var result map[string]any
			found, err := repo.findResult(id, &result)
			if err != nil {
				t.Fatal(err)
			}
			if found {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("scheduled work %s did not finish", id)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestWorkRepositoryInterruptedSideEffectBecomesOutcomeUnknown(t *testing.T) {
	root := t.TempDir()
	repo := newWorkRepository(root, "outbox")
	if err := repo.enqueue("OUT-INTERRUPTED", map[string]any{"id": "OUT-INTERRUPTED", "text": "private"}, "target", "go-sdk", "standard", "never", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.claim(); err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if count, err := repo.recoverRunning(10); err != nil || count != 1 {
		t.Fatalf("recover count=%d err=%v", count, err)
	}
	var result OutboxResult
	found, err := repo.findResult("OUT-INTERRUPTED", &result)
	if err != nil || !found || result.Status != string(OperationOutcomeUnknown) || result.Error != "worker_interrupted" {
		t.Fatalf("result=%#v found=%v err=%v", result, found, err)
	}
}
