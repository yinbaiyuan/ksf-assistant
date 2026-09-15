package desktop

import "testing"

func TestObservationSubscriptionOnlyAcceptedSnapshots(t *testing.T) {
	c := New("")
	key := taskKey{"local", "thread"}
	c.owners[key] = "owner"
	wake, cancel := c.SubscribeConversation("thread")
	defer cancel()
	c.mu.Lock()
	c.cachePushedObservation(key, map[string]any{}, "other", "1")
	c.mu.Unlock()
	select {
	case <-wake:
		t.Fatal("untrusted push woke subscriber")
	default:
	}
	c.mu.Lock()
	c.cachePushedObservation(key, map[string]any{}, "owner", "2")
	c.mu.Unlock()
	select {
	case <-wake:
	default:
		t.Fatal("accepted snapshot did not wake")
	}
	c.mu.Lock()
	c.cachePushedObservation(key, map[string]any{}, "owner", "1")
	c.mu.Unlock()
	select {
	case <-wake:
		t.Fatal("stale snapshot woke subscriber")
	default:
	}
}
