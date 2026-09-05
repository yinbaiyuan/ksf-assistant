package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"codexusagebar/core/internal/feishu"
)

func writeCapabilityRejection(write clientJSONWriter, err error) error {
	advice := feishu.CapabilityServiceErrorAdvice(err)
	return write(map[string]any{"status": "rejected", "errorCode": advice.ErrorCode, "nextAction": advice.NextAction})
}

func operationPendingNextAction(view feishu.OperationView) string {
	if view.Status == feishu.OperationOutcomeUnknown && view.NextAction != "" {
		return view.NextAction
	}
	return "query_same_operation"
}

type clientJSONWriter func(any) error

func operationTerminal(status feishu.OperationStatus) bool {
	switch status {
	case feishu.OperationSucceeded, feishu.OperationFailed, feishu.OperationExpired, feishu.OperationCancelled, feishu.OperationOutcomeUnknown:
		return true
	default:
		return false
	}
}

func runOperationClient(dataRoot string, service *feishu.CapabilityService, arguments []string, write clientJSONWriter) error {
	if len(arguments) == 0 {
		return errors.New("missing operation action")
	}
	switch arguments[0] {
	case "prepare":
		if len(arguments) < 2 {
			return errors.New("missing capability id")
		}
		payload, err := clientPayload(arguments[2:])
		if err != nil {
			return err
		}
		var input map[string]any
		if err := json.Unmarshal(payload, &input); err != nil {
			return errors.New("invalid capability payload")
		}
		source := clientOptionalFlag(arguments[2:], "--source")
		if source == "" {
			source = "codex"
		}
		result, err := service.Prepare(context.Background(), arguments[1], input, source)
		if err != nil {
			advice := feishu.CapabilityServiceErrorAdvice(err)
			return write(map[string]any{"status": "rejected", "errorCode": advice.ErrorCode, "nextAction": advice.NextAction})
		}
		if result.Submitted {
			_ = feishu.WakeQueue(dataRoot, "actionbox")
		}
		return write(map[string]any{"status": "ok", "operation": result.Operation, "challenge": result.Challenge, "submitted": result.Submitted, "result": result.Result})
	case "confirm":
		id, err := clientFlag(arguments[1:], "--id")
		if err != nil {
			return err
		}
		challengeBytes, err := clientPrivateValue(arguments[1:], "--challenge-file")
		if err != nil {
			return err
		}
		result, err := service.Confirm(context.Background(), id, strings.TrimSpace(string(challengeBytes)))
		if err != nil {
			if result.Operation.ID != "" && operationTerminal(result.Operation.Status) {
				return write(map[string]any{"status": "rejected", "operation": result.Operation, "submitted": false, "errorCode": result.Operation.ErrorCode, "nextAction": result.Operation.NextAction})
			}
			advice := feishu.CapabilityServiceErrorAdvice(err)
			return write(map[string]any{"status": "rejected", "operation": result.Operation, "submitted": false, "errorCode": advice.ErrorCode, "nextAction": advice.NextAction})
		}
		if result.Submitted {
			_ = feishu.WakeQueue(dataRoot, "actionbox")
		}
		return write(map[string]any{"status": "ok", "operation": result.Operation, "submitted": result.Submitted, "result": result.Result})
	case "cancel":
		id, err := clientFlag(arguments[1:], "--id")
		if err != nil {
			return err
		}
		view, err := service.Cancel(id)
		if err != nil {
			return err
		}
		return write(map[string]any{"status": "ok", "operation": view})
	case "status":
		id, err := clientFlag(arguments[1:], "--id")
		if err != nil {
			return err
		}
		view, err := service.Status(id)
		if err != nil {
			return err
		}
		return write(map[string]any{"status": "ok", "operation": view})
	default:
		return errors.New("operation action must be prepare, confirm, cancel, or status")
	}
}

func runPolicyClient(service *feishu.CapabilityService, arguments []string, write clientJSONWriter) error {
	if len(arguments) == 0 {
		return errors.New("missing policy action")
	}
	switch arguments[0] {
	case "read":
		policy, err := service.ReadPolicy()
		if err != nil {
			return err
		}
		return write(map[string]any{"status": "ok", "policy": policy})
	case "update":
		payload, err := clientPayload(arguments[1:])
		if err != nil {
			return err
		}
		var policy feishu.CapabilityPolicy
		if err := json.Unmarshal(payload, &policy); err != nil {
			return errors.New("invalid capability policy payload")
		}
		revisionText, err := clientFlag(arguments[1:], "--expected-revision")
		if err != nil {
			return err
		}
		revision, err := strconv.ParseUint(revisionText, 10, 64)
		if err != nil {
			return errors.New("invalid expected revision")
		}
		saved, err := service.UpdatePolicy(policy, revision)
		if err != nil {
			return err
		}
		return write(map[string]any{"status": "updated", "policy": saved})
	default:
		return errors.New("policy action must be read or update")
	}
}
