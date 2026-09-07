package feishu

import (
	"context"
	"errors"
	"testing"
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
	if count != 100 {
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

func TestRetiredProductSwitchesCannotSimulateSuccessfulBusiness(t *testing.T) {
	root := t.TempDir()
	settings := DefaultSettings()
	settings.Outbound = DryRunSwitch{Enabled: false, DryRun: true}
	settings.Actionbox = DryRunSwitch{Enabled: false, DryRun: true}
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewSettingsStore(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := CapabilityByID("im.sdk.message.send")
	if err := validateCapabilityRuntimeGate(definition, loaded); err != nil || effectiveCapabilityDryRun(definition, loaded) {
		t.Fatal("retired settings affect execution")
	}
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	prepared, err := service.Prepare(context.Background(), "im.sdk.message.send", map[string]any{"request-id": "OUT-fixture", "target-type": "open_id", "target-id": "ou_fixture", "format": "text", "text": "fixture", "source": "test"}, "test")
	if err != nil || prepared.Operation.Status != OperationAwaitingConfirmation {
		t.Fatalf("approval boundary lost: %+v %v", prepared, err)
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
	enableCapabilityWrites(t, root)
	executor := &recordingCapabilityServiceExecutor{}
	service := NewCapabilityService(root, executor, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "queued"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := service.ReadPolicy()
	policy.CapabilityOverrides["im.chat.create"] = CapabilityDisabled
	if _, err := service.UpdatePolicy(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := service.Status(prepared.Operation.ID)
	if err != nil || view.Status != OperationFailed || executor.action.calls != 0 {
		t.Fatalf("execution gate view=%#v calls=%d err=%v", view, executor.action.calls, err)
	}
}
