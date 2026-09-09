package integration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

type updatingCardPatcher func(context.Context, string, string) error

func (patch updatingCardPatcher) PatchCard(ctx context.Context, messageID, card string) error {
	return patch(ctx, messageID, card)
}

func TestCardSyncPreservesNewerPendingContent(t *testing.T) {
	runtime := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	link := seedCard(t, runtime)
	patcher := updatingCardPatcher(func(context.Context, string, string) error {
		_, err := runtime.Store().UpdateByID(link.ID, func(value *TaskLink) {
			value.Detail = "newer content"
			value.SetExtraValue("cardSyncPending", true)
		})
		return err
	})
	if err := SyncTaskLinkCard(context.Background(), runtime.Store(), patcher, link, "card-1"); err != nil {
		t.Fatal(err)
	}
	current, _, err := runtime.Store().FindByID(link.ID)
	if err != nil || !TaskLinkCardSyncPending(current) {
		t.Fatalf("newer content lost its sync marker: %v", err)
	}
}

func TestRepliesToInactiveLinksNeverCreateTasks(t *testing.T) {
	for _, state := range []string{"released", "expired"} {
		t.Run(state, func(t *testing.T) {
			core := &fakeCorePort{}
			runtime := testRuntime(t, core, &fakeFeishuPort{})
			link := seedCard(t, runtime)
			if _, err := runtime.Store().UpdateByID(link.ID, func(value *TaskLink) { value.LinkState = state }); err != nil {
				t.Fatal(err)
			}
			err := runtime.HandleMessage(context.Background(), InboundMessage{MessageID: "new-reply", RootID: "card-1", ParentID: "card-1", ChatID: "chat-1", ChatType: "p2p", SenderOpenID: "user-1", MessageType: "text", Text: "continue"})
			if !errors.Is(err, ErrInactiveTaskLink) || core.calls.Load() != 0 {
				t.Fatalf("inactive reply invoked Core: calls=%d err=%v", core.calls.Load(), err)
			}
		})
	}
}

type recoveringWorkspaceCore struct {
	*fakeCorePort
	lookups atomic.Int32
}

func (core *recoveringWorkspaceCore) Workspace(context.Context) (string, error) {
	if core.lookups.Add(1) == 1 {
		return "", errors.New("temporary workspace outage")
	}
	return "/workspace", nil
}

func TestEventRetriesOnlyProvenPreExecutionFailure(t *testing.T) {
	core := &recoveringWorkspaceCore{fakeCorePort: &fakeCorePort{}}
	runtime, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, core)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	event := feishuprotocol.Event{ID: "retry-workspace", Kind: "message", Payload: []byte(`{"EventID":"retry-workspace","MessageID":"message","SenderOpenID":"user-1","ChatID":"chat","ChatType":"p2p","MessageType":"text","Text":"hello"}`)}
	if accepted, err := runtime.AcceptEvent(context.Background(), event); err != nil || !accepted.Accepted {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for core.lookups.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	waitInboxState(t, runtime, event.ID, "completed")
	if core.calls.Load() != 2 {
		t.Fatalf("expected one thread and one turn, got %d calls", core.calls.Load())
	}
}

func TestNewTaskCreationFailureRepliesWithoutRetrying(t *testing.T) {
	core := &fakeCorePort{startThread: func(context.Context, string, string) (StartedThread, error) {
		return StartedThread{}, errors.New("codex unavailable")
	}}
	messages := &fakeFeishuPort{}
	runtime := testRuntime(t, core, messages)
	event := feishuprotocol.Event{ID: "failed-create", Kind: "message", Payload: []byte(`{"EventID":"failed-create","MessageID":"message","SenderOpenID":"user-1","ChatID":"chat","ChatType":"p2p","MessageType":"text","Text":"hello"}`)}
	if accepted, err := runtime.AcceptEvent(context.Background(), event); err != nil || !accepted.Accepted {
		t.Fatal(err)
	}
	waitInboxState(t, runtime, event.ID, "completed")
	if core.calls.Load() != 1 || messages.calls.Load() != 1 {
		t.Fatalf("unexpected calls: core=%d messages=%d", core.calls.Load(), messages.calls.Load())
	}
}

func TestInvalidLaunchContextRepliesWithoutCreatingTask(t *testing.T) {
	core := &fakeCorePort{workspace: func(context.Context) (string, error) {
		return "", ErrInvalidThreadLaunchContext
	}}
	messages := &fakeFeishuPort{}
	runtime := testRuntime(t, core, messages)
	if err := runtime.HandleMessage(context.Background(), InboundMessage{MessageID: "message", SenderOpenID: "user-1", ChatID: "chat", ChatType: "p2p", MessageType: "text", Text: "hello"}); err != nil {
		t.Fatalf("reported launch context failure must be terminal: %v", err)
	}
	if core.calls.Load() != 0 || messages.calls.Load() != 1 {
		t.Fatalf("unexpected calls: core=%d messages=%d", core.calls.Load(), messages.calls.Load())
	}
}

func TestProjectlessMessageStoresCapturedDirectoryWithoutOverridingTurns(t *testing.T) {
	core := &fakeCorePort{
		workspace: func(context.Context) (string, error) { return "", nil },
		startThread: func(context.Context, string, string) (StartedThread, error) {
			return StartedThread{ThreadID: "projectless-thread", CWD: "/Users/example"}, nil
		},
	}
	runtime := testRuntime(t, core, &fakeFeishuPort{})
	if err := runtime.HandleMessage(context.Background(), InboundMessage{MessageID: "message", SenderOpenID: "user-1", ChatID: "chat", ChatType: "p2p", MessageType: "text", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if core.threadCWD.Load().(string) != "" || core.turnCWD.Load().(string) != "" {
		t.Fatalf("projectless task received cwd overrides: thread=%q turn=%q", core.threadCWD.Load(), core.turnCWD.Load())
	}
	file, err := runtime.Store().Load()
	if err != nil || len(file.Links) != 1 {
		t.Fatalf("load task links: %#v %v", file, err)
	}
	link := file.Links[0]
	if link.ThreadID != "projectless-thread" || link.ProjectName != "" || link.ExtraString("launchScope") != LaunchScopeProjectless || link.ExtraString("workingDirectory") != "/Users/example" {
		t.Fatalf("unexpected projectless task link: %#v", link)
	}
}

func TestKSFMessageKeepsWorkspaceOverride(t *testing.T) {
	core := &fakeCorePort{}
	runtime := testRuntime(t, core, &fakeFeishuPort{})
	if err := runtime.HandleMessage(context.Background(), InboundMessage{MessageID: "message", SenderOpenID: "user-1", ChatID: "chat", ChatType: "p2p", MessageType: "text", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if core.threadCWD.Load().(string) != "/workspace" || core.turnCWD.Load().(string) != "/workspace" {
		t.Fatalf("KSF task lost cwd overrides: thread=%q turn=%q", core.threadCWD.Load(), core.turnCWD.Load())
	}
	file, err := runtime.Store().Load()
	if err != nil || len(file.Links) != 1 || file.Links[0].ExtraString("launchScope") != LaunchScopeKSF {
		t.Fatalf("unexpected KSF task link: %#v %v", file, err)
	}
}
