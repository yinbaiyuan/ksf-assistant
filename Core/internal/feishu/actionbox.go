package feishu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
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
	if definition.Queue != "actionbox" {
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
		ctx = context.WithValue(ctx, executionWorkKey{}, executionWork{repository: box.repository, item: item})
		result = box.executeRequest(ctx, executor, request)
	}
	if strings.HasPrefix(result.Error, "operation_persistence_failed:") {
		return errors.New(result.Error)
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
	fail := func(err error) ActionResult {
		result.Status, result.Error = "failed", safeCommandError(err.Error())
		return result
	}
	definition, known := CapabilityByID(request.CapabilityID)
	if request.ID == "" || request.Type != "feishu_capability" || request.Domain != "capability" || request.Action != "execute" || !request.ExplicitAuthorization {
		return fail(errors.New("invalid_actionbox_request"))
	}
	if !known || !CapabilityPublished(definition) {
		return fail(errors.New("capability_not_published"))
	}
	if definition.Identity != request.Identity {
		return fail(errors.New("unsupported_identity"))
	}
	if err := box.validateExecutionInput(request, executor); err != nil {
		return fail(err)
	}
	if request.OperationID != "" {
		if box.operations == nil {
			return fail(errors.New("operation_service_unavailable"))
		}
		if err := box.operations.ValidateQueuedRequest(request.OperationID, request.CapabilityID, request.Input); err != nil {
			if err.Error() == "capability_policy_changed" {
				if _, persistErr := box.operations.RejectBeforeExecution(request.OperationID, err.Error()); persistErr != nil {
					return operationPersistenceFailure(result, persistErr)
				}
			}
			return fail(err)
		}
	}
	settings, err := NewSettingsStore(box.dataRoot).Load()
	if err != nil {
		return fail(err)
	}
	if err := validateCapabilityRuntimeGate(definition, settings); err != nil {
		if request.OperationID != "" {
			if _, persistErr := box.operations.RejectBeforeExecution(request.OperationID, err.Error()); persistErr != nil {
				return operationPersistenceFailure(result, persistErr)
			}
		}
		return fail(err)
	}
	dryRun := request.DryRun || request.Input["dry-run"] == true || effectiveCapabilityDryRun(definition, settings)
	if request.OperationID == "" {
		if !dryRun {
			return fail(errors.New("operation_authorization_required"))
		}
		result.Status, result.Result = "dry_run", map[string]any{"dryRun": true}
		return result
	}
	var preflight map[string]any
	if !dryRun && definition.Preflight != nil {
		reader, ok := executor.(actionPreflightReader)
		if !ok {
			err = errors.New("preflight_recheck_unavailable")
		} else {
			preflight, err = reader.ReadPreflight(ctx, request.CapabilityID, request.Input)
		}
		if err != nil {
			if _, persistErr := box.operations.RejectBeforeExecution(request.OperationID, "preflight_recheck_failed"); persistErr != nil {
				return operationPersistenceFailure(result, persistErr)
			}
			return fail(err)
		}
	}
	if dryRun {
		_, err = box.operations.ClaimExecution(request.OperationID, request.CapabilityID, request.Input)
	} else {
		_, err = box.operations.ClaimExecutionWithEvidence(request.OperationID, request.CapabilityID, request.Input, preflight)
	}
	if err != nil {
		return fail(err)
	}
	if work, ok := ctx.Value(executionWorkKey{}).(executionWork); ok {
		if err := work.repository.setExecutionPhase(work.item.ID, "operation_claimed"); err != nil {
			return operationPersistenceFailure(result, err)
		}
	}
	if dryRun {
		result.Result = map[string]any{"dryRun": true}
		if _, err := box.operations.CompleteWithResult(request.OperationID, result.Result); err != nil {
			return operationPersistenceFailure(result, err)
		}
		result.Status = "dry_run"
		return result
	}
	ctx = context.WithValue(ctx, executionBoundaryKey{}, executionBoundary{operations: box.operations, operationID: request.OperationID, capabilityID: request.CapabilityID, input: request.Input})
	if err := beforeRemoteWrite(ctx); err != nil {
		if _, persistErr := box.operations.Fail(request.OperationID, err.Error()); persistErr != nil {
			return operationPersistenceFailure(result, persistErr)
		}
		return fail(err)
	}
	value, runErr := executor.ExecuteWithOptions(ctx, request.CapabilityID, request.Input, CapabilityExecutionOptions{RemoteTimeout: time.Duration(request.RemoteTimeoutMS) * time.Millisecond, RemotePollInterval: time.Duration(request.PollIntervalMS) * time.Millisecond, OperationID: request.OperationID})
	result.Result = value
	if runErr != nil {
		value = cliFailureResult(value, runErr)
		result.Result = value
		result.Error = safeCommandError(runErr.Error())
		if isUncertainExecutionError(runErr) {
			result.Status = string(OperationOutcomeUnknown)
			_, err = box.operations.MarkOutcomeUnknownWithResult(request.OperationID, CapabilityOperationErrorCode(runErr), value)
		} else {
			result.Status = "failed"
			_, err = box.operations.Fail(request.OperationID, CapabilityOperationErrorCode(runErr))
		}
		if err != nil {
			return operationPersistenceFailure(result, err)
		}
		return result
	}
	if _, err := box.operations.MarkVerifyingWithResult(request.OperationID, value); err != nil {
		return operationPersistenceFailure(result, err)
	}
	if work, ok := ctx.Value(executionWorkKey{}).(executionWork); ok {
		if err := work.repository.setExecutionPhase(work.item.ID, "verifying"); err != nil {
			return operationPersistenceFailure(result, err)
		}
	}
	if definition.Risk == "destructive" {
		assessment := VerificationAssessment{State: VerificationInconclusive}
		if reader, ok := executor.(actionVerificationReader); ok {
			if verified, verifyErr := reader.ReadVerification(ctx, request.CapabilityID, request.Input, value); verifyErr == nil {
				assessment = verified
			} else {
				result.Error = safeCommandError(verifyErr.Error())
			}
		}
		view, resolveErr := box.operations.ResolveReconciliation(request.OperationID, assessment)
		if resolveErr != nil {
			return operationPersistenceFailure(result, resolveErr)
		}
		result.Status = string(view.Status)
		if view.Status == OperationOutcomeUnknown && result.Error == "" {
			result.Error = "verification_inconclusive"
		}
	} else {
		if _, err := box.operations.CompleteWithResult(request.OperationID, value); err != nil {
			return operationPersistenceFailure(result, err)
		}
		result.Status = string(OperationSucceeded)
	}
	return result
}

func operationPersistenceFailure(result ActionResult, err error) ActionResult {
	result.Status, result.Error = string(OperationOutcomeUnknown), "operation_persistence_failed: "+safeCommandError(err.Error())
	return result
}

func isUncertainExecutionError(err error) bool {
	var cliError *CLIExecutionError
	if errors.As(err, &cliError) {
		return cliError.Started
	}
	if isUserApprovalError(err) {
		return false
	}
	var networkError net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &networkError) || CapabilityOutcomeUncertain(err)
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
