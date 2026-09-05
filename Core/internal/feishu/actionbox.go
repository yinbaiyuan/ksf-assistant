package feishu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const QueueSchemaVersion = 3

type ActionRequest struct {
	ID                    string         `json:"id"`
	Type                  string         `json:"type"`
	Domain                string         `json:"domain"`
	Action                string         `json:"action"`
	CapabilityID          string         `json:"capabilityId"`
	OperationID           string         `json:"operationId,omitempty"`
	Identity              string         `json:"identity"`
	Input                 map[string]any `json:"input"`
	ExplicitAuthorization bool           `json:"explicitAuthorization"`
	ConfirmHighImpact     bool           `json:"confirmHighImpact,omitempty"`
	DryRun                bool           `json:"dryRun,omitempty"`
	RemoteTimeoutMS       int            `json:"remoteTimeoutMs,omitempty"`
	PollIntervalMS        int            `json:"pollIntervalMs,omitempty"`
	SaveAs                string         `json:"saveAs,omitempty"`
	Source                string         `json:"source"`
	Reason                string         `json:"reason,omitempty"`
	Trace                 map[string]any `json:"trace,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
}
type ActionResult struct {
	ID           string         `json:"id"`
	OperationID  string         `json:"operationId,omitempty"`
	Status       string         `json:"status"`
	CapabilityID string         `json:"capabilityId"`
	Result       map[string]any `json:"result,omitempty"`
	Error        string         `json:"error,omitempty"`
	CompletedAt  time.Time      `json:"completedAt"`
}
type queueState struct {
	SchemaVersion      int               `json:"schemaVersion"`
	ProcessedLineCount int               `json:"processedLineCount"`
	ProcessedIDs       map[string]string `json:"processedIds"`
	LastProcessedAt    string            `json:"lastProcessedAt"`
	LastError          string            `json:"lastError"`
	LastTrigger        any               `json:"lastTrigger,omitempty"`
	Wake               queueWake         `json:"wake"`
}
type queueWake struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	ConfiguredPort int    `json:"configuredPort"`
	ActualPort     *int   `json:"actualPort"`
}

func defaultQueueState() queueState {
	return queueState{SchemaVersion: QueueSchemaVersion, ProcessedIDs: map[string]string{}, Wake: queueWake{Host: "127.0.0.1"}}
}
func normalizeQueueState(state *queueState) {
	state.SchemaVersion = QueueSchemaVersion
	if state.ProcessedIDs == nil {
		state.ProcessedIDs = map[string]string{}
	}
	if state.Wake.Host == "" {
		state.Wake.Host = "127.0.0.1"
	}
	if len(state.ProcessedIDs) > 5000 {
		trimProcessedIDs(state.ProcessedIDs, 5000)
	}
}
func trimProcessedIDs(values map[string]string, limit int) {
	for len(values) > limit {
		for key := range values {
			delete(values, key)
			break
		}
	}
}

type Actionbox struct {
	root, dataRoot string
	repository     workRepository
	operations     *OperationService
}

func NewActionbox(dataRoot string) *Actionbox {
	return &Actionbox{root: filepath.Join(dataRoot, "logs"), dataRoot: dataRoot, repository: newWorkRepository(dataRoot, "actionbox")}
}

func NewGovernedActionbox(dataRoot string, operations *OperationService) *Actionbox {
	return &Actionbox{root: filepath.Join(dataRoot, "logs"), dataRoot: dataRoot, repository: newWorkRepository(dataRoot, "actionbox"), operations: operations}
}
func (box *Actionbox) queuePath() string  { return filepath.Join(box.root, "actionbox.jsonl") }
func (box *Actionbox) resultPath() string { return filepath.Join(box.root, "actionbox-results.jsonl") }
func (box *Actionbox) statePath() string  { return filepath.Join(box.root, "actionbox-state.json") }
func (box *Actionbox) processLockPath() string {
	return filepath.Join(box.root, ".actionbox-process.lock")
}
func NewActionID() (string, error) {
	return newQueueID("ACT")
}

func NewOutboxID() (string, error) { return newQueueID("OUT") }

func newQueueID(prefix string) (string, error) {
	value := make([]byte, 10)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "-" + time.Now().UTC().Format("20060102150405") + "-" + strings.ToUpper(hex.EncodeToString(value[:4])), nil
}

func (box *Actionbox) Submit(request ActionRequest) error {
	if request.ID == "" || request.Type != "feishu_capability" || request.Domain != "capability" || request.Action != "execute" || !request.ExplicitAuthorization {
		return errors.New("invalid actionbox request")
	}
	definition, ok := CapabilityByID(request.CapabilityID)
	if !ok {
		return errors.New("unknown_capability")
	}
	if definition.Risk == "read" {
		return errors.New("read_capability_must_not_enter_actionbox")
	}
	if definition.Queue != "actionbox" && !(box.operations != nil && definition.Queue == "docbox") {
		return errors.New("capability_requires_" + definition.Queue)
	}
	if definition.Identity != request.Identity {
		return errors.New("unsupported_identity")
	}
	if request.OperationID != "" {
		if box.operations == nil {
			return errors.New("operation_service_unavailable")
		}
		if err := box.operations.ValidateQueuedRequest(request.OperationID, request.CapabilityID, request.Input); err != nil {
			return err
		}
	} else {
		if definition.Risk == "destructive" {
			return errors.New("destructive_operation_requires_governance")
		}
		if definition.Risk == "high-impact-write" && !request.ConfirmHighImpact {
			return errors.New("high_impact_confirmation_required")
		}
	}
	if definition.Risk == "remote-operation" {
		if request.RemoteTimeoutMS != 0 && (request.RemoteTimeoutMS < 10000 || request.RemoteTimeoutMS > 30*60*1000) {
			return errors.New("invalid_remote_timeout")
		}
		if request.PollIntervalMS != 0 && (request.PollIntervalMS < 250 || request.PollIntervalMS > 30000) {
			return errors.New("invalid_remote_poll_interval")
		}
	} else if request.RemoteTimeoutMS != 0 || request.PollIntervalMS != 0 {
		return errors.New("remote_poll_not_supported")
	}
	if err := ValidateCapabilityInput(request.CapabilityID, request.Input); err != nil {
		return err
	}
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		return err
	}
	return box.repository.enqueue(request.ID, request, workConflictKey(definition, request.Input), definition.Backend, definition.ExecutionClass, definition.RetryClass, request.CreatedAt)
}

type actionExecutor interface {
	ExecuteWithOptions(context.Context, string, map[string]any, CapabilityExecutionOptions) (map[string]any, error)
}

type actionPreflightReader interface {
	ReadPreflight(context.Context, string, map[string]any) (map[string]any, error)
}

type actionVerificationReader interface {
	ReadVerification(context.Context, string, map[string]any, map[string]any) (VerificationAssessment, error)
}

// process is intentionally package-private. Production callers must enter via
// CapabilityService.ProcessActions so the governed queue cannot be paired with
// a different executor than the service used for prepare and reconciliation.
func (box *Actionbox) process(ctx context.Context, executor actionExecutor) error {
	for {
		processed, err := box.processOne(ctx, executor)
		if err != nil || !processed {
			return err
		}
	}
}

func (box *Actionbox) processOne(ctx context.Context, executor actionExecutor) (bool, error) {
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		return false, err
	}
	item, found, err := box.repository.claim()
	if err != nil || !found {
		return found, err
	}
	return true, box.processClaimed(ctx, item, executor)
}

func (box *Actionbox) processClaimed(ctx context.Context, item WorkItemV3, executor actionExecutor) error {
	var request ActionRequest
	result := ActionResult{ID: item.ID, CompletedAt: time.Now().UTC()}
	if json.Unmarshal(item.Request, &request) != nil {
		result.Status, result.Error = "failed", "invalid_json_line"
	} else {
		result = box.executeRequest(ctx, executor, request)
	}
	if err := box.repository.finish(item, result, result.Error); err != nil {
		return err
	}
	inputJSON, _ := json.Marshal(request.Input)
	_ = NewAuditLog(box.dataRoot).Record("actionbox_result", map[string]any{"id": result.ID, "capability": result.CapabilityID, "status": result.Status, "input": AuditContentDescriptor(string(inputJSON)), "error": result.Error})
	return nil
}

func (box *Actionbox) executeRequest(ctx context.Context, executor actionExecutor, request ActionRequest) ActionResult {
	result := ActionResult{ID: request.ID, OperationID: request.OperationID, CapabilityID: request.CapabilityID, CompletedAt: time.Now().UTC()}
	definition, knownCapability := CapabilityByID(request.CapabilityID)
	if request.ID == "" || request.Type != "feishu_capability" || request.Domain != "capability" || request.Action != "execute" || !request.ExplicitAuthorization {
		result.Status = "failed"
		result.Error = "invalid_actionbox_request"
	} else if !knownCapability || !CapabilityPublished(definition) {
		result.Status = "failed"
		result.Error = "capability_not_published"
	} else if definition.Identity != request.Identity {
		result.Status = "failed"
		result.Error = "unsupported_identity"
	} else if inputErr := ValidateCapabilityInput(request.CapabilityID, request.Input); inputErr != nil {
		result.Status = "failed"
		result.Error = "invalid_capability_input"
	} else if request.OperationID == "" && definition.Risk == "destructive" {
		result.Status = "failed"
		result.Error = "destructive_operation_requires_governance"
	} else if request.OperationID == "" && definition.Risk == "high-impact-write" && !request.ConfirmHighImpact {
		result.Status = "failed"
		result.Error = "high_impact_confirmation_required"
	} else if settings, settingsErr := NewSettingsStore(box.dataRoot).Load(); settingsErr != nil {
		result.Status = "failed"
		result.Error = safeCommandError(settingsErr.Error())
	} else if gateErr := validateCapabilityRuntimeGate(definition, settings); gateErr != nil {
		result.Status = "failed"
		result.Error = gateErr.Error()
	} else {
		request.DryRun = request.DryRun || effectiveCapabilityDryRun(definition, settings)
	}
	if result.Status != "" && request.OperationID != "" && box.operations != nil {
		if result.Error == "actionbox_disabled" || result.Error == "outbound_disabled" || result.Error == "docbox_disabled" {
			_, _ = box.operations.RejectBeforeExecution(request.OperationID, result.Error)
		} else {
			_, _ = box.operations.Fail(request.OperationID, result.Error)
		}
	}
	var executionPreflight map[string]any
	if result.Status == "" && !request.DryRun && request.OperationID != "" && definition.Preflight != nil {
		reader, ok := executor.(actionPreflightReader)
		if !ok {
			result.Status = "failed"
			result.Error = "preflight_recheck_unavailable"
		} else if value, preflightErr := reader.ReadPreflight(ctx, request.CapabilityID, request.Input); preflightErr != nil {
			result.Status = "failed"
			result.Error = "preflight_recheck_failed"
		} else {
			executionPreflight = value
		}
		if result.Status != "" && box.operations != nil {
			_, _ = box.operations.RejectBeforeExecution(request.OperationID, result.Error)
		}
	}
	if result.Status == "" && request.DryRun {
		if request.OperationID != "" {
			if box.operations == nil {
				result.Status = "failed"
				result.Error = "operation_service_unavailable"
			} else if _, claimErr := box.operations.ClaimExecution(request.OperationID, request.CapabilityID, request.Input); claimErr != nil {
				result.Status = "failed"
				result.Error = safeCommandError(claimErr.Error())
			} else {
				_, _ = box.operations.MarkVerifying(request.OperationID)
				_, _ = box.operations.CompleteWithResult(request.OperationID, map[string]any{"dryRun": true})
			}
		}
		if result.Status == "" {
			result.Status = "dry_run"
			result.Result = map[string]any{"dryRun": true}
		}
	} else if result.Status == "" {
		if request.OperationID != "" {
			if box.operations == nil {
				result.Status = "failed"
				result.Error = "operation_service_unavailable"
			} else if _, claimErr := box.operations.ClaimExecutionWithEvidence(request.OperationID, request.CapabilityID, request.Input, executionPreflight); claimErr != nil {
				result.Status = "failed"
				result.Error = safeCommandError(claimErr.Error())
			}
		}
		if result.Status == "" {
			value, runErr := executor.ExecuteWithOptions(ctx, request.CapabilityID, request.Input, CapabilityExecutionOptions{RemoteTimeout: time.Duration(request.RemoteTimeoutMS) * time.Millisecond, RemotePollInterval: time.Duration(request.PollIntervalMS) * time.Millisecond, OperationID: request.OperationID})
			if runErr != nil {
				result.Error = safeCommandError(runErr.Error())
				operationCode := CapabilityOperationErrorCode(runErr)
				if request.OperationID != "" && isUncertainExecutionError(runErr) {
					result.Status = string(OperationOutcomeUnknown)
					result.Result = value
					_, _ = box.operations.MarkOutcomeUnknownWithResult(request.OperationID, operationCode, value)
				} else {
					result.Status = "failed"
					if request.OperationID != "" {
						_, _ = box.operations.Fail(request.OperationID, operationCode)
					}
				}
			} else {
				if request.OperationID != "" {
					_, _ = box.operations.MarkVerifyingWithResult(request.OperationID, value)
					if definition.Risk == "destructive" {
						assessment := VerificationAssessment{State: VerificationInconclusive}
						if reader, ok := executor.(actionVerificationReader); ok {
							if verified, verifyErr := reader.ReadVerification(ctx, request.CapabilityID, request.Input, value); verifyErr == nil {
								assessment = verified
							} else {
								result.Error = safeCommandError(verifyErr.Error())
							}
						}
						view, _ := box.operations.ResolveReconciliation(request.OperationID, assessment)
						result.Status = string(view.Status)
						if view.Status == OperationOutcomeUnknown && result.Error == "" {
							result.Error = "verification_inconclusive"
						}
					} else {
						_, _ = box.operations.CompleteWithResult(request.OperationID, value)
						result.Status = string(OperationSucceeded)
					}
				} else {
					result.Status = "completed"
				}
				result.Result = value
			}
		}
	}
	return result
}

func isUncertainExecutionError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || CapabilityOutcomeUncertain(err)
}

func (box *Actionbox) FindResult(id string) (ActionResult, bool, error) {
	var result ActionResult
	if err := box.repository.migrateLegacy(box.queuePath(), box.resultPath(), box.statePath()); err != nil {
		return result, false, err
	}
	found, err := box.repository.findResult(id, &result)
	return result, found, err
}
func appendPrivateJSONL(path string, value any) error {
	return withProcessFileLock(path+".lock", func() error {
		return appendPrivateJSONLUnlocked(path, value)
	})
}

func appendPrivateJSONLUnlocked(path string, value any) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if runtimePrivatePermissions() {
		return os.Chmod(path, 0o600)
	}
	return securePrivatePath(path, false)
}
func runtimePrivatePermissions() bool { return os.PathSeparator == '/' }
