package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/corebridge"
)

type fakeCorePort struct {
	CorePort
	calls        atomic.Int32
	start        func(context.Context, string) (string, error)
	answer       func(json.RawMessage) (json.RawMessage, error)
	interruptErr error
}

func (port *fakeCorePort) Workspace(context.Context) (string, error) { return "/workspace", nil }
func (port *fakeCorePort) ReadThread(ctx context.Context, owner, threadID, turnID string) (map[string]any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (port *fakeCorePort) ProjectionOwner(threadID, owner string) string { return owner }
func (port *fakeCorePort) PendingInput(string) (corebridge.PendingUserInput, bool) {
	return corebridge.PendingUserInput{}, false
}
func (port *fakeCorePort) StartThread(context.Context, string, string) (string, error) {
	port.calls.Add(1)
	return "thread-1", nil
}
func (port *fakeCorePort) StartTurn(ctx context.Context, key, owner, threadID, cwd, text string, mode map[string]any) (string, error) {
	port.calls.Add(1)
	if port.start != nil {
		return port.start(ctx, text)
	}
	return "turn-1", nil
}
func (port *fakeCorePort) SteerTurn(ctx context.Context, key, owner, threadID, turnID, cwd, text string) (string, error) {
	port.calls.Add(1)
	return turnID, nil
}
func (port *fakeCorePort) InterruptTurn(context.Context, string, string, string, string) error {
	port.calls.Add(1)
	return port.interruptErr
}
func (port *fakeCorePort) AnswerInput(ctx context.Context, key, owner, threadID, turnID string, request json.RawMessage, questionID, revision, answer string) (json.RawMessage, error) {
	port.calls.Add(1)
	if port.answer != nil {
		return port.answer(request)
	}
	return request, nil
}

type fakeFeishuPort struct {
	calls    atomic.Int32
	staged   atomic.Int32
	cleaned  atomic.Int32
	reply    func(context.Context, string) (string, error)
	stage    func(context.Context, InboundMessage) (StagedInboundMessage, error)
	mu       sync.Mutex
	keys     []string
	patchErr error
}

func (port *fakeFeishuPort) Send(ctx context.Context, target MessageTarget, format, content, key string) (string, error) {
	port.calls.Add(1)
	port.mu.Lock()
	port.keys = append(port.keys, key)
	port.mu.Unlock()
	return "card-1", nil
}
func (port *fakeFeishuPort) Reply(ctx context.Context, messageID, format, content, key string) (string, error) {
	port.calls.Add(1)
	if port.reply != nil {
		return port.reply(ctx, messageID)
	}
	return "reply-1", nil
}
func (port *fakeFeishuPort) PatchCard(context.Context, string, string) error {
	port.calls.Add(1)
	return port.patchErr
}
func (port *fakeFeishuPort) ResolveMessageTarget(alias string) (MessageTarget, error) {
	if alias != "me" {
		return MessageTarget{}, errors.New("target not found")
	}
	return MessageTarget{Type: "open_id", ID: "user-1"}, nil
}
func (port *fakeFeishuPort) AliasForOpenID(string) string { return "me" }
func (port *fakeFeishuPort) StageInbound(ctx context.Context, message InboundMessage, maximum int64) (StagedInboundMessage, error) {
	port.staged.Add(1)
	if maximum != 25*1024*1024 {
		return StagedInboundMessage{}, errors.New("unexpected staging limit")
	}
	if port.stage != nil {
		return port.stage(ctx, message)
	}
	return StagedInboundMessage{}, errors.New("staging unavailable")
}
func (port *fakeFeishuPort) CleanupInbound(context.Context, string) error {
	port.cleaned.Add(1)
	return nil
}

func testRuntime(t *testing.T, core *fakeCorePort, messages *fakeFeishuPort) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(t.TempDir(), messages, core)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func seedCard(t *testing.T, runtime *Runtime) TaskLink {
	t.Helper()
	link, err := runtime.Store().Upsert("thread-1", "Task", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	link, err = runtime.Store().UpdateByID(link.ID, func(value *TaskLink) {
		value.Target = MessageTarget{Type: "open_id", ID: "user-1"}
		value.RootMessageID = "card-1"
		value.ActiveTurnID = "turn-1"
		value.TurnState = "running"
		value.SetExtraString("runtimeOwner", "bridge")
	})
	if err != nil {
		t.Fatal(err)
	}
	return link
}

func TestInactiveCardsNeverReachPortsOrMutateStore(t *testing.T) {
	for _, state := range []string{"released", "expired", "elapsed-active"} {
		for _, actionName := range []string{"task_link_interrupt", "task_link_followup", "task_link_answer", "task_link_implement_plan", "task_link_release"} {
			t.Run(state+"/"+actionName, func(t *testing.T) {
				core, messages := &fakeCorePort{}, &fakeFeishuPort{}
				runtime := testRuntime(t, core, messages)
				link := seedCard(t, runtime)
				_, err := runtime.Store().UpdateByID(link.ID, func(value *TaskLink) {
					if state == "elapsed-active" {
						value.ExpiresAt = time.Now().Add(-time.Second)
					} else {
						value.LinkState = state
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				before, _ := os.ReadFile(runtime.Store().path)
				err = runtime.HandleCard(context.Background(), InboundCardAction{LinkID: link.ID, TaskKey: link.TaskKey, MessageID: "card-1", OperatorOpenID: "user-1", Action: actionName})
				if !errors.Is(err, ErrInactiveTaskLink) {
					t.Fatalf("got %v", err)
				}
				after, _ := os.ReadFile(runtime.Store().path)
				if string(before) != string(after) || core.calls.Load() != 0 || messages.calls.Load() != 0 {
					t.Fatal("inactive card caused effects")
				}
			})
		}
	}
}

func TestReleaseReplayDoesNotPerformRemoteAction(t *testing.T) {
	core, messages := &fakeCorePort{}, &fakeFeishuPort{}
	runtime := testRuntime(t, core, messages)
	link := seedCard(t, runtime)
	first, err := runtime.Release(context.Background(), link.TaskKey)
	if err != nil {
		t.Fatal(err)
	}
	calls := messages.calls.Load()
	second, err := runtime.Release(context.Background(), link.TaskKey)
	if err != nil || first.LinkState != "released" || second.LinkState != "released" {
		t.Fatalf("release replay: %#v %v", second, err)
	}
	if messages.calls.Load() != calls || core.calls.Load() != 0 {
		t.Fatal("release replay performed an action")
	}
}

func TestInterruptFailureDoesNotPretendTaskStopped(t *testing.T) {
	core, messages := &fakeCorePort{interruptErr: errors.New("unavailable")}, &fakeFeishuPort{}
	runtime := testRuntime(t, core, messages)
	link := seedCard(t, runtime)
	if _, err := runtime.Interrupt(context.Background(), link.TaskKey); err == nil {
		t.Fatal("interrupt succeeded")
	}
	stored, _, _ := runtime.Store().FindByID(link.ID)
	if stored.TurnState != "running" || messages.calls.Load() != 0 {
		t.Fatal("failed control mutated status")
	}
}

func TestInboundStagingUsesOnlyCapabilityPort(t *testing.T) {
	core := &fakeCorePort{start: func(ctx context.Context, text string) (string, error) {
		if !strings.Contains(text, "/staged/report") || strings.Contains(text, "private-file-key") {
			return "", fmt.Errorf("unexpected asset prompt: %s", text)
		}
		return "", errors.New("turn unavailable")
	}}
	messages := &fakeFeishuPort{stage: func(ctx context.Context, message InboundMessage) (StagedInboundMessage, error) {
		return StagedInboundMessage{Text: "Read", CleanupDir: "/staged", Assets: []InboundAsset{{LocalPath: "/staged/report", DisplayName: "report"}}}, nil
	}}
	runtime := testRuntime(t, core, messages)
	err := runtime.HandleMessage(context.Background(), InboundMessage{MessageID: "message-1", SenderOpenID: "user-1", MessageType: "file", Raw: map[string]any{"key": "private-file-key"}})
	if err == nil || messages.staged.Load() != 1 || messages.cleaned.Load() != 1 || core.calls.Load() != 2 {
		t.Fatalf("staging route failed: %v", err)
	}
}

func TestAnswerPreservesTypedRequestIDAndRevision(t *testing.T) {
	core, messages := &fakeCorePort{}, &fakeFeishuPort{}
	runtime := testRuntime(t, core, messages)
	link := seedCard(t, runtime)
	request := json.RawMessage(`9007199254740993`)
	questions := []map[string]any{{"id": "q1", "question": "Pick"}}
	revision := PendingQuestionRevisionScoped(link.TaskKey, link.ActiveTurnID, "bridge", request, questions)
	_, err := runtime.Store().UpdateByID(link.ID, func(value *TaskLink) {
		value.TurnState = "waiting_input"
		value.SetExtraRaw("pendingQuestionRequestRef", request)
		value.SetExtraString("pendingQuestionRevision", revision)
		value.SetExtraValue("pendingQuestions", questions)
	})
	if err != nil {
		t.Fatal(err)
	}
	core.answer = func(raw json.RawMessage) (json.RawMessage, error) {
		if string(raw) != string(request) {
			return nil, errors.New("request type or precision changed")
		}
		return raw, nil
	}
	action := InboundCardAction{TaskKey: link.TaskKey, LinkID: link.ID, MessageID: "card-1", OperatorOpenID: "user-1", Action: "task_link_answer", QuestionRevision: revision, Value: map[string]any{"questionId": "q1", "answer": "A"}}
	if err := runtime.HandleCard(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	stored, _, _ := runtime.Store().FindByID(link.ID)
	if string(stored.ExtraRaw("pendingInputAnswerRequestRef")) != string(request) {
		t.Fatal("typed answer reference lost")
	}
	if err := runtime.HandleCard(context.Background(), action); err == nil {
		t.Fatal("stale answer accepted")
	}
	if core.calls.Load() != 1 {
		t.Fatal("stale answer reached control port")
	}
}

func TestSchemaTwoEnvelopeAndLinkUnknownFieldsSurvive(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "task-links-v1.json")
	data := `{"protocol":"codex-feishu-task-link-v1","schemaVersion":2,"updatedAt":"2026-09-04T00:00:00Z","future":{"n":9007199254740993},"links":[{"id":"LINK-KEEP","taskKey":"keep-key","threadId":"thread-1","title":"old","targetAlias":"me","linkState":"active","turnState":"idle","createdAt":"2026-09-04T00:00:00Z","updatedAt":"2026-09-04T00:00:00Z","expiresAt":"2099-01-01T00:00:00Z","futureLink":{"n":9007199254740993}}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewTaskLinkStore(root)
	link, err := store.UpdateActiveByID("LINK-KEEP", func(value *TaskLink) { value.Title = "updated" })
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(file.Extra["future"])
	linkExtra, _ := json.Marshal(file.Links[0].Extra["futureLink"])
	if link.ID != "LINK-KEEP" || link.TaskKey != "keep-key" || string(envelope) != `{"n":9007199254740993}` || string(linkExtra) != `{"n":9007199254740993}` {
		t.Fatalf("schema changed: %#v", file)
	}
}

func TestCloseCancelsObserversAndKeepsRunningState(t *testing.T) {
	runtime := testRuntime(t, &fakeCorePort{}, &fakeFeishuPort{})
	link := seedCard(t, runtime)
	if err := runtime.ResumeActive(); err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	stored, _, _ := runtime.Store().FindByID(link.ID)
	if stored.TurnState != "running" {
		t.Fatalf("shutdown changed durable task: %s", stored.TurnState)
	}
	if err := runtime.HandleMessage(context.Background(), InboundMessage{}); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("closed runtime accepted work: %v", err)
	}
}
