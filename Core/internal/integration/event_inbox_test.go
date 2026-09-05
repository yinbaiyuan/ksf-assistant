package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privatestore"
)

func messageEvent(id, chat string) feishuprotocol.Event {
	data, _ := json.Marshal(InboundMessage{EventID: id, MessageID: id, ChatID: chat, SenderOpenID: "user-1", MessageType: "text"})
	return feishuprotocol.Event{ID: id, Kind: "message", Payload: data}
}

func waitInboxState(t *testing.T, runtime *Runtime, id, wanted string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.inbox.mu.Lock()
		for _, record := range runtime.inbox.file.Events {
			if record.Event.ID == id && record.State == wanted {
				runtime.inbox.mu.Unlock()
				return
			}
		}
		runtime.inbox.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event %s did not become %s", id, wanted)
}

func TestEventACKIsDurableAcceptanceNotExecutionSuccess(t *testing.T) {
	entered, unblock := make(chan struct{}), make(chan struct{})
	messages := &fakeFeishuPort{reply: func(ctx context.Context, messageID string) (string, error) {
		close(entered)
		select {
		case <-unblock:
			return "", errors.New("remote outcome unknown")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	runtime := testRuntime(t, &fakeCorePort{}, messages)
	event := messageEvent("original-event-id", "chat-1")
	accepted, err := runtime.AcceptEvent(context.Background(), event)
	if err != nil || !accepted.Accepted {
		t.Fatalf("not accepted: %#v %v", accepted, err)
	}
	var disk eventInboxFile
	if _, err := privatestore.ReadJSON(runtime.inbox.path, &disk); err != nil {
		t.Fatal(err)
	}
	if len(disk.Events) != 1 || disk.Events[0].Event.ID != event.ID {
		t.Fatal("ACK preceded durable record")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler not dispatched")
	}
	close(unblock)
	waitInboxState(t, runtime, event.ID, "outcome_unknown")
	if result, err := runtime.AcceptEvent(context.Background(), event); err != nil || !result.Accepted {
		t.Fatalf("duplicate not acknowledged: %v", err)
	}
	runtime.Close()
	if messages.calls.Load() != 1 {
		t.Fatal("unknown outcome was retried")
	}
}

func TestEventACKFailsWhenDurableWriteFails(t *testing.T) {
	messages := &fakeFeishuPort{}
	runtime := testRuntime(t, &fakeCorePort{}, messages)
	if err := os.Mkdir(runtime.inbox.path, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.AcceptEvent(context.Background(), messageEvent("event-write-failure", "chat"))
	if err == nil || result.Accepted {
		t.Fatal("failed persistence was acknowledged")
	}
	if messages.calls.Load() != 0 {
		t.Fatal("event dispatched before persistence")
	}
}

func TestEventDedupeAndConflictingID(t *testing.T) {
	messages := &fakeFeishuPort{}
	runtime := testRuntime(t, &fakeCorePort{}, messages)
	event := messageEvent("event-dedupe", "chat")
	for index := 0; index < 10; index++ {
		if accepted, err := runtime.AcceptEvent(context.Background(), event); err != nil || !accepted.Accepted {
			t.Fatalf("accept: %v", err)
		}
	}
	waitInboxState(t, runtime, event.ID, "completed")
	conflict := messageEvent(event.ID, "another-chat")
	if accepted, err := runtime.AcceptEvent(context.Background(), conflict); err == nil || accepted.Accepted {
		t.Fatal("conflicting event ID accepted")
	}
	if messages.calls.Load() != 1 {
		t.Fatalf("duplicate dispatched %d times", messages.calls.Load())
	}
}

func TestRestartRecoversOnlyPendingNotPreviouslyRunningEvents(t *testing.T) {
	root := t.TempDir()
	pending, running := messageEvent("pending-event", "pending-chat"), messageEvent("running-event", "running-chat")
	_, _, pendingDigest, _ := decodeEvent(pending)
	_, _, runningDigest, _ := decodeEvent(running)
	file := eventInboxFile{SchemaVersion: 1, Events: []inboxEvent{
		{Event: pending, Digest: pendingDigest, Partition: 0, State: "pending", AcceptedAt: time.Now()},
		{Event: running, Digest: runningDigest, Partition: 1, State: "running", Attempts: 1, AcceptedAt: time.Now()},
	}}
	if err := privatestore.WriteJSON(filepath.Join(root, "integration-events-v1.json"), file); err != nil {
		t.Fatal(err)
	}
	messages := &fakeFeishuPort{}
	runtime, err := NewRuntime(root, messages, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	if messages.calls.Load() != 0 {
		t.Fatal("constructor started workers before handshake")
	}
	waitInboxState(t, runtime, running.ID, "outcome_unknown")
	if err := runtime.ResumeActive(); err != nil {
		t.Fatal(err)
	}
	waitInboxState(t, runtime, pending.ID, "completed")
	if _, err := runtime.AcceptEvent(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	if messages.calls.Load() != 1 {
		t.Fatal("previously running event replayed after restart")
	}
	var disk eventInboxFile
	if _, err := privatestore.ReadJSON(runtime.inbox.path, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.Events[1].State != "outcome_unknown" {
		t.Fatal("unknown outcome transition was not durable")
	}
}

func TestHandlerControlErrorIsNeverAutomaticallyReplayed(t *testing.T) {
	core := &fakeCorePort{start: func(context.Context, string) (string, error) {
		return "", errors.New("turn accepted but response lost")
	}}
	messages := &fakeFeishuPort{}
	runtime := testRuntime(t, core, messages)
	data, _ := json.Marshal(InboundMessage{EventID: "control-unknown", MessageID: "control-message", ChatID: "chat", SenderOpenID: "user-1", MessageType: "text", Text: "do work"})
	event := feishuprotocol.Event{ID: "control-unknown", Kind: "message", Payload: data}
	if _, err := runtime.AcceptEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	waitInboxState(t, runtime, event.ID, "outcome_unknown")
	if _, err := runtime.AcceptEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	root := runtime.dataRoot
	runtime.Close()
	restarted, err := NewRuntime(root, messages, core)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.ResumeEvents(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.AcceptEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	restarted.Close()
	if core.calls.Load() != 2 {
		t.Fatalf("start control was replayed: %d", core.calls.Load())
	}
}

func TestEventWorkerPoolIsBoundedAndSameConversationFIFO(t *testing.T) {
	chats := map[int]string{}
	for index := 0; len(chats) < eventWorkerCount; index++ {
		chat := fmt.Sprintf("chat-%d", index)
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(conversationKey(chat, "user-1")))
		chats[int(hash.Sum32()%eventWorkerCount)] = chat
	}
	started, release := make(chan string, 16), make(chan struct{})
	var active, maximum atomic.Int32
	messages := &fakeFeishuPort{reply: func(ctx context.Context, id string) (string, error) {
		count := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if count <= old || maximum.CompareAndSwap(old, count) {
				break
			}
		}
		started <- id
		select {
		case <-release:
			return "reply", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	runtime := testRuntime(t, &fakeCorePort{}, messages)
	for partition := 0; partition < eventWorkerCount; partition++ {
		if _, err := runtime.AcceptEvent(context.Background(), messageEvent(fmt.Sprintf("first-%d", partition), chats[partition])); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.AcceptEvent(context.Background(), messageEvent(fmt.Sprintf("second-%d", partition), chats[partition])); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < eventWorkerCount; index++ {
		select {
		case id := <-started:
			if len(id) < 6 || id[:6] != "first-" {
				t.Fatalf("conversation reordered: %s", id)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("independent conversations did not run concurrently")
		}
	}
	select {
	case id := <-started:
		t.Fatalf("worker bound exceeded: %s", id)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for partition := 0; partition < eventWorkerCount; partition++ {
		waitInboxState(t, runtime, fmt.Sprintf("second-%d", partition), "completed")
	}
	if maximum.Load() != eventWorkerCount {
		t.Fatalf("worker count: %d", maximum.Load())
	}
}

func TestInboxCapacityRejectsWithoutDispatch(t *testing.T) {
	runtime := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	for index := 0; index < eventInboxMaxRecords; index++ {
		runtime.inbox.file.Events = append(runtime.inbox.file.Events, inboxEvent{Event: feishuprotocol.Event{ID: fmt.Sprint(index)}, State: "completed", FinishedAt: time.Now()})
	}
	if result, err := runtime.AcceptEvent(context.Background(), messageEvent("overflow", "chat")); err == nil || result.Accepted {
		t.Fatal("capacity overflow acknowledged")
	}
}
