package feishu

import (
	"context"
	"path/filepath"
	"sync"
)

type executionAdmissionKey struct{}

type executionAdmission struct {
	mu                sync.Mutex
	active, cli, long int
	conflicts         map[string]bool
	changed           chan struct{}
}

var executionAdmissions sync.Map

func admissionFor(root string) *executionAdmission {
	value, _ := executionAdmissions.LoadOrStore(filepath.Clean(root), &executionAdmission{conflicts: map[string]bool{}, changed: make(chan struct{})})
	return value.(*executionAdmission)
}

func admitExecution(ctx context.Context, root string, item WorkItemV4) (context.Context, func(), error) {
	if ctx.Value(executionAdmissionKey{}) != nil {
		return ctx, func() {}, nil
	}
	admission := admissionFor(root)
	for {
		if err := ctx.Err(); err != nil {
			return ctx, nil, err
		}
		admission.mu.Lock()
		if admission.active < defaultWorkConcurrency && !admission.conflicts[item.ConflictKey] && (item.Backend != "lark-cli" || admission.cli < maxLarkCLIConcurrency) && (item.ExecutionClass != "long-remote" || admission.long < maxLongRemoteRunning) {
			admission.active++
			admission.conflicts[item.ConflictKey] = true
			if item.Backend == "lark-cli" {
				admission.cli++
			}
			if item.ExecutionClass == "long-remote" {
				admission.long++
			}
			admission.mu.Unlock()
			var once sync.Once
			release := func() {
				once.Do(func() {
					admission.mu.Lock()
					admission.active--
					delete(admission.conflicts, item.ConflictKey)
					if item.Backend == "lark-cli" {
						admission.cli--
					}
					if item.ExecutionClass == "long-remote" {
						admission.long--
					}
					close(admission.changed)
					admission.changed = make(chan struct{})
					admission.mu.Unlock()
					signalWork(root)
				})
			}
			return context.WithValue(ctx, executionAdmissionKey{}, admission), release, nil
		}
		changed := admission.changed
		admission.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx, nil, ctx.Err()
		case <-changed:
		}
	}
}
