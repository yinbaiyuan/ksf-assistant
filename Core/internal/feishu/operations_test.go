package feishu

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOperationSummaryIsUsefulButDoesNotExposePrivateInput(t *testing.T) {
	root := t.TempDir()
	service := NewOperationService(root, NewCapabilityPolicyStore(root), time.Now)
	definition := CapabilityDefinition{ID: "im.sdk.message.send", Domain: "im", Risk: "high-impact-write", Effect: "send", Reversibility: "irreversible"}
	view, _, err := service.Prepare(context.Background(), definition, map[string]any{
		"target-type": "open_id", "target-id": "ou_private_target", "text": "private message body",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view.Summary, "send") || !strings.Contains(view.Summary, "open_id:") {
		t.Fatalf("summary is not useful enough: %q", view.Summary)
	}
	if strings.Contains(view.Summary, "ou_private_target") || strings.Contains(view.Summary, "private message body") {
		t.Fatalf("summary exposed private input: %q", view.Summary)
	}
}

func TestOperationConfirmationIsBoundSingleUseAndExpires(t *testing.T) {
	root := t.TempDir()
	policyStore := NewCapabilityPolicyStore(root)
	policy := DefaultCapabilityPolicy()
	policy.CapabilityOverrides["test.remove"] = CapabilityConfirmEach
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	service := NewOperationService(root, policyStore, func() time.Time { return clock })
	definition := CapabilityDefinition{ID: "test.remove", Domain: "test", Risk: "destructive", Effect: "delete", Reversibility: "hard-delete"}
	prepared, token, err := service.Prepare(context.Background(), definition, map[string]any{"target": "doc_private"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Status != OperationAwaitingConfirmation || token == "" || prepared.NextAction != "confirm" {
		t.Fatalf("unexpected prepared operation: %#v token=%q", prepared, token)
	}
	confirmed, err := service.Confirm(context.Background(), prepared.ID, token)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != OperationQueued {
		t.Fatalf("confirmed status = %s", confirmed.Status)
	}
	if _, err := service.Confirm(context.Background(), prepared.ID, token); err == nil {
		t.Fatal("confirmation token was accepted twice")
	}

	prepared, token, err = service.Prepare(context.Background(), definition, map[string]any{"target": "doc_other"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(6 * time.Minute)
	expired, err := service.Confirm(context.Background(), prepared.ID, token)
	if err == nil {
		t.Fatal("expired confirmation was accepted")
	}
	if expired.Status != OperationExpired || expired.NextAction != "reprepare_on_user_request" {
		t.Fatalf("expired operation = %#v", expired)
	}
}

func TestOperationConfirmationRejectsPolicyChangeAndTimeoutDoesNotReplay(t *testing.T) {
	root := t.TempDir()
	policyStore := NewCapabilityPolicyStore(root)
	clock := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	service := NewOperationService(root, policyStore, func() time.Time { return clock })
	definition := CapabilityDefinition{ID: "test.remote", Domain: "test", Risk: "remote-operation", Effect: "remote", Reversibility: "unknown"}
	prepared, token, err := service.Prepare(context.Background(), definition, map[string]any{"target": "remote_private"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := policyStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides[definition.ID] = CapabilityDisabled
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	stale, err := service.Confirm(context.Background(), prepared.ID, token)
	if err == nil || stale.Status != OperationFailed || stale.NextAction != "reprepare_on_user_request" {
		t.Fatalf("policy change result = %#v err=%v", stale, err)
	}

	policy, _ = policyStore.Load()
	policy.CapabilityOverrides[definition.ID] = CapabilityAllowed
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	queued, _, err := service.Prepare(context.Background(), definition, map[string]any{"target": "remote_private"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != OperationQueued {
		t.Fatalf("allowed operation status = %s", queued.Status)
	}
	unknown, err := service.MarkOutcomeUnknown(queued.ID, "remote_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Status != OperationOutcomeUnknown || unknown.NextAction != "manual_review" || unknown.AttemptCount != 1 {
		t.Fatalf("unknown operation = %#v", unknown)
	}
	again, err := service.MarkOutcomeUnknown(queued.ID, "remote_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if again.AttemptCount != 1 {
		t.Fatalf("timeout replayed operation: attempts=%d", again.AttemptCount)
	}
}

func TestOperationStatusExpiresConfirmationWithoutReturningAnError(t *testing.T) {
	root := t.TempDir()
	policyStore := NewCapabilityPolicyStore(root)
	clock := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	service := NewOperationService(root, policyStore, func() time.Time { return clock })
	definition := CapabilityDefinition{ID: "test.send", Domain: "test", Risk: "high-impact-write", Effect: "send", Reversibility: "irreversible"}
	prepared, _, err := service.Prepare(context.Background(), definition, map[string]any{"body": "private"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(6 * time.Minute)
	view, err := service.Status(prepared.ID)
	if err != nil {
		t.Fatalf("expired status must remain queryable: %v", err)
	}
	if view.Status != OperationExpired || view.NextAction != "reprepare_on_user_request" || view.ErrorCode != "confirmation_expired" {
		t.Fatalf("unexpected expired view: %#v", view)
	}
	record, err := service.load(prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Input != nil || record.ChallengeHash != "" {
		t.Fatal("expired operation retained private request or challenge")
	}
}

func TestUnknownOutcomeStopsAutomaticVerificationAfterBoundedAttempts(t *testing.T) {
	root := t.TempDir()
	policyStore := NewCapabilityPolicyStore(root)
	policy, err := policyStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides["test.send"] = CapabilityAllowed
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	service := NewOperationService(root, policyStore, time.Now)
	definition := CapabilityDefinition{ID: "test.send", Domain: "test", Risk: "high-impact-write", Effect: "send", Reversibility: "irreversible"}
	prepared, _, err := service.Prepare(context.Background(), definition, map[string]any{"body": "private"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MarkOutcomeUnknown(prepared.ID, "remote_timeout"); err != nil {
		t.Fatal(err)
	}
	var view OperationView
	for index := 0; index < 3; index++ {
		if _, err := service.BeginReconciliation(prepared.ID); err != nil {
			t.Fatal(err)
		}
		view, err = service.ReconciliationFailed(prepared.ID, "verification_inconclusive")
		if err != nil {
			t.Fatal(err)
		}
	}
	if view.Status != OperationOutcomeUnknown || view.NextAction != "manual_review" {
		t.Fatalf("bounded reconciliation result = %#v", view)
	}
	if _, err := service.BeginReconciliation(prepared.ID); err == nil {
		t.Fatal("operation remained automatically reconcilable after the attempt limit")
	}
}

func TestResolvedReconciliationPreservesWriteResultAndCompatibilityFields(t *testing.T) {
	root := t.TempDir()
	service := NewOperationService(root, NewCapabilityPolicyStore(root), time.Now)
	definition, _ := CapabilityByID("im.chat.create")
	input := map[string]any{"name": "result preservation"}
	prepared, _, err := service.Prepare(context.Background(), definition, input, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimExecution(prepared.ID, definition.ID, input); err != nil {
		t.Fatal(err)
	}
	partial := map[string]any{"capabilityId": definition.ID, "response": map[string]any{"chat_id": "oc_private"}, "verified": false}
	if _, err := service.MarkOutcomeUnknownWithResult(prepared.ID, "verification_outcome_unknown", partial); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginReconciliation(prepared.ID); err != nil {
		t.Fatal(err)
	}
	view, err := service.ResolveReconciliation(prepared.ID, VerificationAssessment{State: VerificationConfirmed, Evidence: map[string]any{"status": "present"}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != OperationSucceeded {
		t.Fatalf("resolved view = %#v", view)
	}
	record, err := service.load(prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Result["response"] == nil || record.Result["verified"] != true || record.Result["verificationState"] != string(VerificationConfirmed) || record.Result["verification"] == nil {
		t.Fatalf("resolved result = %#v", record.Result)
	}
}

func TestUnknownOutcomeWithoutRereadRequiresManualReview(t *testing.T) {
	root := t.TempDir()
	policyStore := NewCapabilityPolicyStore(root)
	policy, _ := policyStore.Load()
	policy.CapabilityOverrides["im.sdk.message.send"] = CapabilityAllowed
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	definition, _ := CapabilityByID("im.sdk.message.send")
	service := NewOperationService(root, policyStore, time.Now)
	prepared, _, err := service.Prepare(context.Background(), definition, map[string]any{
		"request-id": "OUT-unknown-send", "target-type": "open_id", "target-id": "ou_private",
		"format": "text", "text": "private", "source": "test",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.MarkOutcomeUnknown(prepared.ID, "write_outcome_unknown")
	if err != nil || view.NextAction != "manual_review" {
		t.Fatalf("unknown send view=%#v err=%v", view, err)
	}
	record, err := service.load(prepared.ID)
	if err != nil || record.Input != nil {
		t.Fatalf("manual review retained private input: %#v err=%v", record.Input, err)
	}
}

func TestInterruptedExecutionRecoversAsUnknownWithoutReplay(t *testing.T) {
	root := t.TempDir()
	policyStore := NewCapabilityPolicyStore(root)
	definition, _ := CapabilityByID("im.chat.create")
	service := NewOperationService(root, policyStore, time.Now)
	prepared, _, err := service.Prepare(context.Background(), definition, map[string]any{"name": "interrupted"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimExecution(prepared.ID, definition.ID, map[string]any{"name": "interrupted"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverInterrupted(10); err != nil {
		t.Fatal(err)
	}
	view, err := service.Status(prepared.ID)
	if err != nil || view.Status != OperationOutcomeUnknown || view.AttemptCount != 1 {
		t.Fatalf("recovered view=%#v err=%v", view, err)
	}
	again, err := service.MarkOutcomeUnknown(prepared.ID, "execution_interrupted")
	if err != nil || again.AttemptCount != 1 {
		t.Fatalf("recovery replayed operation: %#v err=%v", again, err)
	}
}
