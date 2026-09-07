package desktop

import (
	"context"
	"errors"
	"fmt"
	"ksfassistant/core/internal/retrypolicy"
	"sync/atomic"
	"time"
)

type observationCache struct {
	target     UserInputTarget
	generation uint64
	nextRead   time.Time
	failures   int
	pushes     uint64
	loading    chan struct{}
}
type ObservationCounters struct{ CacheHits, FullReads atomic.Uint64 }

func (c *ActivityClient) ObservationDiagnostics() map[string]uint64 {
	return map[string]uint64{"cacheHits": c.observationCounters.CacheHits.Load(), "fullHistoryReads": c.observationCounters.FullReads.Load()}
}

// ObserveConversationState is display-only. Interactive submissions must use the
// authoritative user-input API, never this bounded-staleness cache.
func (c *ActivityClient) ObserveConversationState(ctx context.Context, id string) (UserInputTarget, error) {
	key := taskKey{"local", id}
	for {
		c.mu.Lock()
		if c.observationCache == nil {
			c.observationCache = map[taskKey]*observationCache{}
		}
		e := c.observationCache[key]
		if e == nil {
			e = &observationCache{}
			c.observationCache[key] = e
		}
		owner := c.owners[key]
		if e.loading == nil && (e.generation != c.connectionGeneration || (e.target.OwnerClientID != "" && e.target.OwnerClientID != owner)) {
			e.target = UserInputTarget{}
			e.nextRead = time.Time{}
			e.generation = c.connectionGeneration
			e.failures = 0
		}
		valid := c.connection != nil && owner != "" && e.generation == c.connectionGeneration && e.target.OwnerClientID == owner && e.target.State != nil
		if e.loading != nil {
			done := e.loading
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return UserInputTarget{}, ctx.Err()
			case <-done:
				continue
			}
		}
		if time.Now().Before(e.nextRead) {
			if valid {
				result := e.target
				c.observationCounters.CacheHits.Add(1)
				c.mu.Unlock()
				return result, nil
			}
			c.mu.Unlock()
			return UserInputTarget{}, errors.New("observation_refresh_backoff")
		}
		done := make(chan struct{})
		e.loading = done
		generation := c.connectionGeneration
		pushes := e.pushes
		c.mu.Unlock()
		c.observationCounters.FullReads.Add(1)
		target, err := c.ReadConversationStateForUserInput(ctx, id)
		c.mu.Lock()
		if generation != c.connectionGeneration || c.connection == nil || c.owners[key] != target.OwnerClientID {
			if err == nil {
				err = errors.New("observation_owner_changed")
			}
		}
		if err == nil {
			if e.pushes > pushes && e.target.OwnerClientID == target.OwnerClientID {
				target = e.target
			}
			target.ObservationEpoch = generation
			e.target = target
			e.generation = generation
			e.failures = 0
			e.nextRead = time.Now().Add(time.Minute)
		} else {
			e.failures++
			e.nextRead = time.Now().Add(retrypolicy.Delay(e.failures, id))
		}
		e.loading = nil
		close(done)
		c.mu.Unlock()
		return target, err
	}
}
func (c *ActivityClient) cachePushedObservation(key taskKey, state map[string]any, source, revision string) {
	// Called under c.mu. A source with no established owner is not trusted.
	if source == "" || c.owners[key] != source {
		return
	}
	if e := c.observationCache[key]; e != nil && e.generation == c.connectionGeneration && (e.target.OwnerClientID == source || e.target.OwnerClientID == "") {
		e.pushes++
		e.target.OwnerClientID = source
		e.target.ThreadID = key.threadID
		e.target.State = state
		e.target.SnapshotRevision = revision
		e.target.SnapshotSourceClientID = source
	}
}
func ObservationVersion(t UserInputTarget) string {
	return fmt.Sprintf("%d:%s:%s", t.ObservationEpoch, t.OwnerClientID, t.SnapshotRevision)
}
