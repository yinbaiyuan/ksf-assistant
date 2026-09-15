package integration

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type nativeEntryMessages struct {
	fakeFeishuPort
	enabled bool
	card    string
}

func (p *nativeEntryMessages) NativeTaskCardsEnabled() bool { return p.enabled }
func (p *nativeEntryMessages) Send(ctx context.Context, target MessageTarget, format, content, key string) (string, error) {
	p.card = content
	return p.fakeFeishuPort.Send(ctx, target, format, content, key)
}

func TestNativeEnrollmentThroughRuntimeEntry(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		messages := &nativeEntryMessages{enabled: enabled}
		r, err := NewRuntime(t.TempDir(), messages, &fakeCorePort{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.CreateTaskLink(context.Background(), CreateTaskLinkRequest{ThreadID: "thread", Title: "Task", TargetAlias: "me"})
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(messages.card, `"ksf_cardkit"`); got != enabled {
			t.Fatalf("real entry lost enrollment capability: enabled=%v, native=%v", enabled, got)
		}
		link, found, err := r.links.FindByTaskKey(taskKey("thread"))
		if err != nil || !found || nativeTaskCard(link) != enabled {
			t.Fatal("enrollment not persisted", err)
		}
	}
}

func TestNativeEnrollmentDoesNotUpgradeBoundLegacyCard(t *testing.T) {
	messages := &nativeEntryMessages{}
	r, err := NewRuntime(t.TempDir(), messages, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	request := CreateTaskLinkRequest{ThreadID: "thread", Title: "Task", TargetAlias: "me"}
	if _, err := r.CreateTaskLink(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	messages.enabled = true
	if _, err := r.CreateTaskLink(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	link, found, err := r.links.FindByTaskKey(taskKey("thread"))
	if err != nil || !found || nativeTaskCard(link) || messages.calls.Load() != 1 {
		t.Fatal("existing card was upgraded or resent", err)
	}
}

type snapshotEntryCore struct {
	fakeCorePort
	wake         chan struct{}
	read         chan struct{}
	subscribed   chan string
	unsubscribed atomic.Bool
}

func (p *snapshotEntryCore) SubscribeThreadSnapshots(id string) (<-chan struct{}, func()) {
	p.subscribed <- id
	return p.wake, func() { p.unsubscribed.Store(true) }
}
func (p *snapshotEntryCore) ObserveThread(context.Context, string, string, string) (map[string]any, string, error) {
	p.read <- struct{}{}
	return map[string]any{}, "1", nil
}

func TestSnapshotWakeThroughRuntimeObserver(t *testing.T) {
	core := &snapshotEntryCore{wake: make(chan struct{}, 1), read: make(chan struct{}, 4), subscribed: make(chan string, 1)}
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, core)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	link, err := r.links.Upsert("thread", "Task", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.runDesktopTaskObserver(ctx, link.TaskKey) }()
	select {
	case id := <-core.subscribed:
		if id != "thread" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("real observer lost snapshot subscription")
	}
	select {
	case <-core.read:
	case <-time.After(time.Second):
		t.Fatal("missing initial read")
	}
	core.wake <- struct{}{}
	select {
	case <-core.read:
	case <-time.After(time.Second):
		t.Fatal("snapshot wake waited for polling")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not stop")
	}
	if !core.unsubscribed.Load() {
		t.Fatal("subscription leaked")
	}
}
