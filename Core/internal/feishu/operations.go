package feishu

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	OperationRecordVersion      = 1
	OperationConfirmationWindow = 5 * time.Minute
)

type OperationStatus string

const (
	OperationAwaitingConfirmation OperationStatus = "awaiting_confirmation"
	OperationQueued               OperationStatus = "queued"
	OperationRunning              OperationStatus = "running"
	OperationVerifying            OperationStatus = "verifying"
	OperationSucceeded            OperationStatus = "succeeded"
	OperationFailed               OperationStatus = "failed"
	OperationExpired              OperationStatus = "expired"
	OperationCancelled            OperationStatus = "cancelled"
	OperationOutcomeUnknown       OperationStatus = "outcome_unknown"
)

var operationIDPattern = regexp.MustCompile(`^OP-[0-9]{14}-[A-F0-9]{8}$`)

var ErrOperationRequestMismatch = errors.New("operation_request_mismatch")

type OperationRecord struct {
	Version              int                  `json:"version"`
	ID                   string               `json:"id"`
	CapabilityID         string               `json:"capabilityId"`
	Domain               string               `json:"domain"`
	Risk                 string               `json:"risk"`
	Effect               string               `json:"effect,omitempty"`
	Reversibility        string               `json:"reversibility,omitempty"`
	Input                map[string]any       `json:"input"`
	InputFingerprint     string               `json:"inputFingerprint"`
	InputProfile         string               `json:"inputProfile,omitempty"`
	ExecutionIdentity    string               `json:"executionIdentity,omitempty"`
	PreflightFingerprint string               `json:"preflightFingerprint,omitempty"`
	Summary              string               `json:"summary"`
	Source               string               `json:"source"`
	PolicyRevision       uint64               `json:"policyRevision"`
	Permission           CapabilityPermission `json:"permission"`
	Status               OperationStatus      `json:"status"`
	NextAction           string               `json:"nextAction,omitempty"`
	ChallengeHash        string               `json:"challengeHash,omitempty"`
	ChallengeExpiresAt   *time.Time           `json:"challengeExpiresAt,omitempty"`
	AttemptCount         int                  `json:"attemptCount"`
	VerificationAttempts int                  `json:"verificationAttempts"`
	LastError            string               `json:"lastError,omitempty"`
	Result               map[string]any       `json:"result,omitempty"`
	CreatedAt            time.Time            `json:"createdAt"`
	UpdatedAt            time.Time            `json:"updatedAt"`
}

type OperationView struct {
	ID                   string          `json:"id"`
	CapabilityID         string          `json:"capabilityId"`
	Status               OperationStatus `json:"status"`
	Summary              string          `json:"summary,omitempty"`
	NextAction           string          `json:"nextAction,omitempty"`
	ChallengeExpiresAt   *time.Time      `json:"challengeExpiresAt,omitempty"`
	AttemptCount         int             `json:"attemptCount"`
	VerificationAttempts int             `json:"verificationAttempts"`
	ErrorCode            string          `json:"errorCode,omitempty"`
	Result               any             `json:"result,omitempty"`
}

func publicOperation(record OperationRecord) OperationView {
	return OperationView{
		ID: record.ID, CapabilityID: record.CapabilityID, Status: record.Status,
		Summary: record.Summary, NextAction: record.NextAction,
		ChallengeExpiresAt: record.ChallengeExpiresAt, AttemptCount: record.AttemptCount,
		VerificationAttempts: record.VerificationAttempts, ErrorCode: record.LastError, Result: PublicResult(record.Result),
	}
}

type OperationService struct {
	root   string
	policy CapabilityPolicyStore
	now    func() time.Time
}

func NewOperationService(dataRoot string, policy CapabilityPolicyStore, now func() time.Time) *OperationService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &OperationService{root: filepath.Join(dataRoot, "operations"), policy: policy, now: now}
}

func (service *OperationService) Prepare(_ context.Context, definition CapabilityDefinition, input map[string]any, source string) (OperationView, string, error) {
	return service.PrepareWithEvidence(definition, input, source, nil)
}

func (service *OperationService) PrepareWithEvidence(definition CapabilityDefinition, input map[string]any, source string, preflight map[string]any) (OperationView, string, error) {
	return service.prepareWithProfile(definition, input, source, preflight, "")
}

func (service *OperationService) prepareWithProfile(definition CapabilityDefinition, input map[string]any, source string, preflight map[string]any, profile string) (OperationView, string, error) {
	policy, err := service.policy.Load()
	if err != nil {
		return OperationView{}, "", err
	}
	permission := policy.Decision(definition)
	if permission == CapabilityDisabled {
		return OperationView{}, "", errors.New("capability_disabled")
	}
	if strings.TrimSpace(definition.ID) == "" || strings.TrimSpace(source) == "" {
		return OperationView{}, "", errors.New("invalid_operation_request")
	}
	fingerprint, err := operationFingerprint(definition.ID, input)
	if err != nil {
		return OperationView{}, "", err
	}
	id, err := newOperationID(service.now())
	if err != nil {
		return OperationView{}, "", err
	}
	now := service.now().UTC()
	record := OperationRecord{
		Version: OperationRecordVersion, ID: id, CapabilityID: definition.ID, Domain: definition.Domain,
		Risk: definition.Risk, Effect: definition.Effect, Reversibility: definition.Reversibility,
		Input: cloneInput(input), InputFingerprint: fingerprint, InputProfile: profile,
		Summary: operationSummary(definition, input, fingerprint), Source: source,
		PolicyRevision: policy.Revision, Permission: permission, CreatedAt: now, UpdatedAt: now,
	}
	if preflight != nil {
		record.PreflightFingerprint, err = evidenceFingerprint(preflight)
		if err != nil {
			return OperationView{}, "", err
		}
	}
	if profile == serviceMessageInputProfile {
		record.ExecutionIdentity = "bot"
	}
	var token string
	if permission == CapabilityConfirmEach {
		token, err = randomSecret(24)
		if err != nil {
			return OperationView{}, "", err
		}
		expires := now.Add(OperationConfirmationWindow)
		record.Status = OperationAwaitingConfirmation
		record.NextAction = "confirm"
		record.ChallengeHash = secretHash(token)
		record.ChallengeExpiresAt = &expires
	} else {
		record.Status = OperationQueued
		record.NextAction = "execute"
	}
	if err := service.save(record); err != nil {
		return OperationView{}, "", err
	}
	return publicOperation(record), token, nil
}

func operationSummary(definition CapabilityDefinition, input map[string]any, fingerprint string) string {
	parts := []string{definition.ID}
	if definition.Effect != "" {
		parts = append(parts, definition.Effect)
	}
	targetLabel := ""
	targetValue := ""
	if value, ok := input["target-id"].(string); ok && strings.TrimSpace(value) != "" {
		targetValue = value
		targetLabel, _ = input["target-type"].(string)
	}
	if targetValue == "" {
		for _, key := range []string{
			"target", "url", "doc", "message-id", "chat-id", "user-id", "event-id", "task-id",
			"spreadsheet-token", "base-token", "meeting-id", "minute-token", "note-id", "presentation-id",
			"mailbox", "thread-id", "draft-id", "objective-id", "key-result-id", "target-value",
		} {
			if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
				targetLabel, targetValue = key, value
				break
			}
		}
	}
	if targetValue != "" {
		if strings.TrimSpace(targetLabel) == "" {
			targetLabel = "target"
		}
		targetHash := sha256.Sum256([]byte(targetValue))
		parts = append(parts, targetLabel+":"+hex.EncodeToString(targetHash[:4]))
	} else {
		parts = append(parts, "request:"+fingerprint[:8])
	}
	if definition.Risk == "destructive" && definition.GuardProfile == CapabilityGuardBounded {
		parts = append(parts, "结果可能无法独立验证")
	}
	return strings.Join(parts, " · ")
}

func (service *OperationService) Confirm(_ context.Context, id, token string) (OperationView, error) {
	return service.ConfirmWithEvidence(id, token, nil)
}

func (service *OperationService) ConfirmWithEvidence(id, token string, preflight map[string]any) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		now := service.now().UTC()
		if record.Status != OperationAwaitingConfirmation || record.ChallengeHash == "" {
			view = publicOperation(*record)
			return errors.New("confirmation_not_pending")
		}
		if record.ChallengeExpiresAt == nil || !now.Before(*record.ChallengeExpiresAt) {
			record.Status = OperationExpired
			record.NextAction = "reprepare_on_user_request"
			record.LastError = "confirmation_expired"
			record.ChallengeHash = ""
			service.cleanupOperationInput(*record)
			record.Input = nil
			record.UpdatedAt = now
			view = publicOperation(*record)
			return errors.New("confirmation_expired")
		}
		if secretHash(token) != record.ChallengeHash {
			view = publicOperation(*record)
			return errors.New("confirmation_invalid")
		}
		if record.PreflightFingerprint != "" {
			fingerprint, err := evidenceFingerprint(preflight)
			if err != nil || fingerprint != record.PreflightFingerprint {
				record.Status = OperationFailed
				record.NextAction = "reprepare_on_user_request"
				record.LastError = "preflight_changed"
				record.ChallengeHash = ""
				service.cleanupOperationInput(*record)
				record.Input = nil
				record.UpdatedAt = now
				view = publicOperation(*record)
				return errors.New("preflight_changed")
			}
		}
		policy, err := service.policy.Load()
		if err != nil {
			return err
		}
		definition := CapabilityDefinition{ID: record.CapabilityID, Risk: record.Risk}
		if policy.Revision != record.PolicyRevision || policy.Decision(definition) == CapabilityDisabled {
			record.Status = OperationFailed
			record.NextAction = "reprepare_on_user_request"
			record.LastError = "capability_policy_changed"
			record.ChallengeHash = ""
			service.cleanupOperationInput(*record)
			record.Input = nil
			record.UpdatedAt = now
			view = publicOperation(*record)
			return errors.New("capability_policy_changed")
		}
		record.Status = OperationQueued
		record.NextAction = "execute"
		record.ChallengeHash = ""
		record.ChallengeExpiresAt = nil
		record.UpdatedAt = now
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func (service *OperationService) Request(id string) (OperationRecord, error) {
	record, err := service.load(id)
	if err != nil {
		return OperationRecord{}, err
	}
	if record.Status != OperationQueued && record.Status != OperationAwaitingConfirmation {
		return OperationRecord{}, errors.New("operation_request_unavailable")
	}
	record.Input = cloneInput(record.Input)
	return record, nil
}

func (service *OperationService) Cancel(id string) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		if record.Status != OperationAwaitingConfirmation && record.Status != OperationQueued {
			return errors.New("operation_not_cancellable")
		}
		record.Status = OperationCancelled
		record.NextAction = "stop"
		record.ChallengeHash = ""
		service.cleanupOperationInput(*record)
		record.Input = nil
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func (service *OperationService) Status(id string) (OperationView, error) {
	record, err := service.load(id)
	if err != nil {
		return OperationView{}, err
	}
	if record.Status == OperationAwaitingConfirmation && record.ChallengeExpiresAt != nil && !service.now().Before(*record.ChallengeExpiresAt) {
		var view OperationView
		err := service.update(id, func(current *OperationRecord) error {
			if current.Status == OperationAwaitingConfirmation && current.ChallengeExpiresAt != nil && !service.now().Before(*current.ChallengeExpiresAt) {
				current.Status = OperationExpired
				current.NextAction = "reprepare_on_user_request"
				current.LastError = "confirmation_expired"
				current.ChallengeHash = ""
				service.cleanupOperationInput(*current)
				current.Input = nil
				current.UpdatedAt = service.now().UTC()
			}
			view = publicOperation(*current)
			return nil
		})
		return view, err
	}
	return publicOperation(record), nil
}

func (service *OperationService) MarkOutcomeUnknown(id, code string) (OperationView, error) {
	return service.MarkOutcomeUnknownWithResult(id, code, nil)
}

func (service *OperationService) MarkOutcomeUnknownWithResult(id, code string, result map[string]any) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		if record.Status == OperationOutcomeUnknown {
			view = publicOperation(*record)
			return nil
		}
		if record.Status != OperationQueued && record.Status != OperationRunning && record.Status != OperationVerifying {
			return errors.New("operation_not_in_flight")
		}
		if record.AttemptCount == 0 {
			record.AttemptCount = 1
		}
		record.Status = OperationOutcomeUnknown
		record.NextAction = unknownOutcomeNextAction(*record)
		record.LastError = safeCommandError(code)
		if result != nil {
			record.Result = boundCapabilityResult(result)
		}
		if record.NextAction == "manual_review" {
			service.cleanupOperationInput(*record)
			record.Input = nil
		}
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func unknownOutcomeNextAction(record OperationRecord) string {
	if record.InputProfile == serviceMessageInputProfile {
		return "manual_review"
	}
	if record.VerificationAttempts >= 3 {
		return "manual_review"
	}
	definition, ok := CapabilityByID(record.CapabilityID)
	if !ok || definition.Reread == nil {
		return "manual_review"
	}
	return "query_same_operation"
}

func (service *OperationService) ValidateQueuedRequest(id, capabilityID string, input map[string]any) error {
	record, err := service.load(id)
	if err != nil {
		return err
	}
	fingerprint, err := operationFingerprint(capabilityID, input)
	if err != nil {
		return err
	}
	if record.Status != OperationQueued || record.CapabilityID != capabilityID || record.InputFingerprint != fingerprint {
		return ErrOperationRequestMismatch
	}
	return service.validateCurrentPolicy(record)
}

func (service *OperationService) ValidateExecution(id, capabilityID string, input map[string]any) error {
	record, err := service.load(id)
	if err != nil {
		return err
	}
	fingerprint, err := operationFingerprint(capabilityID, input)
	if err != nil {
		return err
	}
	if record.ID != id || record.Version != OperationRecordVersion || record.Status != OperationRunning || record.CapabilityID != capabilityID || record.InputFingerprint != fingerprint || record.AttemptCount != 1 {
		return ErrOperationRequestMismatch
	}
	if err := service.validateCurrentPolicy(record); err != nil {
		return err
	}
	definition, ok := CapabilityByID(capabilityID)
	if !ok || !CapabilityPublished(definition) {
		return errors.New("capability_not_published")
	}
	settings, err := NewSettingsStore(filepath.Dir(service.root)).Load()
	if err != nil {
		return err
	}
	if err := validateCapabilityRuntimeGate(definition, settings); err != nil {
		return err
	}
	if effectiveCapabilityDryRun(definition, settings) || input["dry-run"] == true {
		return errors.New("execution_dry_run")
	}
	if record.InputProfile == serviceMessageInputProfile {
		if record.ExecutionIdentity != "bot" {
			return errors.New("invalid_service_execution_identity")
		}
		return validateServiceMessageInput(capabilityID, input)
	}
	if record.InputProfile != "" {
		return errors.New("unsupported_operation_input_profile")
	}
	return ValidateCapabilityInput(capabilityID, input)
}

func (service *OperationService) ClaimExecution(id, capabilityID string, input map[string]any) (OperationView, error) {
	return service.claimExecution(id, capabilityID, input, nil, false)
}

func (service *OperationService) ClaimExecutionWithEvidence(id, capabilityID string, input map[string]any, preflight map[string]any) (OperationView, error) {
	return service.claimExecution(id, capabilityID, input, preflight, true)
}

func (service *OperationService) claimExecution(id, capabilityID string, input map[string]any, preflight map[string]any, requireEvidence bool) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		fingerprint, err := operationFingerprint(capabilityID, input)
		if err != nil {
			return err
		}
		if record.Status != OperationQueued || record.CapabilityID != capabilityID || record.InputFingerprint != fingerprint {
			return ErrOperationRequestMismatch
		}
		if requireEvidence && record.PreflightFingerprint != "" {
			fingerprint, fingerprintErr := evidenceFingerprint(preflight)
			if fingerprintErr != nil || fingerprint != record.PreflightFingerprint {
				record.Status = OperationFailed
				record.NextAction = "reprepare_on_user_request"
				record.LastError = "preflight_changed"
				service.cleanupOperationInput(*record)
				record.Input = nil
				record.UpdatedAt = service.now().UTC()
				view = publicOperation(*record)
				return errors.New("preflight_changed")
			}
		}
		if err := service.validateCurrentPolicy(*record); err != nil {
			record.Status = OperationFailed
			record.NextAction = "reprepare_on_user_request"
			record.LastError = safeCommandError(err.Error())
			service.cleanupOperationInput(*record)
			record.Input = nil
			record.UpdatedAt = service.now().UTC()
			view = publicOperation(*record)
			return err
		}
		record.Status = OperationRunning
		record.NextAction = "query_same_operation"
		record.AttemptCount++
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func (service *OperationService) MarkVerifying(id string) (OperationView, error) {
	return service.transition(id, []OperationStatus{OperationRunning}, OperationVerifying, "query_same_operation", "")
}

func (service *OperationService) MarkVerifyingWithResult(id string, result map[string]any) (OperationView, error) {
	view, err := service.MarkVerifying(id)
	if err != nil || result == nil {
		return view, err
	}
	err = service.update(id, func(record *OperationRecord) error {
		if record.Status != OperationVerifying {
			return errors.New("operation_not_verifying")
		}
		record.Result = boundCapabilityResult(result)
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func (service *OperationService) Complete(id string) (OperationView, error) {
	return service.CompleteWithResult(id, nil)

}

func (service *OperationService) CompleteWithResult(id string, result map[string]any) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		if record.Status != OperationRunning && record.Status != OperationVerifying {
			return errors.New("invalid_operation_transition")
		}
		record.Status, record.NextAction, record.LastError = OperationSucceeded, "stop", ""
		record.Result = boundCapabilityResult(result)
		service.cleanupOperationInput(*record)
		record.Input = nil
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func (service *OperationService) BeginReconciliation(id string) (OperationRecord, error) {
	var output OperationRecord
	err := service.update(id, func(record *OperationRecord) error {
		if record.Status != OperationOutcomeUnknown || record.VerificationAttempts >= 3 {
			return errors.New("operation_not_reconcilable")
		}
		record.Status = OperationVerifying
		record.NextAction = "query_same_operation"
		record.VerificationAttempts++
		record.UpdatedAt = service.now().UTC()
		output = *record
		output.Input = cloneInput(record.Input)
		return nil
	})
	return output, err
}

func (service *OperationService) ReconciliationFailed(id, code string) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		if record.Status != OperationVerifying {
			return errors.New("operation_not_verifying")
		}
		record.Status = OperationOutcomeUnknown
		if record.VerificationAttempts >= 3 {
			record.NextAction = "manual_review"
			service.cleanupOperationInput(*record)
			record.Input = nil
		} else {
			record.NextAction = "query_same_operation"
		}
		record.LastError = safeCommandError(code)
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

// ResolveReconciliation applies only a reviewed verification decision. Merely
// obtaining reread evidence is inconclusive and must never promote an uncertain
// side effect to success.
func (service *OperationService) ResolveReconciliation(id string, assessment VerificationAssessment) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		if record.Status != OperationVerifying {
			return errors.New("operation_not_verifying")
		}
		state := assessment.normalizedState()
		var evidence map[string]any
		if assessment.Evidence != nil {
			evidence = boundCapabilityResult(assessment.Evidence)
		}
		result := cloneInput(record.Result)
		result["verificationState"] = string(state)
		result["verified"] = state == VerificationConfirmed
		result["verification"] = evidence
		record.Result = boundCapabilityResult(result)
		switch state {
		case VerificationConfirmed:
			record.Status = OperationSucceeded
			record.NextAction = "stop"
			record.LastError = ""
			service.cleanupOperationInput(*record)
			record.Input = nil
		case VerificationRejected:
			record.Status = OperationFailed
			record.NextAction = "stop"
			record.LastError = "verification_rejected"
			service.cleanupOperationInput(*record)
			record.Input = nil
		default:
			record.Status = OperationOutcomeUnknown
			record.LastError = "verification_inconclusive"
			if record.VerificationAttempts >= 3 {
				record.NextAction = "manual_review"
				service.cleanupOperationInput(*record)
				record.Input = nil
			} else {
				record.NextAction = "query_same_operation"
			}
		}
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func (service *OperationService) Unknown(limit int) ([]OperationRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	entries, err := os.ReadDir(service.root)
	if errors.Is(err, os.ErrNotExist) {
		return []OperationRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := make([]OperationRecord, 0, limit)
	for _, entry := range entries {
		if len(values) >= limit {
			break
		}
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		record, loadErr := service.load(id)
		definition, known := CapabilityByID(record.CapabilityID)
		if loadErr == nil && record.InputProfile == "" && known && definition.Reread != nil && record.Status == OperationOutcomeUnknown && record.VerificationAttempts < 3 {
			values = append(values, record)
		}
	}
	return values, nil
}

func (service *OperationService) ExpireAwaiting(limit int) error {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	entries, err := os.ReadDir(service.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	processed := 0
	for _, entry := range entries {
		if processed >= limit || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		record, loadErr := service.load(id)
		if loadErr != nil || record.Status != OperationAwaitingConfirmation || record.ChallengeExpiresAt == nil || service.now().Before(*record.ChallengeExpiresAt) {
			continue
		}
		_, _ = service.Status(id)
		processed++
	}
	return nil
}

func (service *OperationService) RecoverInterrupted(limit int) error {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	entries, err := os.ReadDir(service.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	processed := 0
	for _, entry := range entries {
		if processed >= limit || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		record, loadErr := service.load(id)
		if loadErr == nil && record.Status == OperationQueued && record.InputProfile == serviceMessageInputProfile {
			if _, err := service.RejectBeforeExecution(id, "service_message_dispatch_interrupted"); err != nil {
				return err
			}
			processed++
			continue
		}
		if loadErr != nil || (record.Status != OperationRunning && record.Status != OperationVerifying) {
			continue
		}
		if _, err := service.MarkOutcomeUnknown(id, "execution_interrupted"); err != nil {
			return err
		}
		processed++
	}
	return nil
}

func (service *OperationService) Fail(id, code string) (OperationView, error) {
	return service.transition(id, []OperationStatus{OperationQueued, OperationRunning, OperationVerifying}, OperationFailed, "stop", code)
}

func (service *OperationService) RejectBeforeExecution(id, code string) (OperationView, error) {
	return service.transition(id, []OperationStatus{OperationQueued}, OperationFailed, "reprepare_on_user_request", code)
}

func (service *OperationService) transition(id string, from []OperationStatus, to OperationStatus, nextAction, code string) (OperationView, error) {
	var view OperationView
	err := service.update(id, func(record *OperationRecord) error {
		allowed := false
		for _, status := range from {
			allowed = allowed || record.Status == status
		}
		if !allowed {
			return errors.New("invalid_operation_transition")
		}
		record.Status = to
		record.NextAction = nextAction
		record.LastError = safeCommandError(code)
		if to == OperationSucceeded || to == OperationFailed {
			service.cleanupOperationInput(*record)
			record.Input = nil
		}
		record.UpdatedAt = service.now().UTC()
		view = publicOperation(*record)
		return nil
	})
	return view, err
}

func (service *OperationService) cleanupOperationInput(record OperationRecord) {
	if record.CapabilityID != "im.sdk.message.send" {
		return
	}
	relative, _ := record.Input["file-path"].(string)
	if relative == "" || filepath.IsAbs(relative) {
		return
	}
	dataRoot := filepath.Dir(service.root)
	mediaRoot := filepath.Join(dataRoot, "private-cache", "media")
	path := filepath.Join(dataRoot, filepath.FromSlash(relative))
	within, err := filepath.Rel(mediaRoot, path)
	if err != nil || within == "." || filepath.IsAbs(within) || strings.HasPrefix(within, ".."+string(os.PathSeparator)) {
		return
	}
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		_ = os.Remove(path)
	}
}

func (service *OperationService) validateCurrentPolicy(record OperationRecord) error {
	policy, err := service.policy.Load()
	if err != nil {
		return err
	}
	definition, ok := CapabilityByID(record.CapabilityID)
	if !ok {
		return errors.New("unknown_capability")
	}
	if policy.Revision != record.PolicyRevision || policy.Decision(definition) != record.Permission || record.Permission == CapabilityDisabled {
		return errors.New("capability_policy_changed")
	}
	return nil
}

func (service *OperationService) operationPath(id string) (string, error) {
	if !operationIDPattern.MatchString(id) {
		return "", errors.New("invalid_operation_id")
	}
	return filepath.Join(service.root, id+".json"), nil
}

func (service *OperationService) save(record OperationRecord) error {
	path, err := service.operationPath(record.ID)
	if err != nil {
		return err
	}
	if err := ensurePrivateDirectory(service.root); err != nil {
		return err
	}
	err = withProcessFileLock(path+".lock", func() error {
		if _, err := os.Lstat(path); err == nil {
			return errors.New("operation_already_exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return writeOperationRecord(path, record)
	})
	if err == nil {
		signalMaintenance(filepath.Dir(service.root))
	}
	return err
}

func (service *OperationService) load(id string) (OperationRecord, error) {
	path, err := service.operationPath(id)
	if err != nil {
		return OperationRecord{}, err
	}
	var record OperationRecord
	missing, err := readPrivateJSON(path, &record)
	if missing {
		return OperationRecord{}, errors.New("operation_not_found")
	}
	return record, err
}

func (service *OperationService) update(id string, mutate func(*OperationRecord) error) error {
	path, err := service.operationPath(id)
	if err != nil {
		return err
	}
	err = withProcessFileLock(path+".lock", func() error {
		var record OperationRecord
		missing, err := readPrivateJSON(path, &record)
		if missing {
			return errors.New("operation_not_found")
		}
		if err != nil {
			return err
		}
		mutateErr := mutate(&record)
		if err := writeOperationRecord(path, record); err != nil {
			return err
		}
		return mutateErr
	})
	if err == nil {
		signalMaintenance(filepath.Dir(service.root))
	}
	return err
}

func operationFingerprint(capabilityID string, input map[string]any) (string, error) {
	data, err := json.Marshal(map[string]any{"capabilityId": capabilityID, "input": input})
	if err != nil {
		return "", fmt.Errorf("invalid_operation_input: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func writeOperationRecord(path string, record OperationRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > maximumPrivateJSONBytes {
		return errors.New("operation_record_too_large")
	}
	return writePrivateJSON(path, record)
}

func evidenceFingerprint(value map[string]any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("invalid_preflight_evidence: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func newOperationID(now time.Time) (string, error) {
	value := make([]byte, 4)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "OP-" + now.UTC().Format("20060102150405") + "-" + strings.ToUpper(hex.EncodeToString(value)), nil
}

func randomSecret(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func secretHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
