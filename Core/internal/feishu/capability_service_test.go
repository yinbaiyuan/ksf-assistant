package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type recordingCapabilityServiceExecutor struct {
	action          recordingActionExecutor
	verification    VerificationAssessment
	verificationErr error
}

func enableCapabilityWrites(t *testing.T, root string) Settings {
	t.Helper()
	settings := DefaultSettings()
	settings.Actionbox.Enabled = true
	settings.Actionbox.DryRun = false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestCapabilityServiceDoesNotAllowDestructivePolicyToBypassConfirmationOrGuards(t *testing.T) {
	root := t.TempDir()
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	policy, err := service.ReadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	policy.RiskDefaults["destructive"] = CapabilityAllowed
	if _, err := service.UpdatePolicy(policy, policy.Revision); !errors.Is(err, ErrUnsafeDestructivePolicy) {
		t.Fatalf("destructive allowed policy accepted: %v", err)
	}

	policy, _ = service.ReadPolicy()
	policy.RiskDefaults["destructive"] = CapabilityConfirmEach
	if _, err := service.UpdatePolicy(policy, policy.Revision); !errors.Is(err, ErrUnsafeDestructivePolicy) {
		t.Fatalf("bulk destructive confirmation policy accepted: %v", err)
	}

	policy, _ = service.ReadPolicy()
	policy.CapabilityOverrides["base.shortcut.app.page.delete"] = CapabilityConfirmEach
	updated, err := service.UpdatePolicy(policy, policy.Revision)
	if err != nil {
		t.Fatalf("bounded destructive override rejected: %v", err)
	}
	definition, _ := CapabilityByID("base.shortcut.app.page.delete")
	if updated.Decision(definition) != CapabilityConfirmEach || definition.GuardProfile != CapabilityGuardBounded {
		t.Fatalf("bounded destructive governance = policy:%s definition:%#v", updated.Decision(definition), definition)
	}
}

func TestApprovalDestructiveCapabilityCanOnlyBeOpenedIndividuallyWithFixedGuards(t *testing.T) {
	root := t.TempDir()
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	policy, err := service.ReadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides["approval.instances.cancel"] = CapabilityConfirmEach
	updated, err := service.UpdatePolicy(policy, policy.Revision)
	if err != nil {
		t.Fatalf("guarded approval cancellation override rejected: %v", err)
	}
	definition, _ := CapabilityByID("approval.instances.cancel")
	if updated.Decision(definition) != CapabilityConfirmEach {
		t.Fatalf("approval cancellation policy = %s", updated.Decision(definition))
	}
}

func TestEveryPublishedDestructiveCapabilityCanBeOpenedOnlyAsConfirmEach(t *testing.T) {
	root := t.TempDir()
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	policy, err := service.ReadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range manifest.Capabilities {
		if CapabilityPublished(definition) && definition.Risk == "destructive" {
			policy.CapabilityOverrides[definition.ID] = CapabilityConfirmEach
			count++
		}
	}
	updated, err := service.UpdatePolicy(policy, policy.Revision)
	if err != nil {
		t.Fatalf("one or more destructive capabilities cannot be individually enabled: %v", err)
	}
	if count != 101 {
		t.Fatalf("destructive override count = %d", count)
	}
	for id := range policy.CapabilityOverrides {
		definition, _ := CapabilityByID(id)
		if definition.Risk == "destructive" && updated.Decision(definition) != CapabilityConfirmEach {
			t.Fatalf("%s did not retain confirm_each", id)
		}
	}

	policy = updated
	policy.CapabilityOverrides["approval.instances.cancel"] = CapabilityAllowed
	if _, err := service.UpdatePolicy(policy, policy.Revision); !errors.Is(err, ErrUnsafeDestructivePolicy) {
		t.Fatalf("destructive allowed override accepted: %v", err)
	}
}

func (executor *recordingCapabilityServiceExecutor) ReadPreflight(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	return map[string]any{"revision": "v1"}, nil
}

func (executor *recordingCapabilityServiceExecutor) ExecuteWithOptions(ctx context.Context, capabilityID string, input map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	return executor.action.ExecuteWithOptions(ctx, capabilityID, input, options)
}

func (executor *recordingCapabilityServiceExecutor) ReadVerification(_ context.Context, _ string, _ map[string]any, _ map[string]any) (VerificationAssessment, error) {
	if executor.verificationErr != nil {
		return VerificationAssessment{}, executor.verificationErr
	}
	if executor.verification.State == "" {
		return VerificationAssessment{State: VerificationInconclusive, Evidence: map[string]any{"observed": true}}, nil
	}
	return executor.verification, nil
}

func TestCapabilityServiceReconcilesUnknownWithoutReplayingSideEffect(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{
		action:       recordingActionExecutor{err: &CapabilityExecutionError{Phase: "verification", Err: errors.New("temporary reread failure")}},
		verification: VerificationAssessment{State: VerificationInconclusive, Evidence: map[string]any{"chat": "present"}},
	}
	service := NewCapabilityService(root, executor, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "reconcile"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Status(prepared.Operation.ID)
	if before.Status != OperationOutcomeUnknown {
		t.Fatalf("before reconcile: %#v", before)
	}
	if err := service.ReconcileUnknown(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	after, _ := service.Status(prepared.Operation.ID)
	if after.Status != OperationOutcomeUnknown || after.NextAction != "query_same_operation" || after.VerificationAttempts != 1 || executor.action.calls != 1 {
		t.Fatalf("after=%#v side-effect calls=%d", after, executor.action.calls)
	}
}

func TestCapabilityServiceAppliesExplicitReconciliationDecisions(t *testing.T) {
	tests := []struct {
		name      string
		state     VerificationState
		want      OperationStatus
		wantError string
	}{
		{name: "confirmed", state: VerificationConfirmed, want: OperationSucceeded},
		{name: "rejected", state: VerificationRejected, want: OperationFailed, wantError: "verification_rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			enableCapabilityWrites(t, root)
			executor := &recordingCapabilityServiceExecutor{
				action:       recordingActionExecutor{err: &CapabilityExecutionError{Phase: "verification", Err: errors.New("reread outcome unknown")}},
				verification: VerificationAssessment{State: test.state, Evidence: map[string]any{"observed": "terminal"}},
			}
			service := NewCapabilityService(root, executor, nil)
			prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": test.name}, "test")
			if err != nil {
				t.Fatal(err)
			}
			if err := service.ProcessActions(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := service.ReconcileUnknown(context.Background(), 10); err != nil {
				t.Fatal(err)
			}
			view, err := service.Status(prepared.Operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Status != test.want || view.ErrorCode != test.wantError || executor.action.calls != 1 {
				t.Fatalf("view=%#v side-effect calls=%d", view, executor.action.calls)
			}
		})
	}
}

func TestCapabilityServiceReconciliationQueryFailureStaysUnknown(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{
		action:          recordingActionExecutor{err: &CapabilityExecutionError{Phase: "verification", Err: errors.New("initial reread timeout")}},
		verificationErr: errors.New("verification query unavailable"),
	}
	service := NewCapabilityService(root, executor, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "query failure"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileUnknown(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	view, err := service.Status(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != OperationOutcomeUnknown || view.NextAction != "query_same_operation" || view.VerificationAttempts != 1 || executor.action.calls != 1 {
		t.Fatalf("view=%#v side-effect calls=%d", view, executor.action.calls)
	}
}

func TestCapabilityServiceStopsAfterThreeInconclusiveObservations(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{
		action:       recordingActionExecutor{err: &CapabilityExecutionError{Phase: "verification", Err: errors.New("initial reread timeout")}},
		verification: VerificationAssessment{State: VerificationInconclusive, Evidence: map[string]any{"observed": true}},
	}
	service := NewCapabilityService(root, executor, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "manual review"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := service.ReconcileUnknown(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
	}
	view, err := service.Status(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != OperationOutcomeUnknown || view.NextAction != "manual_review" || view.VerificationAttempts != 3 || executor.action.calls != 1 {
		t.Fatalf("view=%#v side-effect calls=%d", view, executor.action.calls)
	}
}

func TestCapabilityServiceKeepsDocumentWriteUnknownWhenPostWriteRereadFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	root := t.TempDir()
	settings := enableCapabilityWrites(t, root)
	settings.Docbox.Enabled = true
	settings.Docbox.DryRun = false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	fetchCount := filepath.Join(root, "fetch-count")
	callLog := filepath.Join(root, "calls")
	bin := filepath.Join(root, "fake-lark")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + callLog + `"
case "$*" in
 *+fetch*)
   count=0
   if [ -f "` + fetchCount + `" ]; then count=$(cat "` + fetchCount + `"); fi
   count=$((count + 1))
   printf '%s' "$count" > "` + fetchCount + `"
   if [ "$count" -ge 4 ]; then
     printf 'post-write reread timed out\n' >&2
     exit 1
   fi
   printf '{"data":{"file_token":"doc_test"}}\n'
   ;;
 *versions*) printf '{"data":{"version_id":"v1"}}\n' ;;
 *+update*) printf '{"data":{"revision_id":"2"}}\n' ;;
 *) printf '{}\n' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root}
	service := NewCapabilityService(root, UnifiedCapabilityExecutor{LongTail: raw, DataRoot: root}, nil)
	prepared, err := service.Prepare(context.Background(), "docs.service.document.append", map[string]any{
		"target-kind": "docx_token", "target-value": "doc_test", "content": "new", "format": "markdown", "source": "test",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		box := NewDocbox(root)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			_ = box.Process(workerCtx, raw, false)
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	if err := service.ProcessActions(context.Background()); err != nil {
		cancelWorker()
		<-workerDone
		t.Fatal(err)
	}
	cancelWorker()
	<-workerDone

	view, err := service.Status(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != OperationOutcomeUnknown || view.NextAction != "query_same_operation" {
		t.Fatalf("document operation = %#v", view)
	}
	record, err := service.operations.load(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Result == nil || record.Result["verificationState"] != string(VerificationInconclusive) {
		t.Fatalf("document write evidence was not retained: %#v", record.Result)
	}
	if err := service.ReconcileUnknown(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "docs +update") != 1 {
		t.Fatalf("document write was replayed: %q", data)
	}
	after, err := service.Status(prepared.Operation.ID)
	if err != nil || after.Status != OperationOutcomeUnknown || after.VerificationAttempts != 1 {
		t.Fatalf("post-reconciliation operation=%#v err=%v", after, err)
	}
}

func TestCapabilityServiceQueuesAllowedWriteThroughGovernedActionbox(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{}
	service := NewCapabilityService(root, executor, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "service test"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Operation.Status != OperationQueued || prepared.Challenge != "" || !prepared.Submitted {
		t.Fatalf("unexpected prepare result: %#v", prepared)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != OperationSucceeded || executor.action.calls != 1 {
		t.Fatalf("status=%#v calls=%d", status, executor.action.calls)
	}
}

func TestCapabilityServiceHighImpactRequiresChallengeBeforeQueueing(t *testing.T) {
	root := t.TempDir()
	settings := enableCapabilityWrites(t, root)
	settings.Outbound.Enabled = true
	settings.Outbound.DryRun = false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	input := map[string]any{
		"request-id": "OUT-confirm-test", "target-type": "open_id", "target-id": "ou_private_123",
		"format": "text", "text": "private body", "source": "test",
	}
	prepared, err := service.Prepare(context.Background(), "im.sdk.message.send", input, "test")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Operation.Status != OperationAwaitingConfirmation || prepared.Challenge == "" || prepared.Submitted {
		t.Fatalf("unexpected prepare result: %#v", prepared)
	}
	confirmed, err := service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Operation.Status != OperationQueued || !confirmed.Submitted {
		t.Fatalf("unexpected confirm result: %#v", confirmed)
	}
}

func TestApprovalDecisionUsesTheSameConfirmationAndQueueLifecycle(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{}
	service := NewCapabilityService(root, executor, nil)
	input := map[string]any{"data": map[string]any{"instance_code": "instance_1", "task_id": "task_1", "comment": "同意"}}
	prepared, err := service.Prepare(context.Background(), "approval.tasks.approve", input, "test")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Operation.Status != OperationAwaitingConfirmation || prepared.Challenge == "" || prepared.Submitted {
		t.Fatalf("approval decision skipped confirmation: %#v", prepared)
	}
	confirmed, err := service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge)
	if err != nil || !confirmed.Submitted || confirmed.Operation.Status != OperationQueued {
		t.Fatalf("approval decision was not queued: %#v err=%v", confirmed, err)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := service.Status(prepared.Operation.ID)
	if err != nil || view.Status != OperationSucceeded || executor.action.calls != 1 {
		t.Fatalf("approval decision lifecycle: view=%#v calls=%d err=%v", view, executor.action.calls, err)
	}
}

func TestCapabilityServiceEnforcesOutboundGateAndGlobalDryRun(t *testing.T) {
	root := t.TempDir()
	settings := enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{}
	service := NewCapabilityService(root, executor, nil)
	input := map[string]any{
		"request-id": "OUT-service-gate", "target-type": "open_id", "target-id": "ou_private_123",
		"format": "text", "text": "private body", "source": "test",
	}
	if _, err := service.Prepare(context.Background(), "im.sdk.message.send", input, "test"); err == nil || err.Error() != "outbound_disabled" {
		t.Fatalf("disabled outbound gate result = %v", err)
	}

	settings.Outbound.Enabled = true
	settings.Outbound.DryRun = true
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	policy, err := service.ReadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides["im.sdk.message.send"] = CapabilityAllowed
	if _, err := service.UpdatePolicy(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	prepared, err := service.Prepare(context.Background(), "im.sdk.message.send", input, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != OperationSucceeded || executor.action.calls != 0 {
		t.Fatalf("dry-run status=%#v side-effect calls=%d", status, executor.action.calls)
	}
}

func TestCapabilityServiceKeepsDocboxBehindGovernanceAndItsOwnGate(t *testing.T) {
	root := t.TempDir()
	settings := enableCapabilityWrites(t, root)
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	input := map[string]any{"doc": "https://example.feishu.cn/docx/private", "content": "graph TD; A-->B", "doc-format": "mermaid"}
	if _, err := service.Prepare(context.Background(), "docs.whiteboard.insert", input, "test"); err == nil || err.Error() != "docbox_disabled" {
		t.Fatalf("disabled docbox gate result = %v", err)
	}

	settings.Docbox.Enabled = true
	settings.Docbox.DryRun = true
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	prepared, err := service.Prepare(context.Background(), "docs.whiteboard.insert", input, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Submitted {
		t.Fatalf("docbox capability did not enter governed queue: %#v", prepared)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := service.Status(prepared.Operation.ID)
	if err != nil || view.Status != OperationSucceeded {
		t.Fatalf("docbox dry-run view=%#v err=%v", view, err)
	}
}

func TestCapabilityServiceErrorsExposeStableAgentAdvice(t *testing.T) {
	cases := map[string]CapabilityServiceAdvice{
		"outbound_disabled":                   {ErrorCode: "outbound_disabled", NextAction: "enable_outbound"},
		"confirmation_expired":                {ErrorCode: "confirmation_expired", NextAction: "reprepare_on_user_request"},
		"capability_policy_revision_conflict": {ErrorCode: "capability_policy_revision_conflict", NextAction: "reread_policy"},
		"missing_required:target-id":          {ErrorCode: "invalid_capability_input", NextAction: "fix_request"},
	}
	for input, expected := range cases {
		if actual := CapabilityServiceErrorAdvice(errors.New(input)); actual != expected {
			t.Fatalf("%s advice = %#v, want %#v", input, actual, expected)
		}
	}
}

func TestCapabilityServiceRechecksBaseGatesImmediatelyBeforeExecution(t *testing.T) {
	root := t.TempDir()
	settings := enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{}
	service := NewCapabilityService(root, executor, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "queued"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	settings.Actionbox.Enabled = false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := service.Status(prepared.Operation.ID)
	if err != nil || view.Status != OperationFailed || view.ErrorCode != "actionbox_disabled" || executor.action.calls != 0 {
		t.Fatalf("execution gate view=%#v calls=%d err=%v", view, executor.action.calls, err)
	}
}
