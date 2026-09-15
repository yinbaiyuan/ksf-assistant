package feishu

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrUnsafeDestructivePolicy = errors.New("unsafe_destructive_policy")

type CapabilityServiceExecutor interface {
	actionExecutor
	ReadPreflight(context.Context, string, map[string]any) (map[string]any, error)
	ReadVerification(context.Context, string, map[string]any, map[string]any) (VerificationAssessment, error)
}

type VerificationState string

const (
	VerificationConfirmed    VerificationState = "confirmed"
	VerificationRejected     VerificationState = "rejected"
	VerificationInconclusive VerificationState = "inconclusive"
)

// VerificationAssessment separates collecting post-write evidence from proving
// the mutation's outcome. A successful reread is inconclusive unless a reviewed
// capability-specific verifier explicitly confirms or rejects the postcondition.
type VerificationAssessment struct {
	State    VerificationState `json:"state"`
	Evidence map[string]any    `json:"evidence,omitempty"`
}

type PreparedOperation struct {
	Operation  OperationView  `json:"operation"`
	Challenge  string         `json:"challenge,omitempty"`
	Submitted  bool           `json:"submitted"`
	Result     map[string]any `json:"result,omitempty"`
	ErrorCode  string         `json:"errorCode,omitempty"`
	NextAction string         `json:"nextAction,omitempty"`
}

type CapabilityServiceAdvice struct {
	ErrorCode  string `json:"errorCode"`
	NextAction string `json:"nextAction"`
}

func CapabilityServiceErrorAdvice(err error) CapabilityServiceAdvice {
	if err == nil {
		return CapabilityServiceAdvice{}
	}
	code := strings.TrimSpace(err.Error())
	switch code {
	case "outbound_disabled":
		return CapabilityServiceAdvice{ErrorCode: code, NextAction: "enable_outbound"}
	case "actionbox_disabled":
		return CapabilityServiceAdvice{ErrorCode: code, NextAction: "enable_actionbox"}
	case "confirmation_expired", "confirmation_invalid", "confirmation_not_pending", "preflight_changed", "capability_policy_changed":
		return CapabilityServiceAdvice{ErrorCode: code, NextAction: "reprepare_on_user_request"}
	case ErrCapabilityPolicyRevisionConflict.Error():
		return CapabilityServiceAdvice{ErrorCode: code, NextAction: "reread_policy"}
	case "capability_disabled", "capability_not_published", "unknown_capability", "unknown_capability_override":
		return CapabilityServiceAdvice{ErrorCode: code, NextAction: "stop"}
	}
	for _, prefix := range []string{"missing_required:", "invalid_", "unsupported_", "input_too_large:", "unsafe_path:", "requires_", "mutually_exclusive:"} {
		if strings.HasPrefix(code, prefix) {
			return CapabilityServiceAdvice{ErrorCode: "invalid_capability_input", NextAction: "fix_request"}
		}
	}
	return CapabilityServiceAdvice{ErrorCode: "capability_service_failed", NextAction: "stop"}
}

type CapabilityService struct {
	dataRoot   string
	policy     CapabilityPolicyStore
	operations *OperationService
	actionbox  *Actionbox
	executor   CapabilityServiceExecutor
	transport  *ServiceTransport
}

func NewCapabilityService(dataRoot string, executor CapabilityServiceExecutor, now func() time.Time) *CapabilityService {
	policy := NewCapabilityPolicyStore(dataRoot)
	operations := NewOperationService(dataRoot, policy, now)
	return &CapabilityService{
		dataRoot: dataRoot, policy: policy, operations: operations,
		actionbox: NewGovernedActionbox(dataRoot, operations), executor: executor,
	}
}

func (service *CapabilityService) Prepare(ctx context.Context, capabilityID string, input map[string]any, source string) (PreparedOperation, error) {
	if input["dry-run"] == true {
		return PreparedOperation{}, errors.New("product_preview_retired")
	}
	definition, ok := CapabilityByID(capabilityID)
	if !ok {
		return PreparedOperation{}, errors.New("unknown_capability")
	}
	if !CapabilityPublished(definition) {
		return PreparedOperation{}, errors.New("capability_not_published")
	}
	settings, err := NewSettingsStore(service.dataRoot).Load()
	if err != nil {
		return PreparedOperation{}, err
	}
	if err := validateCapabilityRuntimeGate(definition, settings); err != nil {
		return PreparedOperation{}, err
	}
	if err := validateCapabilityInput(definition, input); err != nil {
		return PreparedOperation{}, err
	}
	var preflight map[string]any
	if definition.Preflight != nil {
		preflight, err = service.executor.ReadPreflight(ctx, capabilityID, input)
		if err != nil {
			return PreparedOperation{}, err
		}
	}
	view, challenge, err := service.operations.PrepareWithEvidence(definition, input, source, preflight)
	if err != nil {
		return PreparedOperation{}, err
	}
	_ = service.auditGovernance("operation_prepared", view)
	prepared := PreparedOperation{Operation: view, Challenge: challenge}
	if view.Status == OperationAwaitingConfirmation && definition.Identity == "user" && definition.Risk != "read" {
		if executor, ok := service.executor.(interface{ DesktopApprovalEnabled() bool }); ok && executor.DesktopApprovalEnabled() {
			return service.confirm(ctx, view.ID, challenge, true)
		}
	}
	if view.Status == OperationQueued {
		return service.dispatch(ctx, prepared)
	}
	return prepared, nil
}

func (service *CapabilityService) Confirm(ctx context.Context, id, challenge string) (PreparedOperation, error) {
	return service.confirm(ctx, id, challenge, false)
}

// The desktop confirmation channel is only for bridge-owned messages/cards.
// It must never resume an old Agent business operation after middleware removal.
func (service *CapabilityService) ConfirmServiceMessage(ctx context.Context, id, challenge string) (PreparedOperation, error) {
	record, err := service.operations.Request(id)
	if err != nil {
		return PreparedOperation{}, err
	}
	if record.InputProfile != serviceMessageInputProfile {
		return PreparedOperation{}, errors.New("agent_feishu_middleware_removed")
	}
	return service.confirm(ctx, id, challenge, false)
}

func (service *CapabilityService) confirm(ctx context.Context, id, challenge string, desktopPending bool) (PreparedOperation, error) {
	record, err := service.operations.Request(id)
	if err != nil {
		return PreparedOperation{}, err
	}
	definition, ok := CapabilityByID(record.CapabilityID)
	if !ok || !CapabilityPublished(definition) {
		return PreparedOperation{}, errors.New("capability_not_published")
	}
	if record.InputProfile == serviceMessageInputProfile && service.transport == nil {
		return PreparedOperation{}, errors.New("service_message_transport_unavailable")
	}
	var preflight map[string]any
	if definition.Preflight != nil && record.InputProfile != serviceMessageInputProfile {
		preflight, err = service.executor.ReadPreflight(ctx, record.CapabilityID, record.Input)
		if err != nil {
			return PreparedOperation{}, err
		}
	}
	view, err := service.operations.ConfirmWithEvidence(id, challenge, preflight)
	if err != nil {
		if view.ID != "" {
			_ = service.auditGovernance("operation_confirmation_rejected", view)
		}
		return PreparedOperation{Operation: view}, err
	}
	event := "operation_confirmed"
	if desktopPending {
		event = "operation_waiting_desktop_approval"
	}
	_ = service.auditGovernance(event, view)
	if record.InputProfile == serviceMessageInputProfile {
		result, executeErr := service.transport.ResumeOperation(ctx, id)
		current, statusErr := service.operations.Status(id)
		return PreparedOperation{Operation: current, Submitted: true, Result: map[string]any{"messageID": result}}, errors.Join(executeErr, statusErr)
	}
	return service.dispatch(ctx, PreparedOperation{Operation: view})
}

func (service *CapabilityService) SetMessageTransport(transport *ServiceTransport) {
	service.transport = transport
}

func (service *CapabilityService) Cancel(id string) (OperationView, error) {
	view, err := service.operations.Cancel(id)
	if err == nil {
		_ = service.auditGovernance("operation_cancelled", view)
	}
	return view, err
}

func (service *CapabilityService) Status(id string) (OperationView, error) {
	return service.operations.Status(id)
}

func (service *CapabilityService) ReadPolicy() (CapabilityPolicy, error) {
	return service.policy.Load()
}

func (service *CapabilityService) UpdatePolicy(policy CapabilityPolicy, expectedRevision uint64) (CapabilityPolicy, error) {
	if value := policy.RiskDefaults["destructive"]; value != "" && value != CapabilityDisabled {
		return CapabilityPolicy{}, ErrUnsafeDestructivePolicy
	}
	current, err := service.policy.Load()
	if err != nil {
		return CapabilityPolicy{}, err
	}
	for capabilityID, permission := range policy.CapabilityOverrides {
		// Historical overrides may be carried forward or removed, never newly granted.
		if retiredDocumentPolicyID(capabilityID) && current.CapabilityOverrides[capabilityID] == permission {
			continue
		}
		// Managed command effects have policy identifiers without queue adapters.
		if capabilityID == "drive.file.version.create" {
			continue
		}
		if capabilityID == "docs.shortcut.overwrite" {
			if permission != CapabilityDisabled && permission != CapabilityConfirmEach {
				return CapabilityPolicy{}, ErrUnsafeDestructivePolicy
			}
			continue
		}
		definition, ok := CapabilityByID(capabilityID)
		if !ok {
			return CapabilityPolicy{}, errors.New("unknown_capability_override")
		}
		if !CapabilityPublished(definition) && permission != CapabilityDisabled {
			return CapabilityPolicy{}, errors.New("capability_not_published")
		}
		if definition.Risk == "destructive" && permission != CapabilityDisabled {
			if permission != CapabilityConfirmEach || (definition.GuardProfile != CapabilityGuardStrong && definition.GuardProfile != CapabilityGuardBounded) || definition.Postcondition == nil {
				return CapabilityPolicy{}, ErrUnsafeDestructivePolicy
			}
		}
	}
	saved, err := service.policy.Save(policy, expectedRevision)
	if err == nil {
		_ = NewAuditLog(service.dataRoot).Record("capability_policy_updated", map[string]any{"revision": saved.Revision, "overrideCount": len(saved.CapabilityOverrides)})
	}
	return saved, err
}

func (service *CapabilityService) ProcessActions(ctx context.Context) error {
	return service.actionbox.process(ctx, service.executor)
}

func (service *CapabilityService) ReconcileUnknown(ctx context.Context, limit int) error {
	records, err := service.operations.Unknown(limit)
	if err != nil {
		return err
	}
	for _, candidate := range records {
		definition, ok := CapabilityByID(candidate.CapabilityID)
		if !ok || definition.Reread == nil {
			continue
		}
		record, claimErr := service.operations.BeginReconciliation(candidate.ID)
		if claimErr != nil {
			continue
		}
		assessment, verifyErr := service.executor.ReadVerification(ctx, record.CapabilityID, record.Input, record.Result)
		if verifyErr != nil {
			_, _ = service.operations.ReconciliationFailed(record.ID, CapabilityOperationErrorCode(verifyErr))
			continue
		}
		view, resolveErr := service.operations.ResolveReconciliation(record.ID, assessment)
		if resolveErr == nil {
			_ = service.auditGovernance("operation_reconciliation_"+string(assessment.normalizedState()), view)
		}
	}
	return nil
}

func (assessment VerificationAssessment) normalizedState() VerificationState {
	switch assessment.State {
	case VerificationConfirmed, VerificationRejected, VerificationInconclusive:
		return assessment.State
	default:
		return VerificationInconclusive
	}
}

func (service *CapabilityService) ExpireAwaiting(limit int) error {
	return service.operations.ExpireAwaiting(limit)
}

func (service *CapabilityService) RecoverInterrupted(limit int) error {
	for _, kind := range []string{"actionbox", "outbox"} {
		repo := newWorkRepository(service.dataRoot, kind)
		if _, err := repo.recoverRunning(limit); err != nil {
			return err
		}
		if err := repo.reconcileTerminalOperations(); err != nil {
			return err
		}
	}
	return service.operations.RecoverInterrupted(limit)
}

func (service *CapabilityService) dispatch(ctx context.Context, prepared PreparedOperation) (PreparedOperation, error) {
	record, err := service.operations.Request(prepared.Operation.ID)
	if err != nil {
		return prepared, err
	}
	definition, ok := CapabilityByID(record.CapabilityID)
	if !ok {
		return prepared, errors.New("unknown_capability")
	}
	settings, err := NewSettingsStore(service.dataRoot).Load()
	if err != nil {
		return prepared, err
	}
	if err := validateCapabilityRuntimeGate(definition, settings); err != nil {
		_, _ = service.operations.Fail(record.ID, err.Error())
		return prepared, err
	}
	if definition.Risk == "read" {
		if _, err := service.operations.ClaimExecution(record.ID, record.CapabilityID, record.Input); err != nil {
			return prepared, err
		}
		result, runErr := service.executor.ExecuteWithOptions(ctx, record.CapabilityID, record.Input, CapabilityExecutionOptions{})
		if runErr != nil {
			prepared.Result = cliFailureResult(result, runErr)
			prepared.Operation, _ = service.operations.Fail(record.ID, CapabilityOperationErrorCode(runErr))
			return prepared, runErr
		}
		_, _ = service.operations.MarkVerifying(record.ID)
		prepared.Operation, err = service.operations.CompleteWithResult(record.ID, result)
		prepared.Result = result
		return prepared, err
	}
	actionID, err := NewActionID()
	if err != nil {
		return prepared, err
	}
	request := ActionRequest{
		ID: actionID, Type: "feishu_capability", Domain: "capability", Action: "execute",
		CapabilityID: definition.ID, OperationID: record.ID, Identity: definition.Identity,
		Input: record.Input, ExplicitAuthorization: true, Source: record.Source,
		DryRun: effectiveCapabilityDryRun(definition, settings) || record.Input["dry-run"] == true, CreatedAt: time.Now().UTC(),
	}
	if err := service.actionbox.Submit(request); err != nil {
		_, _ = service.operations.Fail(record.ID, err.Error())
		return prepared, err
	}
	prepared.Submitted = true
	return prepared, nil
}

func validateCapabilityRuntimeGate(_ CapabilityDefinition, _ Settings) error { return nil }
func effectiveCapabilityDryRun(_ CapabilityDefinition, _ Settings) bool      { return false }

func capabilityUsesOutbound(definition CapabilityDefinition) bool {
	return definition.Effect == "send" || contains([]string{"im.message.edit", "im.messages.patch"}, definition.ID)
}

func (service *CapabilityService) auditGovernance(direction string, view OperationView) error {
	return NewAuditLog(service.dataRoot).Record(direction, map[string]any{
		"operationId": view.ID, "capability": view.CapabilityID, "status": view.Status,
		"summary": view.Summary, "nextAction": view.NextAction, "errorCode": view.ErrorCode,
	})
}
