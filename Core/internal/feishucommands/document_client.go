package feishucommands

import (
	"errors"
	"time"

	"ksfassistant/core/internal/feishu"
)

func (call *invocation) runDocumentClient(dataRoot string, service *feishu.CapabilityService, arguments []string, write clientJSONWriter) error {
	if len(arguments) < 1 || arguments[0] != "create" && arguments[0] != "update" {
		return errors.New("doc action must be create or update")
	}
	flags := arguments[1:]
	content, err := call.clientPrivateValue(flags, "--content-file")
	if err != nil {
		return err
	}
	format := clientOptionalFlag(flags, "--format")
	if format == "" {
		format = "markdown"
	}
	config, err := feishu.NewClientConfigStore(dataRoot).Load()
	if err != nil {
		return err
	}
	input := map[string]any{
		"content": string(content), "format": format, "source": config.DefaultSource,
		"dry-run": clientHasFlag(flags, "--dry-run"),
	}
	capabilityID := "docs.service.document.create"
	if targetValue := clientOptionalFlag(flags, "--target"); targetValue != "" {
		target, resolveErr := config.ResolveDocumentTarget(targetValue)
		if resolveErr != nil {
			return resolveErr
		}
		input["target-kind"], input["target-value"] = target.Kind, target.Value
	}
	if arguments[0] == "update" {
		if _, ok := input["target-value"]; !ok {
			return errors.New("missing --target")
		}
		mode := clientOptionalFlag(flags, "--mode")
		if mode == "" || mode == "append" {
			capabilityID = "docs.service.document.append"
		} else if mode == "overwrite" || mode == "str_replace" {
			capabilityID = "docs.service.document.overwrite"
			if mode == "str_replace" {
				pattern, patternErr := call.clientPrivateValue(flags, "--pattern-file")
				if patternErr != nil {
					return patternErr
				}
				input["selection-pattern"] = string(pattern)
			}
		} else {
			return errors.New("document update mode must be append, overwrite, or str_replace")
		}
	}
	prepared, err := call.prepare(service, call.ctx, capabilityID, input, config.DefaultSource)
	if err != nil {
		advice := feishu.CapabilityServiceErrorAdvice(err)
		return write(map[string]any{"status": "rejected", "errorCode": advice.ErrorCode, "nextAction": advice.NextAction})
	}
	if prepared.Submitted {
		_ = feishu.WakeQueue(dataRoot, "actionbox")
	}
	if prepared.Operation.Status == feishu.OperationAwaitingConfirmation || clientHasFlag(flags, "--async") {
		return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": prepared.Operation, "challenge": prepared.Challenge, "submitted": prepared.Submitted}))
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		view, statusErr := service.Status(prepared.Operation.ID)
		if statusErr != nil {
			return statusErr
		}
		if operationTerminal(view.Status) {
			return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": view}))
		}
		if err := call.wait(250 * time.Millisecond); err != nil {
			return err
		}
	}
	view, _ := service.Status(prepared.Operation.ID)
	return write(feishu.PublicResult(map[string]any{"status": "pending", "operation": view, "nextAction": operationPendingNextAction(view)}))
}
