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
	LongTail CapabilityExecutor
	DataRoot string
	Direct   *DirectExecutionAdapter
	Sender   MessageSender
}

func (executor UnifiedCapabilityExecutor) ReadPreflight(ctx context.Context, id string, input map[string]any) (map[string]any, error) {
	return executor.LongTail.ReadPreflight(ctx, id, input)
}

func (executor UnifiedCapabilityExecutor) ReadVerification(ctx context.Context, id string, input, response map[string]any) (VerificationAssessment, error) {
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

func (executor UnifiedCapabilityExecutor) directAdapter(dataRoot string) DirectExecutionAdapter {
	if executor.Direct != nil {
		adapter := *executor.Direct
		if adapter.DataRoot == "" {
			adapter.DataRoot = dataRoot
		}
		return adapter
	}
	return DirectExecutionAdapter{DataRoot: dataRoot, Sender: executor.Sender}
}
