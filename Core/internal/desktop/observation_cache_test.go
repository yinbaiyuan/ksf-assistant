package desktop

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestDisconnectedObservationCacheIsInvalid(t *testing.T) {
	c := New("test")
	a, b := net.Pipe()
	defer b.Close()
	c.connection = a
	c.states[taskKey{"local", "thread"}] = map[string]any{"old": true}
	c.connectionEnded(a)
	if _, ok := c.CachedConversationState("thread"); ok {
		t.Fatal("disconnected state remained usable")
	}
}

func TestPassiveDesktopObservationDoesNotDiscoverOrOpenUnknownTask(t *testing.T) {
	c := New("test")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c.connection = a
	c.started = true
	c.clientID = "client"
	value, found, err := c.ObserveKnownConversationState(context.Background(), "thread")
	if err != nil || found || value.State != nil {
		t.Fatalf("unknown task was actively claimed: %#v %v %v", value, found, err)
	}
	b.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, err := bufio.NewReader(b).ReadByte(); err == nil {
		t.Fatal("passive observation sent an owner discovery request")
	}
}

func TestPassiveDesktopObservationLoadsOnlyKnownOwner(t *testing.T) {
	c := New("test")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c.connection = a
	c.started = true
	c.clientID = "client"
	c.owners[taskKey{"local", "thread"}] = "owner"
	result := make(chan error, 1)
	go func() {
		value, found, err := c.ObserveKnownConversationState(context.Background(), "thread")
		if err == nil && (!found || value.OwnerClientID != "owner" || value.State["marker"] != true) {
			err = errors.New("known owner snapshot was not returned")
		}
		result <- err
	}()
	reader := bufio.NewReader(b)
	_ = readTestFrameWithReader(t, reader)
	request := readTestFrameWithReader(t, reader)
	if request["method"] != "thread-follower-load-complete-history" || request["targetClientId"] != "owner" {
		t.Fatalf("passive read targeted the wrong owner: %#v", request)
	}
	c.handle(mustJSON(t, map[string]any{"type": "response", "requestId": request["requestId"], "result": map[string]any{}}))
	c.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "owner", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": "2", "conversationState": map[string]any{"marker": true}}}}))
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestObservationCacheUsesTrustedPushWithoutHistoryReload(t *testing.T) {
	c := New("test")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c.connection = a
	c.started = true
	c.clientID = "client"
	key := taskKey{"local", "thread"}
	c.owners[key] = "owner"
	c.observationCache = map[taskKey]*observationCache{key: {target: UserInputTarget{OwnerClientID: "owner", State: map[string]any{"value": "initial"}, SnapshotRevision: "1"}, nextRead: time.Now().Add(time.Minute)}}
	c.mu.Lock()
	c.cachePushedObservation(key, map[string]any{"value": "wrong"}, "other", "2")
	c.mu.Unlock()
	first, err := c.ObserveConversationState(context.Background(), "thread")
	if err != nil || first.State["value"] != "initial" {
		t.Fatalf("wrong owner accepted: %v", err)
	}
	c.mu.Lock()
	c.cachePushedObservation(key, map[string]any{"value": "new"}, "owner", "3")
	c.mu.Unlock()
	for i := 0; i < 20; i++ {
		v, err := c.ObserveConversationState(context.Background(), "thread")
		if err != nil || v.State["value"] != "new" || v.SnapshotRevision != "3" {
			t.Fatalf("cache miss %v", err)
		}
	}
	if c.ObservationDiagnostics()["fullHistoryReads"] != 0 {
		t.Fatal("cache read loaded history")
	}
}

func TestObservationCacheRejectsOutOfOrderNumericPushes(t *testing.T) {
	c := New("test")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c.connection = a
	c.started = true
	key := taskKey{"local", "thread"}
	c.owners[key] = "owner"
	c.observationCache = map[taskKey]*observationCache{key: {
		target:     UserInputTarget{OwnerClientID: "owner", State: map[string]any{"value": "current"}, SnapshotRevision: "12"},
		generation: c.connectionGeneration,
		nextRead:   time.Now().Add(time.Minute),
	}}
	c.mu.Lock()
	c.cachePushedObservation(key, map[string]any{"value": "older"}, "owner", "11")
	c.mu.Unlock()

	value, err := c.ObserveConversationState(context.Background(), "thread")
	if err != nil {
		t.Fatal(err)
	}
	if value.State["value"] != "current" || value.SnapshotRevision != "12" {
		t.Fatalf("older revision replaced cache: %#v", value)
	}
}

func TestConcurrentObservationInitializesOnceAndCalibratesAfterMinute(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := New("test")
	c.connection = a
	c.started = true
	c.clientID = "client"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := c.ObserveConversationState(ctx, "thread"); results <- err }()
	}
	reader := bufio.NewReader(b)
	respond := func() {
		discovery := readTestFrameWithReader(t, reader)
		if discovery["method"] != "thread-owner-discovery" {
			t.Fatal(discovery["method"])
		}
		c.handle(mustJSON(t, map[string]any{"type": "response", "requestId": discovery["requestId"], "result": map[string]any{"handledByClientId": "owner"}}))
		_ = readTestFrameWithReader(t, reader)
		history := readTestFrameWithReader(t, reader)
		if history["method"] != "thread-follower-load-complete-history" {
			t.Fatal(history["method"])
		}
		c.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "owner", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": "1", "conversationState": map[string]any{"marker": true}}}}))
		c.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "owner", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": "2", "conversationState": map[string]any{"marker": "latest"}}}}))
		c.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	}
	respond()
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	value, err := c.ObserveConversationState(ctx, "thread")
	if err != nil || value.State["marker"] != "latest" {
		t.Fatal("history response replaced newer push", err)
	}
	if c.ObservationDiagnostics()["fullHistoryReads"] != 1 {
		t.Fatal("duplicate initialization")
	}
	c.mu.Lock()
	c.observationCache[taskKey{"local", "thread"}].nextRead = time.Now().Add(-time.Second)
	c.mu.Unlock()
	go func() { _, err := c.ObserveConversationState(ctx, "thread"); results <- err }()
	respond()
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if c.ObservationDiagnostics()["fullHistoryReads"] != 2 {
		t.Fatal("calibration missing")
	}
	value, err = c.ObserveConversationState(ctx, "thread")
	if err != nil || value.State["marker"] != "latest" || value.SnapshotRevision != "2" {
		t.Fatalf("older calibration replaced newer cache: %#v, %v", value, err)
	}
}
