package feishu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type reviewBarrierExecutor struct {
	UnifiedCapabilityExecutor
	gate <-chan struct{}
	wait time.Duration
}

func (executor reviewBarrierExecutor) ExecuteWithOptions(ctx context.Context, id string, input map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	<-executor.gate
	options.Timeout = 2 * time.Second
	if executor.wait > 0 {
		options.Timeout = executor.wait
	}
	return executor.UnifiedCapabilityExecutor.ExecuteWithOptions(ctx, id, input, options)
}

func TestReviewNestedSchedulerCapacity(t *testing.T) {
	root := t.TempDir()
	settings := enableCapabilityWrites(t, root)
	settings.Docbox.Enabled, settings.Docbox.DryRun = true, false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	transport := &reviewDocumentTransport{}
	executor := reviewBarrierExecutor{UnifiedCapabilityExecutor: UnifiedCapabilityExecutor{DataRoot: root, Documents: transport}, gate: gate}
	service := NewCapabilityService(root, executor, nil)
	for index := 0; index < 2; index++ {
		if _, err := service.Prepare(context.Background(), "docs.service.document.create", map[string]any{"target-kind": "folder_token", "target-value": fmt.Sprintf("folder_%d", index), "content": "fixture", "format": "text", "source": "review"}, "review"); err != nil {
			t.Fatal(err)
		}
	}
	scheduler := NewWorkScheduler(root)
	scheduler.RegisterCapabilityService(service)
	scheduler.RegisterDocbox(NewDocbox(root), CapabilityExecutor{}, false)
	if err := scheduler.dispatchAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scheduler.cli != 2 || scheduler.active != 2 {
		t.Fatalf("capacity cli=%d active=%d", scheduler.cli, scheduler.active)
	}
	close(gate)
	reviewDrain(t, scheduler, 2)
	if transport.writes.Load() != 2 {
		t.Fatalf("writes=%d", transport.writes.Load())
	}
	reviewAssertNoChildWork(t, root, "docbox")
}

func TestReviewAcceptedWorkMustBeReadable(t *testing.T) {
	root := t.TempDir()
	box := NewOutbox(root)
	request := OutboxRequest{ID: "OUT-review-large", Type: "text", Target: MessageTarget{Type: "open_id", ID: "ou_fixture"}, Text: strings.Repeat("<", 200000), ExplicitAuthorization: true, Source: "review"}
	if err := box.Submit(request); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(box.repository.path("pending", request.ID))
	if err != nil {
		t.Fatal(err)
	}
	heads, err := box.repository.pendingHeads()
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 1 {
		t.Fatalf("Submit accepted %d bytes but pendingHeads silently returns %d items (reader max=%d)", info.Size(), len(heads), maximumPrivateJSONBytes)
	}
}

func TestReviewAcceptedResultMustBeReadable(t *testing.T) {
	repo := newWorkRepository(t.TempDir(), "outbox")
	if err := repo.enqueue("OUT-result", map[string]any{"id": "OUT-result"}, "key", "go-sdk", "standard", "never", time.Now()); err != nil {
		t.Fatal(err)
	}
	item, found, err := repo.claim()
	if err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	if err := repo.finish(item, map[string]any{"id": item.ID, "status": "sent", "payload": strings.Repeat("a", 1200000)}, ""); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if found, err := repo.findResult(item.ID, &result); err != nil || !found {
		t.Fatalf("finish succeeded but result unreadable: found=%v err=%v", found, err)
	}
}

func TestReviewDocboxRejectsFictitiousOperation(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "fake-lark")
	log := filepath.Join(root, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nprintf '{\"data\":{\"file_token\":\"doc_fixture\"}}\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	request := DocumentRequest{ID: "DOC-review", OperationID: "OP-20260905120000-ABCDEF12", Type: "document_task", Action: "update_document", Identity: "user", Target: &DocumentTarget{Kind: "docx_token", Value: "doc_fixture"}, Content: DocumentContent{Format: "text", Text: "replacement"}, Instruction: "review", ExplicitAuthorization: true, Source: "review", UpdateMode: "overwrite"}
	box := NewDocbox(root)
	if err := box.Submit(request); err != nil {
		return
	}
	if _, err := box.ProcessOne(context.Background(), CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root}, false); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "+update") {
		t.Fatal("fake remote overwrite executed without any OperationRecord or policy confirmation")
	}
}

func TestReviewCrashBetweenQueueClaimAndOperationClaim(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "fixture"}, "review")
	if err != nil {
		t.Fatal(err)
	}
	item, found, err := service.actionbox.repository.claim()
	if err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	if err := service.RecoverInterrupted(1000); err != nil {
		t.Fatal(err)
	}
	if _, err := service.actionbox.repository.recoverRunning(1000); err != nil {
		t.Fatal(err)
	}
	view, err := service.Status(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result ActionResult
	if found, err := service.actionbox.repository.findResult(item.ID, &result); err != nil || !found {
		t.Fatalf("terminal %v %v", found, err)
	}
	if view.Status == OperationQueued {
		t.Fatalf("operation stays queued/execute forever while work is terminal %s; result operationId=%q", result.Status, result.OperationID)
	}
}

type reviewFaultExecutor struct{ root, operationID string }

func (executor reviewFaultExecutor) ExecuteWithOptions(context.Context, string, map[string]any, CapabilityExecutionOptions) (map[string]any, error) {
	err := os.Chmod(filepath.Join(executor.root, "operations", executor.operationID+".json"), 0644)
	return map[string]any{"receipt": "fixture"}, err
}

func TestReviewOperationPersistenceErrorNotReportedAsSuccess(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	operations := NewOperationService(root, NewCapabilityPolicyStore(root), nil)
	definition, _ := CapabilityByID("im.chat.create")
	input := map[string]any{"name": "fixture"}
	view, _, err := operations.Prepare(context.Background(), definition, input, "review")
	if err != nil {
		t.Fatal(err)
	}
	box := NewGovernedActionbox(root, operations)
	request := ActionRequest{ID: "ACT-fault", OperationID: view.ID, Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: input, ExplicitAuthorization: true, Source: "review"}
	if err := box.Submit(request); err != nil {
		t.Fatal(err)
	}
	if err := box.process(context.Background(), reviewFaultExecutor{root: root, operationID: view.ID}); err != nil {
		return
	}
	if err := os.Chmod(filepath.Join(root, "operations", view.ID+".json"), 0600); err != nil {
		t.Fatal(err)
	}
	view, err = operations.Status(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, found, err := box.FindResult(request.ID)
	if err != nil || !found {
		t.Fatalf("result %v %v", found, err)
	}
	if result.Status == "succeeded" && view.Status != OperationSucceeded {
		t.Fatalf("queue reports %s but operation remains %s after persistence error", result.Status, view.Status)
	}
}

type reviewTimeoutSender struct{}

func (reviewTimeoutSender) Send(context.Context, MessageTarget, string, string, string) (string, error) {
	return "", context.DeadlineExceeded
}

func TestReviewOutboxTimeoutRemainsUnknown(t *testing.T) {
	box := NewOutbox(t.TempDir())
	request := OutboxRequest{ID: "OUT-timeout", Type: "text", Target: MessageTarget{Type: "open_id", ID: "ou_fixture"}, Text: "fixture", Source: "review", ExplicitAuthorization: true}
	ctx, operationID := reviewRunningBoundary(t, box.dataRoot, "im.sdk.message.send", outboxCapabilityInput(box.dataRoot, request))
	request.OperationID = operationID
	result := box.processRequest(ctx, reviewTimeoutSender{}, request, false)
	if result.Status != string(OperationOutcomeUnknown) {
		t.Fatalf("uncertain transport timeout became status=%q error=%q", result.Status, result.Error)
	}
}

func TestReviewUnknownManualRecordsDoNotStarveReconciliation(t *testing.T) {
	root := t.TempDir()
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	base := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 21; index++ {
		capability := "im.sdk.message.send"
		if index == 20 {
			capability = "docs.service.document.append"
		}
		record := OperationRecord{Version: 1, ID: fmt.Sprintf("OP-%s-%08X", base.Add(time.Duration(index)*time.Second).Format("20060102150405"), index), CapabilityID: capability, Status: OperationOutcomeUnknown, NextAction: "manual_review", Input: map[string]any{}, CreatedAt: base, UpdatedAt: base}
		if err := service.operations.save(record); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 3; index++ {
		if err := service.ReconcileUnknown(context.Background(), 20); err != nil {
			t.Fatal(err)
		}
	}
	records, err := service.operations.Unknown(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.CapabilityID == "docs.service.document.append" && record.VerificationAttempts == 0 {
			t.Fatal("20 older no-reread manual records consume every page; later reconcilable operation never attempted")
		}
	}
}

func TestReviewClaimIndexFailureLeavesNoLiveRecovery(t *testing.T) {
	repo := newWorkRepository(t.TempDir(), "outbox")
	if err := repo.enqueue("OUT-index", map[string]any{"id": "OUT-index"}, "key", "go-sdk", "standard", "never", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo.indexPath(), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	_, found, err := repo.claimID("OUT-index")
	if err == nil || found {
		t.Fatal("expected injected index failure")
	}
	if _, err := os.Stat(repo.path("running", "OUT-index")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claim returned failure but already moved work to running; scheduler has no worker or periodic running recovery: %v", err)
	}
}

func reviewSDKService(t *testing.T, root string, gate <-chan struct{}, wait time.Duration, count int) (*CapabilityService, []string) {
	t.Helper()
	settings := enableCapabilityWrites(t, root)
	settings.Outbound.Enabled, settings.Outbound.DryRun = true, false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	service := NewCapabilityService(root, reviewBarrierExecutor{UnifiedCapabilityExecutor: UnifiedCapabilityExecutor{DataRoot: root}, gate: gate, wait: wait}, nil)
	ids := []string{}
	for index := 0; index < count; index++ {
		input := map[string]any{"request-id": fmt.Sprintf("OUT-review-%d", index), "target-type": "open_id", "target-id": fmt.Sprintf("ou_fixture_%d", index), "format": "text", "text": "fixture", "source": "review"}
		prepared, err := service.Prepare(context.Background(), "im.sdk.message.send", input, "review")
		if err != nil {
			t.Fatal(err)
		}
		prepared, err = service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, prepared.Operation.ID)
	}
	return service, ids
}

func reviewWaitPending(t *testing.T, repo workRepository, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		heads, err := repo.pendingHeads()
		if err != nil {
			t.Fatal(err)
		}
		if len(heads) == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending count=%d want=%d", len(heads), count)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReviewFourSDKParentsExhaustScheduler(t *testing.T) {
	root := t.TempDir()
	gate := make(chan struct{})
	service, ids := reviewSDKService(t, root, gate, 2*time.Second, 4)
	sender := &reviewConcurrentSender{}
	scheduler := NewWorkScheduler(root)
	scheduler.RegisterCapabilityService(service)
	scheduler.RegisterOutbox(NewOutbox(root), sender, false)
	if err := scheduler.dispatchAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scheduler.active != 4 {
		t.Fatalf("active=%d", scheduler.active)
	}
	close(gate)
	reviewDrain(t, scheduler, 4)
	if sender.calls.Load() != 4 {
		t.Fatalf("sends=%d", sender.calls.Load())
	}
	for _, id := range ids {
		view, err := service.Status(id)
		if err != nil || view.Status != OperationSucceeded {
			t.Fatalf("status=%#v err=%v", view, err)
		}
	}
	reviewAssertNoChildWork(t, root, "outbox")
}

func TestReviewQueuedChildChecksExecutionBoundary(t *testing.T) {
	for _, mode := range []string{"outbound-disabled", "policy-disabled", "dry-run-enabled", "parent-timed-out", "input-changed"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			gate := make(chan struct{})
			service, ids := reviewSDKService(t, root, gate, 2*time.Second, 1)
			sender := &reviewConcurrentSender{}
			scheduler := NewWorkScheduler(root)
			scheduler.RegisterCapabilityService(service)
			scheduler.RegisterOutbox(NewOutbox(root), sender, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := scheduler.dispatchAvailable(ctx); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				view, err := service.Status(ids[0])
				if err != nil {
					t.Fatal(err)
				}
				if view.Status == OperationRunning {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("operation not claimed")
				}
				time.Sleep(time.Millisecond)
			}
			switch mode {
			case "outbound-disabled", "dry-run-enabled":
				settings, err := NewSettingsStore(root).Load()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "outbound-disabled" {
					settings.Outbound.Enabled = false
				} else {
					settings.Outbound.DryRun = true
				}
				if err := NewSettingsStore(root).Save(settings); err != nil {
					t.Fatal(err)
				}
			case "policy-disabled":
				policy, err := service.ReadPolicy()
				if err != nil {
					t.Fatal(err)
				}
				policy.CapabilityOverrides["im.sdk.message.send"] = CapabilityDisabled
				if _, err := service.UpdatePolicy(policy, policy.Revision); err != nil {
					t.Fatal(err)
				}
			case "parent-timed-out":
				cancel()
			case "input-changed":
				if err := service.operations.update(ids[0], func(record *OperationRecord) error { record.InputFingerprint = "tampered"; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			close(gate)
			reviewDrain(t, scheduler, 1)
			if sender.calls.Load() != 0 {
				t.Fatalf("sent after %s", mode)
			}
			reviewAssertNoChildWork(t, root, "outbox")
		})
	}
}

type reviewConcurrentSender struct{ calls atomic.Int32 }

func (sender *reviewConcurrentSender) Send(context.Context, MessageTarget, string, string, string) (string, error) {
	sender.calls.Add(1)
	return "om_fixture", nil
}

type reviewDocumentTransport struct{ writes atomic.Int32 }

func (transport *reviewDocumentTransport) DocumentCreate(context.Context, DocumentRequest) (map[string]any, error) {
	transport.writes.Add(1)
	return map[string]any{"file_token": "doc_fixture"}, nil
}
func (transport *reviewDocumentTransport) DocumentFetch(context.Context, DocumentTarget) (map[string]any, error) {
	return map[string]any{"file_token": "doc_fixture"}, nil
}
func (transport *reviewDocumentTransport) DocumentVersion(context.Context, DocumentTarget, map[string]any, string) (map[string]any, error) {
	return map[string]any{"version": "1"}, nil
}
func (transport *reviewDocumentTransport) DocumentUpdate(context.Context, DocumentRequest) (map[string]any, error) {
	transport.writes.Add(1)
	return map[string]any{"revision": "2"}, nil
}

func reviewDrain(t *testing.T, scheduler *WorkScheduler, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		select {
		case completion := <-scheduler.complete:
			scheduler.release(completion.item)
			if completion.err != nil {
				t.Fatal(completion.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("scheduler work did not finish")
		}
	}
}
func reviewAssertNoChildWork(t *testing.T, root, kind string) {
	t.Helper()
	repo := newWorkRepository(root, kind)
	for _, state := range []string{"pending", "running", "terminal"} {
		entries, err := os.ReadDir(repo.stateDir(state))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("nested %s/%s work remains: %d", kind, state, len(entries))
		}
	}
}
func reviewRunningBoundary(t *testing.T, root, capabilityID string, input map[string]any) (context.Context, string) {
	t.Helper()
	settings := enableCapabilityWrites(t, root)
	settings.Outbound.Enabled, settings.Outbound.DryRun = true, false
	settings.Docbox.Enabled, settings.Docbox.DryRun = true, false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	operations := NewOperationService(root, NewCapabilityPolicyStore(root), nil)
	definition, _ := CapabilityByID(capabilityID)
	view, challenge, err := operations.Prepare(context.Background(), definition, input, "review")
	if err != nil {
		t.Fatal(err)
	}
	if challenge != "" {
		view, err = operations.Confirm(context.Background(), view.ID, challenge)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := operations.ClaimExecution(view.ID, capabilityID, input); err != nil {
		t.Fatal(err)
	}
	return context.WithValue(context.Background(), executionBoundaryKey{}, executionBoundary{operations: operations, operationID: view.ID, capabilityID: capabilityID, input: input}), view.ID
}
