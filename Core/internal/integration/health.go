package integration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type RuntimeHealth struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

func (runtime *Runtime) setHealth(component string, err error) {
	runtime.healthMu.Lock()
	defer runtime.healthMu.Unlock()
	if runtime.healthIssues == nil {
		runtime.healthIssues = map[string]bool{}
	}
	if err == nil {
		delete(runtime.healthIssues, component)
	} else {
		runtime.healthIssues[component] = true
	}
}

func (runtime *Runtime) Health() RuntimeHealth {
	if runtime.watchCtx.Err() != nil {
		return RuntimeHealth{State: "stopped", Detail: "Core integration is stopped"}
	}
	runtime.healthMu.Lock()
	issues := make([]string, 0, len(runtime.healthIssues))
	for component := range runtime.healthIssues {
		issues = append(issues, component)
	}
	runtime.healthMu.Unlock()
	runtime.inbox.mu.Lock()
	unknown := len(runtime.inbox.unknownReceipts)
	for _, event := range runtime.inbox.file.Events {
		if eventNeedsReview(event) && !runtime.inbox.unknownReceipts[event.Event.ID] {
			unknown++
		}
	}
	runtime.inbox.mu.Unlock()
	if unknown > 0 {
		issues = append(issues, fmt.Sprintf("%d event outcomes require reconciliation", unknown))
	}
	if len(issues) == 0 {
		return RuntimeHealth{State: "ready"}
	}
	sort.Strings(issues)
	return RuntimeHealth{State: "degraded", Detail: strings.Join(issues, "; ")}
}

func (runtime *Runtime) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycleCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			runtime.runMaintenanceCycle(cycleCtx)
			cancel()
		}
	}
}

func (runtime *Runtime) runMaintenanceCycle(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	recoveryErr := runtime.resumeActiveLinks(ctx)
	runtime.setHealth("task observation recovery failed", recoveryErr)
	runtime.ReconcileTaskLinkCards(ctx)
	if ctx.Err() != nil {
		return
	}
	_, err := runtime.links.CleanupAt(time.Now().UTC())
	runtime.setHealth("task link cleanup persistence failed", err)
	err = runtime.inbox.prune(time.Now().UTC())
	runtime.setHealth("event inbox retention persistence failed", err)
	if err == nil {
		runtime.inbox.mu.Lock()
		active := 0
		for _, record := range runtime.inbox.file.Events {
			if !terminalInboxEvent(record) {
				active++
			}
		}
		full := active >= eventInboxMaxRecords
		runtime.inbox.mu.Unlock()
		if !full {
			runtime.setHealth("event inbox capacity exceeded", nil)
		}
	}
}

func (runtime *Runtime) reconcileTaskLinkCards(ctx context.Context) error {
	file, err := runtime.links.Load()
	if err != nil {
		return err
	}
	var syncErrors error
	for _, link := range file.Links {
		if ctx.Err() != nil {
			return errors.Join(syncErrors, ctx.Err())
		}
		if TaskLinkCardSyncPending(link) {
			syncErrors = errors.Join(syncErrors, SyncTaskLinkCard(ctx, runtime.links, runtime.messages, link, ""))
		}
		if effectiveTaskLinkState(link, time.Now()) != "active" || !runtime.desktopOwned(link) {
			continue
		}
		if link.TurnOwner == "bridge" && (link.TurnState == "running" || link.TurnState == "waiting_input") {
			continue
		}
		runtime.observeDesktopTask(link.TaskKey)
	}
	return syncErrors
}
