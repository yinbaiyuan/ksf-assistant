package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type recordingActionExecutor struct {
	calls          int
	err            error
	preflight      map[string]any
	preflightErr   error
	preflightCalls int
}

func (executor *recordingActionExecutor) ExecuteWithOptions(_ context.Context, capabilityID string, input map[string]any, _ CapabilityExecutionOptions) (map[string]any, error) {
	executor.calls++
	if executor.err != nil {
		return nil, executor.err
	}
	return map[string]any{"capabilityId": capabilityID, "inputSeen": len(input)}, nil
}

func (executor *recordingActionExecutor) ReadPreflight(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	executor.preflightCalls++
	return executor.preflight, executor.preflightErr
}

func TestGovernedActionboxConsumesQueuedOperationAndCompletesIt(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	policy := NewCapabilityPolicyStore(root)
	operations := NewOperationService(root, policy, nil)
	definition, ok := CapabilityByID("im.chat.create")
	if !ok {
		t.Fatal("missing fixture capability")
	}
	input := map[string]any{"name": "governed action"}
	view, challenge, err := operations.Prepare(context.Background(), definition, input, "test")
	if err != nil || challenge != "" || view.Status != OperationQueued {
		t.Fatalf("prepare: view=%#v challenge=%q err=%v", view, challenge, err)
	}
	id, _ := NewActionID()
	box := NewGovernedActionbox(root, operations)
	request := ActionRequest{
		ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute",
		CapabilityID: definition.ID, Identity: definition.Identity, Input: input,
		OperationID: view.ID, ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC(),
	}
	if err := box.Submit(request); err != nil {
		t.Fatal(err)
	}
	executor := &recordingActionExecutor{}
	if err := box.process(context.Background(), executor); err != nil {
		t.Fatal(err)
	}
	status, err := operations.Status(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != OperationSucceeded || status.NextAction != "stop" || status.AttemptCount != 1 {
		t.Fatalf("unexpected operation status: %#v", status)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
}

func TestGovernedActionboxTimeoutBecomesOutcomeUnknownWithoutReplay(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	operations := NewOperationService(root, NewCapabilityPolicyStore(root), nil)
	definition, _ := CapabilityByID("im.chat.create")
	input := map[string]any{"name": "timeout action"}
	view, _, err := operations.Prepare(context.Background(), definition, input, "test")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewActionID()
	box := NewGovernedActionbox(root, operations)
	if err := box.Submit(ActionRequest{
		ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute",
		CapabilityID: definition.ID, Identity: definition.Identity, Input: input,
		OperationID: view.ID, ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	executor := &recordingActionExecutor{err: context.DeadlineExceeded}
	if err := box.process(context.Background(), executor); err != nil {
		t.Fatal(err)
	}
	status, err := operations.Status(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != OperationOutcomeUnknown || status.NextAction != "query_same_operation" {
		t.Fatalf("unexpected operation status: %#v", status)
	}
	if err := box.process(context.Background(), executor); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 {
		t.Fatalf("timed-out side effect replayed: calls=%d", executor.calls)
	}
	result, found, err := box.FindResult(id)
	if err != nil || !found || result.Status != string(OperationOutcomeUnknown) {
		t.Fatalf("result=%#v found=%v err=%v", result, found, err)
	}
}

func TestGovernedActionboxDistinguishesPreflightFailureFromPostSubmitUncertainty(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want OperationStatus
	}{
		{name: "preflight", err: &CapabilityExecutionError{Phase: "preflight", Err: errors.New("unavailable")}, want: OperationFailed},
		{name: "write", err: &CapabilityExecutionError{Phase: "write", Err: errors.New("connection reset")}, want: OperationOutcomeUnknown},
		{name: "verification", err: &CapabilityExecutionError{Phase: "verification", Err: errors.New("reread unavailable")}, want: OperationOutcomeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			enableCapabilityWrites(t, root)
			operations := NewOperationService(root, NewCapabilityPolicyStore(root), nil)
			definition, _ := CapabilityByID("im.chat.create")
			input := map[string]any{"name": "phase test"}
			view, _, err := operations.Prepare(context.Background(), definition, input, "test")
			if err != nil {
				t.Fatal(err)
			}
			id, _ := NewActionID()
			box := NewGovernedActionbox(root, operations)
			if err := box.Submit(ActionRequest{ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: input, OperationID: view.ID, ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if err := box.process(context.Background(), &recordingActionExecutor{err: test.err}); err != nil {
				t.Fatal(err)
			}
			status, err := operations.Status(view.ID)
			if err != nil || status.Status != test.want {
				t.Fatalf("status=%#v err=%v want=%s", status, err, test.want)
			}
		})
	}
}

func TestGovernedActionboxRejectsMismatchedOperationInput(t *testing.T) {
	root := t.TempDir()
	operations := NewOperationService(root, NewCapabilityPolicyStore(root), nil)
	definition, _ := CapabilityByID("im.chat.create")
	view, _, err := operations.Prepare(context.Background(), definition, map[string]any{"name": "expected"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewActionID()
	err = NewGovernedActionbox(root, operations).Submit(ActionRequest{
		ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute",
		CapabilityID: definition.ID, Identity: definition.Identity, Input: map[string]any{"name": "changed"},
		OperationID: view.ID, ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrOperationRequestMismatch) {
		t.Fatalf("expected operation mismatch, got %v", err)
	}
}

func TestGovernedActionboxRechecksPreflightImmediatelyBeforeExecution(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	policyStore := NewCapabilityPolicyStore(root)
	policy, err := policyStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides["docs.history.revert"] = CapabilityConfirmEach
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	operations := NewOperationService(root, policyStore, nil)
	definition, ok := CapabilityByID("docs.history.revert")
	if !ok || definition.Preflight == nil {
		t.Fatal("missing guarded destructive fixture")
	}
	input := map[string]any{"doc": "https://example.invalid/docx/example", "history-version-id": "version_1"}
	prepared, challenge, err := operations.PrepareWithEvidence(definition, input, "test", map[string]any{"revision": "v1"})
	if err != nil || prepared.Status != OperationAwaitingConfirmation {
		t.Fatalf("prepare: view=%#v err=%v", prepared, err)
	}
	queued, err := operations.ConfirmWithEvidence(prepared.ID, challenge, map[string]any{"revision": "v1"})
	if err != nil || queued.Status != OperationQueued {
		t.Fatalf("confirm: view=%#v err=%v", queued, err)
	}
	id, _ := NewActionID()
	box := NewGovernedActionbox(root, operations)
	if err := box.Submit(ActionRequest{
		ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute",
		CapabilityID: definition.ID, Identity: definition.Identity, Input: input,
		OperationID: prepared.ID, ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	executor := &recordingActionExecutor{preflight: map[string]any{"revision": "v2"}}
	if err := box.process(context.Background(), executor); err != nil {
		t.Fatal(err)
	}
	status, err := operations.Status(prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != OperationFailed || status.ErrorCode != "preflight_changed" || status.NextAction != "reprepare_on_user_request" {
		t.Fatalf("unexpected operation status: %#v", status)
	}
	if executor.calls != 0 || executor.preflightCalls != 1 {
		t.Fatalf("executor calls=%d preflight calls=%d", executor.calls, executor.preflightCalls)
	}
}

func TestGovernedActionboxPreflightReadFailureStopsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	policyStore := NewCapabilityPolicyStore(root)
	policy, _ := policyStore.Load()
	policy.CapabilityOverrides["docs.history.revert"] = CapabilityConfirmEach
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	operations := NewOperationService(root, policyStore, nil)
	definition, _ := CapabilityByID("docs.history.revert")
	input := map[string]any{"doc": "https://example.invalid/docx/example", "history-version-id": "version_1"}
	prepared, challenge, err := operations.PrepareWithEvidence(definition, input, "test", map[string]any{"revision": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operations.ConfirmWithEvidence(prepared.ID, challenge, map[string]any{"revision": "v1"}); err != nil {
		t.Fatal(err)
	}
	id, _ := NewActionID()
	box := NewGovernedActionbox(root, operations)
	if err := box.Submit(ActionRequest{ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: input, OperationID: prepared.ID, ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	executor := &recordingActionExecutor{preflightErr: errors.New("temporarily unavailable")}
	if err := box.process(context.Background(), executor); err != nil {
		t.Fatal(err)
	}
	status, err := operations.Status(prepared.ID)
	if err != nil || status.Status != OperationFailed || status.ErrorCode != "preflight_recheck_failed" || status.NextAction != "reprepare_on_user_request" || executor.calls != 0 {
		t.Fatalf("status=%#v calls=%d err=%v", status, executor.calls, err)
	}
}

func TestActionboxDryRunPersistsTerminalState(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	settings, err := NewSettingsStore(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	settings.Outbound.Enabled = true
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	box := NewActionbox(root)
	id, _ := NewActionID()
	definition, _ := CapabilityByID("im.message.reply")
	request := ActionRequest{ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: map[string]any{"message-id": "om_test", "text": "hello"}, ExplicitAuthorization: true, DryRun: true, Source: "test", CreatedAt: time.Now().UTC()}
	if err := box.Submit(request); err != nil {
		t.Fatal(err)
	}
	if err := box.process(context.Background(), CapabilityExecutor{}); err != nil {
		t.Fatal(err)
	}
	result, found, err := box.FindResult(id)
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Status != "dry_run" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if err := box.process(context.Background(), CapabilityExecutor{}); err != nil {
		t.Fatal(err)
	}
	state, err := box.repository.readIndex()
	if err != nil {
		t.Fatal(err)
	}
	if state.Processed != 1 || state.Terminal != 1 || state.Pending != 0 || state.Running != 0 {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestQueueStatePreservesNodeLastTrigger(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":2,"processedLineCount":0,"processedIds":{},"lastProcessedAt":"","lastError":"","lastTrigger":{"source":"timer"},"wake":{"enabled":true,"host":"127.0.0.1","configuredPort":0,"actualPort":null}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var state queueState
	missing, err := readPrivateJSON(path, &state)
	if err != nil || missing || state.LastTrigger == nil {
		t.Fatalf("Node state was not accepted: missing=%v err=%v state=%#v", missing, err, state)
	}
}

func TestLegacyQueueCannotAcquireDestructivePermissionDuringUpgrade(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	box := NewGovernedActionbox(root, NewOperationService(root, NewCapabilityPolicyStore(root), nil))
	request := ActionRequest{
		ID: "ACT-LEGACY-DESTRUCTIVE", Type: "feishu_capability", Domain: "capability", Action: "execute",
		CapabilityID: "docs.history.revert", Identity: "user",
		Input:                 map[string]any{"doc": "https://example.invalid/docx/example", "history-version-id": "version_1"},
		ExplicitAuthorization: true, ConfirmHighImpact: true, Source: "legacy", CreatedAt: time.Now().UTC(),
	}
	if err := ensurePrivateDirectory(box.root); err != nil {
		t.Fatal(err)
	}
	if err := appendPrivateJSONL(box.queuePath(), request); err != nil {
		t.Fatal(err)
	}
	executor := &recordingActionExecutor{}
	if err := box.process(context.Background(), executor); err != nil {
		t.Fatal(err)
	}
	result, found, err := box.FindResult(request.ID)
	if err != nil || !found || result.Status != "failed" || result.Error != "destructive_operation_requires_governance" || executor.calls != 0 {
		t.Fatalf("result=%#v found=%v calls=%d err=%v", result, found, executor.calls, err)
	}
}

func TestActionboxRejectsMissingHighImpactConfirmation(t *testing.T) {
	box := NewActionbox(t.TempDir())
	definition, ok := CapabilityByID("sheets.range.move")
	if !ok {
		t.Fatal("missing capability")
	}
	if definition.Risk != "high-impact-write" {
		t.Skip("fixture capability risk changed")
	}
	err := box.Submit(ActionRequest{ID: "ACT-test", Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: map[string]any{}, ExplicitAuthorization: true})
	if err == nil {
		t.Fatal("high-impact request accepted without confirmation")
	}
}

func TestActionboxConcurrentSubmitKeepsEveryV3WorkItem(t *testing.T) {
	root := t.TempDir()
	const total = 40
	var wait sync.WaitGroup
	for index := 0; index < total; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			box := NewActionbox(root)
			request := ActionRequest{
				ID: "ACT-CONCURRENT-" + time.Unix(int64(index), 0).UTC().Format("150405"), Type: "feishu_capability",
				Domain: "capability", Action: "execute", CapabilityID: "im.chat.create", Identity: "user",
				Input: map[string]any{"name": "concurrent test"}, ExplicitAuthorization: true,
				Source: "test", CreatedAt: time.Now().UTC(),
			}
			if err := box.Submit(request); err != nil {
				t.Errorf("submit %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	box := NewActionbox(root)
	entries, err := os.ReadDir(box.repository.stateDir("pending"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			count++
		}
	}
	if count != total {
		t.Fatalf("queue has %d records, want %d", count, total)
	}
	index, err := box.repository.readIndex()
	if err != nil || index.Pending != total {
		t.Fatalf("index=%#v err=%v", index, err)
	}
}
