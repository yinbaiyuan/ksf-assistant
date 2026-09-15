package desktop

import (
	"context"
	"errors"
	"fmt"
	"ksfassistant/core/internal/retrypolicy"
	"strings"
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

// SubscribeConversation wakes only after an accepted full snapshot enters the
// cache. The bounded notification is an invalidation, never a history queue.
func (c *ActivityClient) SubscribeConversation(id string) (<-chan struct{}, func()) {
	key := taskKey{"local", id}
	wake := make(chan struct{}, 1)
	c.mu.Lock()
	if c.observationSubscribers == nil {
		c.observationSubscribers = map[taskKey]map[chan struct{}]bool{}
	}
	if c.observationSubscribers[key] == nil {
		c.observationSubscribers[key] = map[chan struct{}]bool{}
	}
	c.observationSubscribers[key][wake] = true
	c.mu.Unlock()
	return wake, func() {
		c.mu.Lock()
		delete(c.observationSubscribers[key], wake)
		if len(c.observationSubscribers[key]) == 0 {
			delete(c.observationSubscribers, key)
		}
		c.mu.Unlock()
	}
}

// Trusted pushes keep this deadline moving; silence triggers a bounded refresh
// even when the previously observed turn was terminal (a new turn may start).
const observationMaxSilence = 5 * time.Second

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
			if e.target.OwnerClientID == target.OwnerClientID && e.target.State != nil {
				if order, comparable := compareNumericSnapshotRevisions(target.SnapshotRevision, e.target.SnapshotRevision); comparable {
					if order <= 0 {
						target = e.target
					}
				} else if e.pushes > pushes {
					target = e.target
				}
			}
			target.ObservationEpoch = generation
			e.target = target
			e.generation = generation
			e.failures = 0
			e.nextRead = time.Now().Add(observationMaxSilence)
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

// ObserveKnownConversationState is a passive ownership probe. It only reads a
// task already claimed by Desktop and never discovers, opens, or starts it.
func (c *ActivityClient) ObserveKnownConversationState(ctx context.Context, id string) (UserInputTarget, bool, error) {
	key := taskKey{"local", id}
	c.mu.Lock()
	owner := c.owners[key]
	generation := c.connectionGeneration
	connected := c.connection != nil
	c.mu.Unlock()
	if !connected || owner == "" {
		return UserInputTarget{}, false, nil
	}
	state, source, revision, err := c.loadConversationStateFromOwnerMatchingRevision(ctx, id, owner, owner, nil)
	if err != nil {
		return UserInputTarget{}, true, err
	}
	c.mu.Lock()
	stillOwned := c.connection != nil && c.connectionGeneration == generation && c.owners[key] == owner
	c.mu.Unlock()
	if !stillOwned {
		return UserInputTarget{}, false, nil
	}
	return UserInputTarget{ObservationEpoch: generation, ThreadID: id, OwnerClientID: owner, SnapshotSourceClientID: source, SnapshotRevision: revision, State: state}, true, nil
}
func (c *ActivityClient) cachePushedObservation(key taskKey, state map[string]any, source, revision string) bool {
	// Called under c.mu. A source with no established owner is not trusted.
	if source == "" || c.owners[key] != source {
		return false
	}
	if c.observationCache == nil {
		c.observationCache = map[taskKey]*observationCache{}
	}
	if e := c.observationCache[key]; e == nil || e.generation != c.connectionGeneration || (e.target.OwnerClientID != "" && e.target.OwnerClientID != source) {
		c.observationCache[key] = &observationCache{generation: c.connectionGeneration}
	}
	if e := c.observationCache[key]; e != nil && e.generation == c.connectionGeneration && (e.target.OwnerClientID == source || e.target.OwnerClientID == "") {
		if order, comparable := compareNumericSnapshotRevisions(revision, e.target.SnapshotRevision); comparable && order <= 0 {
			return false
		}
		e.pushes++
		e.target.OwnerClientID = source
		e.target.ObservationEpoch = c.connectionGeneration
		e.target.ThreadID = key.threadID
		e.target.State = state
		e.target.SnapshotRevision = revision
		e.target.SnapshotSourceClientID = source
		e.nextRead = time.Now().Add(observationMaxSilence)
	}
	for wake := range c.observationSubscribers[key] {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return true
}

func compareNumericSnapshotRevisions(candidate, current string) (int, bool) {
	candidate = strings.TrimSpace(candidate)
	current = strings.TrimSpace(current)
	if candidate == "" || current == "" {
		return 0, false
	}
	for _, value := range []string{candidate, current} {
		for _, character := range value {
			if character < '0' || character > '9' {
				return 0, false
			}
		}
	}
	candidate = strings.TrimLeft(candidate, "0")
	current = strings.TrimLeft(current, "0")
	if candidate == "" {
		candidate = "0"
	}
	if current == "" {
		current = "0"
	}
	if len(candidate) < len(current) {
		return -1, true
	}
	if len(candidate) > len(current) {
		return 1, true
	}
	return strings.Compare(candidate, current), true
}
func ObservationVersion(t UserInputTarget) string {
	return fmt.Sprintf("%d:%s:%s", t.ObservationEpoch, t.OwnerClientID, t.SnapshotRevision)
}
