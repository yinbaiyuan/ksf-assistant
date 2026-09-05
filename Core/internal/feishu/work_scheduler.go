package feishu

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	defaultWorkConcurrency = 4
	maxLarkCLIConcurrency  = 2
	maxLongRemoteRunning   = 1
)

type scheduledWorkQueue struct {
	kind    string
	repo    workRepository
	migrate func() error
	handle  func(context.Context, WorkItemV3) error
}

type scheduledCompletion struct {
	queue *scheduledWorkQueue
	item  WorkItemV3
	err   error
}

// WorkScheduler is the single production dispatcher for all side-effecting
// Feishu work. Its coordinator owns capacity and conflict reservations, so
// workers never block while holding a slot waiting for another worker.
type WorkScheduler struct {
	dataRoot string
	queues   []*scheduledWorkQueue
	complete chan scheduledCompletion
	active   int
	cli      int
	long     int
	conflict map[string]bool
	lastKey  map[string]string
	nextKind int
	runOnce  sync.Once
}

func NewWorkScheduler(dataRoot string) *WorkScheduler {
	return &WorkScheduler{
		dataRoot: dataRoot,
		complete: make(chan scheduledCompletion, defaultWorkConcurrency),
		conflict: map[string]bool{},
		lastKey:  map[string]string{},
	}
}

func (scheduler *WorkScheduler) RegisterCapabilityService(service *CapabilityService) {
	if service == nil || service.actionbox == nil {
		return
	}
	box := service.actionbox
	scheduler.queues = append(scheduler.queues, &scheduledWorkQueue{
		kind: "actionbox", repo: box.repository,
		migrate: func() error { return box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()) },
		handle: func(ctx context.Context, item WorkItemV3) error {
			return box.processClaimed(ctx, item, service.executor)
		},
	})
}

func (scheduler *WorkScheduler) RegisterDocbox(box *Docbox, executor CapabilityExecutor, dryRun bool) {
	if box == nil {
		return
	}
	scheduler.queues = append(scheduler.queues, &scheduledWorkQueue{
		kind: "docbox", repo: box.repository,
		migrate: func() error { return box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()) },
		handle: func(ctx context.Context, item WorkItemV3) error {
			return box.processClaimed(ctx, item, executor, dryRun)
		},
	})
}

func (scheduler *WorkScheduler) RegisterOutbox(box *Outbox, sender MessageSender, dryRun bool) {
	if box == nil {
		return
	}
	scheduler.queues = append(scheduler.queues, &scheduledWorkQueue{
		kind: "outbox", repo: box.repository,
		migrate: func() error { return box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()) },
		handle:  func(ctx context.Context, item WorkItemV3) error { return box.processClaimed(ctx, item, sender, dryRun) },
	})
}

func (scheduler *WorkScheduler) Wake() { signalWork(scheduler.dataRoot) }

func (scheduler *WorkScheduler) Run(ctx context.Context) error {
	if len(scheduler.queues) == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	var startupErr error
	scheduler.runOnce.Do(func() {
		for _, queue := range scheduler.queues {
			if err := queue.migrate(); err != nil {
				startupErr = errors.Join(startupErr, err)
				continue
			}
			if _, err := queue.repo.recoverRunning(1000); err != nil {
				startupErr = errors.Join(startupErr, err)
			}
			if err := queue.repo.rebuildIndex(); err != nil {
				startupErr = errors.Join(startupErr, err)
			}
		}
	})
	if startupErr != nil {
		return startupErr
	}
	reconcile := time.NewTicker(time.Minute)
	defer reconcile.Stop()
	for {
		if err := scheduler.dispatchAvailable(ctx); err != nil {
			_ = NewAuditLog(scheduler.dataRoot).Record("work_scheduler_degraded", map[string]any{"error": safeCommandError(err.Error())})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case completion := <-scheduler.complete:
			scheduler.release(completion.item)
			if completion.err != nil {
				_ = NewAuditLog(scheduler.dataRoot).Record("work_item_processing_failed", map[string]any{"kind": completion.queue.kind, "id": AuditFingerprint(completion.item.ID), "error": safeCommandError(completion.err.Error())})
			}
		case <-workSignal(scheduler.dataRoot):
		case <-reconcile.C:
		}
	}
}

func (scheduler *WorkScheduler) dispatchAvailable(ctx context.Context) error {
	for scheduler.active < defaultWorkConcurrency {
		queue, candidate, found, err := scheduler.nextEligible()
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		item, claimed, err := queue.repo.claimID(candidate.ID)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		scheduler.reserve(item)
		scheduler.lastKey[queue.kind] = item.ConflictKey
		go scheduler.execute(ctx, queue, item)
	}
	return nil
}

func (scheduler *WorkScheduler) nextEligible() (*scheduledWorkQueue, WorkItemV3, bool, error) {
	for offset := 0; offset < len(scheduler.queues); offset++ {
		index := (scheduler.nextKind + offset) % len(scheduler.queues)
		queue := scheduler.queues[index]
		heads, err := queue.repo.pendingHeads()
		if err != nil {
			return nil, WorkItemV3{}, false, err
		}
		candidate, ok := scheduler.rotateEligible(heads, scheduler.lastKey[queue.kind])
		if !ok {
			continue
		}
		scheduler.nextKind = (index + 1) % len(scheduler.queues)
		return queue, candidate, true, nil
	}
	return nil, WorkItemV3{}, false, nil
}

func (scheduler *WorkScheduler) rotateEligible(items []WorkItemV3, after string) (WorkItemV3, bool) {
	if len(items) == 0 {
		return WorkItemV3{}, false
	}
	start := 0
	for index, item := range items {
		if item.ConflictKey == after {
			start = (index + 1) % len(items)
			break
		}
	}
	for offset := 0; offset < len(items); offset++ {
		item := items[(start+offset)%len(items)]
		if scheduler.eligible(item) {
			return item, true
		}
	}
	return WorkItemV3{}, false
}

func (scheduler *WorkScheduler) eligible(item WorkItemV3) bool {
	if scheduler.conflict[item.ConflictKey] {
		return false
	}
	if item.Backend == "lark-cli" && scheduler.cli >= maxLarkCLIConcurrency {
		return false
	}
	if item.ExecutionClass == "long-remote" && scheduler.long >= maxLongRemoteRunning {
		return false
	}
	return true
}

func (scheduler *WorkScheduler) reserve(item WorkItemV3) {
	scheduler.active++
	scheduler.conflict[item.ConflictKey] = true
	if item.Backend == "lark-cli" {
		scheduler.cli++
	}
	if item.ExecutionClass == "long-remote" {
		scheduler.long++
	}
}

func (scheduler *WorkScheduler) release(item WorkItemV3) {
	if scheduler.active > 0 {
		scheduler.active--
	}
	delete(scheduler.conflict, item.ConflictKey)
	if item.Backend == "lark-cli" && scheduler.cli > 0 {
		scheduler.cli--
	}
	if item.ExecutionClass == "long-remote" && scheduler.long > 0 {
		scheduler.long--
	}
}

func (scheduler *WorkScheduler) execute(ctx context.Context, queue *scheduledWorkQueue, item WorkItemV3) {
	var err error
	defer func() {
		if recover() != nil {
			err = errors.New("work_handler_panicked")
			_ = queue.repo.markOutcomeUnknown(item, "work_handler_panicked")
		} else if err != nil {
			// A claimed side effect must never be left as silently retryable work.
			// If the handler failed before persisting a terminal result, preserve
			// the uncertainty and let explicit reconciliation close it.
			_ = queue.repo.markOutcomeUnknown(item, "work_handler_failed")
		}
		scheduler.complete <- scheduledCompletion{queue: queue, item: item, err: err}
	}()
	err = queue.handle(ctx, item)
}
