package service

import (
	"context"
	"time"
)

// startActivityDiscovery keeps the lightweight task index fresh independently
// from the dashboard's slower account/project refresh. This lets the status bar
// discover newly running and unassigned tasks while the popover is closed.
func (service *Service) startActivityDiscovery() {
	service.mu.Lock()
	if service.codex == nil || service.activityDiscoveryCancel != nil {
		service.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	service.activityDiscoveryCancel = cancel
	service.activityDiscoveryDone = done
	service.mu.Unlock()

	go func() {
		defer close(done)
		service.discoverTaskActivity(ctx)
		ticker := time.NewTicker(activityDiscoveryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				service.discoverTaskActivity(ctx)
			}
		}
	}()
}

func (service *Service) discoverTaskActivity(parent context.Context) {
	service.mu.Lock()
	client := service.codex
	service.mu.Unlock()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, activityDiscoveryInterval)
	defer cancel()
	threads, err := client.FetchActivityThreads(ctx)
	if err != nil {
		return
	}
	service.mu.Lock()
	service.lastActivityThreads = threads
	service.mu.Unlock()
	service.desktop.ReconcileCandidates(localTaskCandidates(threads))
}

func (service *Service) stopActivityDiscovery() {
	service.mu.Lock()
	cancel := service.activityDiscoveryCancel
	done := service.activityDiscoveryDone
	service.activityDiscoveryCancel = nil
	service.activityDiscoveryDone = nil
	service.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}
