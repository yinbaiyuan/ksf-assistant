package desktop

import (
	"net"
	"testing"
	"time"
)

func TestPatchBurstDoesNotBlockReaderOrRequestEverySnapshot(t *testing.T) {
	c := New("test")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c.connection = a
	c.owners[taskKey{"local", "thread"}] = "owner"
	message := mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "owner", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread", "change": map[string]any{"type": "patches"}}})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			c.handle(message)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("patch handling blocked the IPC reader on snapshot requests")
	}
	b.SetReadDeadline(time.Now().Add(time.Second))
	request := readTestFrame(t, b)
	if request["method"] != "thread-stream-following-changed" {
		t.Fatal("no snapshot recovery")
	}
	b.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var next [1]byte
	if _, err := b.Read(next[:]); err == nil {
		t.Fatal("burst was not coalesced")
	}
}

func TestActivityRejectsStaleAndForeignSnapshots(t *testing.T) {
	c := New("test")
	key := taskKey{"local", "thread"}
	c.owners[key] = "owner"
	push := func(source, revision, status string) {
		c.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": source, "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread", "change": map[string]any{"type": "snapshot", "revision": revision, "conversationState": map[string]any{"threadRuntimeStatus": map[string]any{"type": status}}}}}))
	}
	push("owner", "2", "idle")
	push("owner", "1", "active")
	if c.Snapshot(time.Now()).RunningCount != 0 {
		t.Fatal("stale snapshot revived completed task")
	}
	push("other", "3", "active")
	if c.Snapshot(time.Now()).RunningCount != 0 {
		t.Fatal("foreign snapshot revived completed task")
	}
	push("owner", "4", "active")
	if c.Snapshot(time.Now()).RunningCount != 1 {
		t.Fatal("new turn was not observed")
	}
}

func TestPendingSnapshotRefreshDoesNotFollowReplacedOwner(t *testing.T) {
	c := New("test")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c.connection = a
	key := taskKey{"local", "thread"}
	c.owners[key] = "owner"
	c.scheduleSnapshotRefresh(key, "owner")
	c.mu.Lock()
	c.owners[key] = "replacement"
	c.mu.Unlock()
	b.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	var data [1]byte
	if _, err := b.Read(data[:]); err == nil {
		t.Fatal("stale owner received a follow request")
	}
}
