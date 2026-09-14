package integration

import (
	"context"
	"time"
)

// The durable link is a latest-value mailbox, not a queue of historical cards.
// One worker per link keeps network latency out of task observation. The shared
// sync lock also serializes explicit actions and maintenance against this worker.
func (runtime *Runtime) deliverTaskCard(id string) {
	runtime.watchMu.Lock()
	if runtime.closed {
		runtime.watchMu.Unlock()
		return
	}
	if runtime.cardWakes == nil {
		runtime.cardWakes = map[string]chan struct{}{}
	}
	wake := runtime.cardWakes[id]
	if wake == nil {
		wake = make(chan struct{}, 1)
		runtime.cardWakes[id] = wake
	}
	select {
	case wake <- struct{}{}:
	default:
	}
	runtime.watchMu.Unlock()
	runtime.launchWatcher("card:"+id, func(ctx context.Context) {
		defer func() {
			runtime.watchMu.Lock()
			delete(runtime.cardWakes, id)
			runtime.watchMu.Unlock()
		}()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			link, found, err := runtime.links.FindByID(id)
			if err != nil || !found || ctx.Err() != nil {
				return
			}
			if TaskLinkCardSyncPending(link) {
				sendCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
				err = SyncTaskLinkCard(sendCtx, runtime.links, runtime.messages, link, "")
				cancel()
				// Per-card failures are reported from the durable sync states by
				// Health; one successful card must not clear another card's error.
			}
			if effectiveTaskLinkState(link, time.Now()) != "active" && !TaskLinkCardSyncPending(link) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-wake:
			}
		}
	})
}
