package integration

import (
	"context"
	"time"
)

// The durable link is a latest-value mailbox, not a queue of historical cards.
// One worker per link keeps network latency out of task observation. The shared
// sync lock also serializes explicit actions and maintenance against this worker.
func (runtime *Runtime) deliverTaskCard(id string) {
	runtime.launchWatcher("card:"+id, func(ctx context.Context) {
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
			}
		}
	})
}
