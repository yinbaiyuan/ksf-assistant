package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

type DocumentExecutionTransport interface {
	DocumentCreate(context.Context, DocumentRequest) (map[string]any, error)
	DocumentFetch(context.Context, DocumentTarget) (map[string]any, error)
	DocumentVersion(context.Context, DocumentTarget, map[string]any, string) (map[string]any, error)
	DocumentUpdate(context.Context, DocumentRequest) (map[string]any, error)
}

type DirectExecutionAdapter struct {
	DataRoot  string
	Sender    MessageSender
	Documents DocumentExecutionTransport
}

type executionBoundaryKey struct{}
type executionSenderKey struct{}
type executionWorkKey struct{}
type executionMessagePartKey struct{}

type executionMessagePart struct {
	target            MessageTarget
	format, value, id string
}

type executionBoundary struct {
	operations   *OperationService
	operationID  string
	capabilityID string
	input        map[string]any
}

type executionWork struct {
	repository workRepository
	item       WorkItemV4
}

func checkExecutionBoundary(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	boundary, ok := ctx.Value(executionBoundaryKey{}).(executionBoundary)
	if !ok {
		return errors.New("operation_execution_boundary_required")
	}
	return boundary.operations.ValidateExecution(boundary.operationID, boundary.capabilityID, boundary.input)
}

func ValidateMessageExecution(ctx context.Context, capabilityID string, input map[string]any) error {
	if !contains([]string{"im.sdk.message.send", "im.message.reply", "im.message.edit"}, capabilityID) {
		return errors.New("unsupported_bound_message_capability")
	}
	return validateDirectBinding(ctx, capabilityID, input, "")
}

func validateDirectBinding(ctx context.Context, capabilityID string, input map[string]any, operationID string) error {
	boundary, ok := ctx.Value(executionBoundaryKey{}).(executionBoundary)
	if !ok {
		return errors.New("operation_execution_boundary_required")
	}
	if boundary.capabilityID != capabilityID || operationID != "" && boundary.operationID != operationID {
		return ErrOperationRequestMismatch
	}
	expected, err := operationFingerprint(boundary.capabilityID, boundary.input)
	if err != nil {
		return err
	}
	actual, err := operationFingerprint(capabilityID, input)
	if err != nil {
		return err
	}
	if expected != actual {
		return ErrOperationRequestMismatch
	}
	return checkExecutionBoundary(ctx)
}

func validateDocumentExecution(ctx context.Context, request DocumentRequest) error {
	boundary, ok := ctx.Value(executionBoundaryKey{}).(executionBoundary)
	if !ok {
		return errors.New("operation_execution_boundary_required")
	}
	if request.NewTitle != "" {
		return ErrOperationRequestMismatch
	}
	if boundary.capabilityID == "docs.whiteboard.insert" {
		config, err := NewClientConfigStore(filepath.Dir(boundary.operations.root)).Load()
		if err != nil {
			return err
		}
		target, err := config.ResolveDocumentTarget(fmt.Sprint(boundary.input["doc"]))
		if err != nil {
			return err
		}
		content, err := DocWhiteboardXML(boundary.input)
		if err != nil {
			return err
		}
		if request.Action != "update_document" || request.UpdateMode != "append" || request.Target == nil || *request.Target != target || request.Content.Format != "text" || request.Content.Text != content {
			return ErrOperationRequestMismatch
		}
		return validateDirectBinding(ctx, boundary.capabilityID, boundary.input, request.OperationID)
	}
	id, input := documentCapabilityInput(request)
	if value, exists := boundary.input["dry-run"]; exists {
		input["dry-run"] = value
	}
	return validateDirectBinding(ctx, id, input, request.OperationID)
}

type boundMessageExecutor func(context.Context) (map[string]any, error)

func (execute boundMessageExecutor) ExecuteWithOptions(ctx context.Context, _ string, _ map[string]any, _ CapabilityExecutionOptions) (map[string]any, error) {
	if err := beforeRemoteWrite(ctx); err != nil {
		return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
	}
	return execute(ctx)
}

func (service *OperationService) ExecuteBoundMessage(ctx context.Context, operationID, capabilityID string, input map[string]any, execute func(context.Context) (map[string]any, error)) (ActionResult, error) {
	definition, known := CapabilityByID(capabilityID)
	if !known || !contains([]string{"im.sdk.message.send", "im.message.reply", "im.message.edit"}, capabilityID) || execute == nil {
		return ActionResult{}, errors.New("unsupported_bound_message_capability")
	}
	if definition.Identity != "bot" {
		record, err := service.load(operationID)
		if err != nil {
			return ActionResult{}, err
		}
		if record.InputProfile != serviceMessageInputProfile || record.ExecutionIdentity != "bot" {
			return ActionResult{}, errors.New("service_message_profile_required")
		}
	}
	id, err := NewActionID()
	if err != nil {
		return ActionResult{}, err
	}
	conflictKey := workConflictKey(definition, input)
	if targetType, ok := input["target-type"].(string); ok {
		sendDefinition, _ := CapabilityByID("im.sdk.message.send")
		conflictKey = workConflictKey(sendDefinition, map[string]any{"target-type": targetType, "target-id": input["target-id"]})
	}
	ctx, release, err := admitExecution(ctx, filepath.Dir(service.root), WorkItemV4{ConflictKey: conflictKey, Backend: "go-sdk", ExecutionClass: "standard"})
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	result := NewGovernedActionbox(filepath.Dir(service.root), service).executeRequest(ctx, boundMessageExecutor(execute), queueActionRequest(id, operationID, capabilityID, input))
	if result.Status != string(OperationSucceeded) && result.Status != "dry_run" {
		if result.Error == "" {
			result.Error = "bound_message_execution_failed"
		}
		return result, errors.New(result.Error)
	}
	return result, nil
}

func (box *Actionbox) validateExecutionInput(request ActionRequest, executor actionExecutor) error {
	if request.OperationID != "" && box.operations != nil {
		record, err := box.operations.load(request.OperationID)
		if err != nil {
			return err
		}
		if record.InputProfile != "" {
			if _, ok := executor.(boundMessageExecutor); !ok || record.InputProfile != serviceMessageInputProfile {
				return errors.New("operation_input_profile_requires_service_transport")
			}
			return validateServiceMessageInput(request.CapabilityID, request.Input)
		}
	}
	return ValidateCapabilityInput(request.CapabilityID, request.Input)
}

func beforeRemoteWrite(ctx context.Context) error {
	if err := checkExecutionBoundary(ctx); err != nil {
		return err
	}
	if work, ok := ctx.Value(executionWorkKey{}).(executionWork); ok {
		return work.repository.setExecutionPhase(work.item.ID, "writing")
	}
	return nil
}

func (adapter DirectExecutionAdapter) Send(ctx context.Context, request OutboxRequest) OutboxResult {
	sender := adapter.Sender
	if sender == nil {
		sender, _ = ctx.Value(executionSenderKey{}).(MessageSender)
	}
	return NewOutbox(adapter.DataRoot).processRequest(ctx, sender, request, false)
}

func (adapter DirectExecutionAdapter) ExecuteDocument(ctx context.Context, fallback CapabilityExecutor, request DocumentRequest) DocumentResult {
	transport := adapter.Documents
	if transport == nil {
		transport = fallback
	}
	return executeDocumentTransport(ctx, transport, request, false)
}

func (executor CapabilityExecutor) DocumentCreate(ctx context.Context, request DocumentRequest) (map[string]any, error) {
	return executor.documentCreate(ctx, request)
}
func (executor CapabilityExecutor) DocumentFetch(ctx context.Context, target DocumentTarget) (map[string]any, error) {
	return executor.documentFetch(ctx, target)
}
func (executor CapabilityExecutor) DocumentVersion(ctx context.Context, target DocumentTarget, preflight map[string]any, id string) (map[string]any, error) {
	return executor.documentVersion(ctx, target, preflight, id)
}
func (executor CapabilityExecutor) DocumentUpdate(ctx context.Context, request DocumentRequest) (map[string]any, error) {
	return executor.documentUpdate(ctx, request)
}

func outboxCapabilityInput(dataRoot string, request OutboxRequest) map[string]any {
	input := map[string]any{"request-id": request.ID, "target-type": request.Target.Type, "target-id": request.Target.ID, "format": request.Type, "source": request.Source}
	if request.Text != "" {
		input["text"] = request.Text
	}
	if request.FilePath != "" {
		relative, err := filepath.Rel(dataRoot, request.FilePath)
		if err == nil {
			input["file-path"] = filepath.ToSlash(relative)
		}
	}
	return input
}

func documentCapabilityInput(request DocumentRequest) (string, map[string]any) {
	id := "docs.service.document.create"
	input := map[string]any{"content": request.Content.Text, "format": request.Content.Format, "source": request.Source}
	if request.Target != nil {
		input["target-kind"], input["target-value"] = request.Target.Kind, request.Target.Value
	}
	if request.Action == "update_document" {
		id = "docs.service.document.append"
		if request.UpdateMode == "overwrite" || request.UpdateMode == "str_replace" {
			id = "docs.service.document.overwrite"
		}
		if request.UpdateMode == "str_replace" {
			input["selection-pattern"] = request.Selection["withEllipsis"]
		}
	}
	return id, input
}

func boundQueueInput(operations *OperationService, operationID, capabilityID string, input map[string]any) (map[string]any, error) {
	record, err := operations.Request(operationID)
	if err != nil {
		return nil, err
	}
	if dryRun, exists := record.Input["dry-run"]; exists {
		input["dry-run"] = dryRun
	}
	if err := operations.ValidateQueuedRequest(operationID, capabilityID, input); err != nil {
		return nil, err
	}
	return input, nil
}

func (box *Outbox) executeGoverned(ctx context.Context, sender MessageSender, request OutboxRequest, dryRun bool) OutboxResult {
	result := OutboxResult{ID: request.ID, OperationID: request.OperationID, Target: request.Target, CompletedAt: time.Now().UTC()}
	if dryRun || request.DryRun {
		return box.processRequest(ctx, sender, request, true)
	}
	operations := NewOperationService(box.dataRoot, NewCapabilityPolicyStore(box.dataRoot), nil)
	input, err := boundQueueInput(operations, request.OperationID, "im.sdk.message.send", outboxCapabilityInput(box.dataRoot, request))
	if err != nil {
		result.Status, result.Error = "failed", err.Error()
		return result
	}
	action := NewGovernedActionbox(box.dataRoot, operations).executeRequest(ctx, UnifiedCapabilityExecutor{DataRoot: box.dataRoot, Sender: sender}, queueActionRequest(request.ID, request.OperationID, "im.sdk.message.send", input))
	if response, ok := action.Result["response"]; ok {
		data, _ := json.Marshal(response)
		_ = json.Unmarshal(data, &result)
	} else {
		result.Status, result.Error = action.Status, action.Error
	}
	if action.Status != string(OperationSucceeded) {
		result.Status, result.Error = action.Status, action.Error
	}
	if action.Status == "dry_run" {
		result.DryRun = true
	}
	return result
}

func (box *Docbox) executeGoverned(ctx context.Context, executor CapabilityExecutor, request DocumentRequest, dryRun bool) DocumentResult {
	result := DocumentResult{ID: request.ID, OperationID: request.OperationID, Action: request.Action, CompletedAt: time.Now().UTC()}
	if err := validateDocumentRequest(request); err != nil {
		result.Status, result.Error = "invalid", err.Error()
		return result
	}
	if request.NewTitle != "" {
		result.Status, result.Error = "failed", "unbound_document_title"
		return result
	}
	if dryRun || request.DryRun {
		result.Status = "dry_run"
		return result
	}
	operations := NewOperationService(box.dataRoot, NewCapabilityPolicyStore(box.dataRoot), nil)
	id, input := documentCapabilityInput(request)
	input, err := boundQueueInput(operations, request.OperationID, id, input)
	if err != nil {
		result.Status, result.Error = "failed", err.Error()
		return result
	}
	action := NewGovernedActionbox(box.dataRoot, operations).executeRequest(ctx, UnifiedCapabilityExecutor{DataRoot: box.dataRoot, LongTail: executor}, queueActionRequest(request.ID, request.OperationID, id, input))
	if response, ok := action.Result["response"]; ok {
		data, _ := json.Marshal(response)
		_ = json.Unmarshal(data, &result)
	} else {
		result.Status, result.Error = action.Status, action.Error
	}
	result.ID = request.ID
	if action.Status != string(OperationSucceeded) {
		result.Status, result.Error = action.Status, action.Error
	}
	return result
}

func queueActionRequest(id, operationID, capabilityID string, input map[string]any) ActionRequest {
	definition, _ := CapabilityByID(capabilityID)
	return ActionRequest{ID: id, OperationID: operationID, Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: capabilityID, Identity: definition.Identity, Input: input, ExplicitAuthorization: true}
}
