package feishu

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type UnifiedCapabilityExecutor struct {
	LongTail  CapabilityExecutor
	DataRoot  string
	Direct    *DirectExecutionAdapter
	Sender    MessageSender
	Documents DocumentExecutionTransport
}

func (executor UnifiedCapabilityExecutor) ReadPreflight(ctx context.Context, id string, input map[string]any) (map[string]any, error) {
	if id == "docs.service.document.append" || id == "docs.service.document.overwrite" {
		return executor.documentSnapshot(ctx, input)
	}
	return executor.LongTail.ReadPreflight(ctx, id, input)
}

func (executor UnifiedCapabilityExecutor) ReadVerification(ctx context.Context, id string, input, response map[string]any) (VerificationAssessment, error) {
	if id == "docs.service.document.append" || id == "docs.service.document.overwrite" {
		evidence, err := executor.documentSnapshot(ctx, input)
		if err != nil {
			return VerificationAssessment{}, err
		}
		return VerificationAssessment{State: VerificationInconclusive, Evidence: evidence}, nil
	}
	return executor.LongTail.ReadVerification(ctx, id, input, response)
}

func (executor UnifiedCapabilityExecutor) ExecuteWithOptions(ctx context.Context, id string, input map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	definition, ok := CapabilityByID(id)
	if !ok {
		return nil, errors.New("unknown_capability")
	}
	if definition.Risk != "read" {
		if err := validateDirectBinding(ctx, id, input, options.OperationID); err != nil {
			return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
		}
	}
	if options.Timeout > 0 && definition.Identity != "user" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	if definition.Backend != "go-sdk" {
		if strings.HasPrefix(id, "docs.service.document.") {
			return executor.executeDocumentService(ctx, id, input, options)
		}
		if id == "docs.whiteboard.insert" {
			return executor.executeDocbox(ctx, id, input, options)
		}
		if definition.Risk != "read" {
			if err := beforeRemoteWrite(ctx); err != nil {
				return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
			}
		}
		return executor.LongTail.ExecuteWithOptions(ctx, id, input, options)
	}
	if id != "im.sdk.message.send" {
		return nil, errors.New("unsupported_go_sdk_capability")
	}
	if err := ValidateCapabilityInput(id, input); err != nil {
		return nil, err
	}
	dataRoot := executor.DataRoot
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = executor.LongTail.DataRoot
	}
	filePath := ""
	if relative, ok := input["file-path"].(string); ok && relative != "" {
		filePath = filepath.Join(dataRoot, filepath.FromSlash(relative))
		if err := validatePrivateMediaPath(dataRoot, filePath); err != nil {
			return nil, err
		}
	}
	request := OutboxRequest{
		ID: fmt.Sprint(input["request-id"]), OperationID: options.OperationID, Type: fmt.Sprint(input["format"]),
		Target: MessageTarget{Type: fmt.Sprint(input["target-type"]), ID: fmt.Sprint(input["target-id"])},
		Text:   fmt.Sprint(input["text"]), FilePath: filePath, ExplicitAuthorization: true,
		Source: fmt.Sprint(input["source"]), DryRun: input["dry-run"] == true, CreatedAt: time.Now().UTC(),
	}
	result := executor.directAdapter(dataRoot).Send(ctx, request)
	switch result.Status {
	case "sent", "dry_run":
		verificationState := string(VerificationInconclusive)
		if result.Status == "sent" {
			verificationState = string(VerificationConfirmed)
		}
		return map[string]any{"capabilityId": id, "response": result, "verified": result.Status == "sent", "verificationState": verificationState}, nil
	case string(OperationOutcomeUnknown), "partial_sent":
		return map[string]any{"capabilityId": id, "response": result}, &CapabilityExecutionError{Phase: "write", Err: errors.New("message_send_" + result.Status)}
	default:
		return nil, &CapabilityExecutionError{Phase: "remote_result", Err: errors.New("message_send_" + result.Status)}
	}
}

func (executor UnifiedCapabilityExecutor) documentSnapshot(ctx context.Context, input map[string]any) (map[string]any, error) {
	target, err := documentServiceTarget(input)
	if err != nil {
		return nil, err
	}
	if transport := executor.directAdapter(executor.DataRoot).Documents; transport != nil {
		return transport.DocumentFetch(ctx, target)
	}
	return executor.LongTail.documentFetch(ctx, target)
}

func documentServiceTarget(input map[string]any) (DocumentTarget, error) {
	target := DocumentTarget{Kind: fmt.Sprint(input["target-kind"]), Value: fmt.Sprint(input["target-value"])}
	if target.Kind == "" || target.Kind == "<nil>" || target.Value == "" || target.Value == "<nil>" {
		return DocumentTarget{}, errors.New("document_target_required")
	}
	if !contains([]string{"url", "docx_token", "wiki_url", "wiki_token", "folder_token"}, target.Kind) {
		return DocumentTarget{}, errors.New("document_target_invalid")
	}
	return target, nil
}

func (executor UnifiedCapabilityExecutor) executeDocumentService(ctx context.Context, id string, input map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	dataRoot := executor.DataRoot
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = executor.LongTail.DataRoot
	}
	requestID, err := NewDocboxID()
	if err != nil {
		return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
	}
	request := DocumentRequest{
		ID: requestID, OperationID: options.OperationID, Type: "document_task", Action: "create_document", Identity: "user",
		Content:     DocumentContent{Format: fmt.Sprint(input["format"]), Text: fmt.Sprint(input["content"])},
		Instruction: "创建飞书文档", ExplicitAuthorization: true, Source: fmt.Sprint(input["source"]), CreatedAt: time.Now().UTC(),
	}
	if _, hasTarget := input["target-value"]; hasTarget {
		target, targetErr := documentServiceTarget(input)
		if targetErr != nil {
			return nil, &CapabilityExecutionError{Phase: "preflight", Err: targetErr}
		}
		request.Target = &target
		request.Identity = documentIdentity(target)
	}
	if id != "docs.service.document.create" {
		request.Action = "update_document"
		request.Instruction = "先创建飞书官方版本，再更新并复读验证"
		request.VersionPolicy = "official_before_update"
		request.UpdateMode = "append"
		if id == "docs.service.document.overwrite" {
			request.UpdateMode = "overwrite"
			if pattern, ok := input["selection-pattern"].(string); ok && pattern != "" {
				request.UpdateMode = "str_replace"
				request.Selection = map[string]any{"withEllipsis": pattern}
			}
		}
	}
	return executor.executeDocumentDirect(ctx, id, dataRoot, request, options)
}

func (executor UnifiedCapabilityExecutor) executeDocumentDirect(ctx context.Context, id, dataRoot string, request DocumentRequest, options CapabilityExecutionOptions) (map[string]any, error) {
	result := executor.directAdapter(dataRoot).ExecuteDocument(ctx, executor.LongTail, request)
	switch result.Status {
	case "completed", "dry_run":
		return map[string]any{"capabilityId": id, "response": result, "verified": result.Verified, "verificationState": result.VerificationState}, nil
	case string(OperationOutcomeUnknown):
		phase := result.FailurePhase
		if phase == "" {
			phase = "verification"
		}
		return map[string]any{"capabilityId": id, "response": result, "verified": false, "verificationState": result.VerificationState}, &CapabilityExecutionError{Phase: phase, Err: errors.New("document_update_outcome_unknown")}
	default:
		if result.FailurePhase == "approval" {
			return nil, &CapabilityExecutionError{Phase: "approval", Err: &UserApprovalError{Code: result.Error}}
		}
		return nil, &CapabilityExecutionError{Phase: "remote_result", Err: errors.New("document_update_" + result.Status)}
	}
}

func (executor UnifiedCapabilityExecutor) executeDocbox(ctx context.Context, id string, input map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	dataRoot := executor.DataRoot
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = executor.LongTail.DataRoot
	}
	config, err := NewClientConfigStore(dataRoot).Load()
	if err != nil {
		return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
	}
	target, err := config.ResolveDocumentTarget(fmt.Sprint(input["doc"]))
	if err != nil {
		return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
	}
	content, err := DocWhiteboardXML(input)
	if err != nil {
		return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
	}
	requestID, err := NewDocboxID()
	if err != nil {
		return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
	}
	identity := "user"
	if target.Kind == "wiki_url" || target.Kind == "wiki_token" {
		identity = "bot"
	}
	request := DocumentRequest{
		ID: requestID, OperationID: options.OperationID, Type: "document_task", Action: "update_document", Identity: identity, Target: &target,
		Content:       DocumentContent{Format: "text", Text: content},
		Instruction:   "先读取目标文档并创建飞书官方版本，再追加 Whiteboard，完成后复读验证",
		VersionPolicy: "official_before_update", UpdateMode: "append", ExplicitAuthorization: true,
		Source: "capability-service", CreatedAt: time.Now().UTC(),
	}
	return executor.executeDocumentDirect(ctx, id, dataRoot, request, options)
}

func (executor UnifiedCapabilityExecutor) directAdapter(dataRoot string) DirectExecutionAdapter {
	if executor.Direct != nil {
		adapter := *executor.Direct
		if adapter.DataRoot == "" {
			adapter.DataRoot = dataRoot
		}
		return adapter
	}
	return DirectExecutionAdapter{DataRoot: dataRoot, Sender: executor.Sender, Documents: executor.Documents}
}
