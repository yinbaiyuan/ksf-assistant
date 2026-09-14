package integration

import (
	"context"
	"errors"
	"time"
)

// ReconcileFreshThreadList must only receive a successful, fresh live catalog.
// Missing entries trigger verification, never disconnection by themselves.
// Local release completes before returning; remote card delivery stays asynchronous.
func (runtime *Runtime) ReconcileFreshThreadList(ctx context.Context, liveIDs []string) {
	file, err := runtime.links.Load()
	if err != nil {
		runtime.setHealth("task archive reconciliation failed", err)
		return
	}
	live := make(map[string]bool, len(liveIDs))
	for _, id := range liveIDs {
		live[id] = true
	}
	for _, link := range file.Links {
		if effectiveTaskLinkState(link, time.Now()) == "active" && !live[link.ThreadID] {
			checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			runtime.setHealth("task archive reconciliation failed", runtime.reconcileArchivedLinks(checkCtx))
			return
		}
	}
}

// Absence from the live task list is never archive evidence. Only positive
// IDs from a successful archived catalog read may release a connection.
func (runtime *Runtime) reconcileArchivedLinks(ctx context.Context) error {
	file, err := runtime.links.Load()
	if err != nil {
		return err
	}
	active := false
	for _, link := range file.Links {
		if effectiveTaskLinkState(link, time.Now()) == "active" {
			active = true
			break
		}
	}
	if !active {
		return nil
	}
	ids, err := runtime.core.(interface {
		ArchivedThreadIDs(context.Context) ([]string, error)
	}).ArchivedThreadIDs(ctx)
	if err != nil {
		return err
	}
	archived := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			archived[id] = true
		}
	}
	var result error
	for _, link := range file.Links {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		if !archived[link.ThreadID] || effectiveTaskLinkState(link, time.Now()) != "active" {
			continue
		}
		unlock := runtime.lockActions("task:" + link.TaskKey)
		_, err := runtime.links.UpdateActiveByID(link.ID, func(value *TaskLink) {
			releaseTaskLink(value)
			value.Detail = "Codex 任务已归档，飞书连接已断开。"
		})
		unlock()
		if errors.Is(err, ErrInactiveTaskLink) {
			continue
		}
		result = errors.Join(result, err)
		if err == nil {
			runtime.deliverTaskCard(link.ID)
		}
	}
	return result
}
