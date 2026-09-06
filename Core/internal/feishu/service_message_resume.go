package feishu

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (transport *ServiceTransport) ResumeOperation(ctx context.Context, id string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	record, err := NewOperationService(transport.root, NewCapabilityPolicyStore(transport.root), nil).Request(id)
	if err != nil {
		return "", err
	}
	if record.InputProfile != serviceMessageInputProfile {
		return "", ErrOperationRequestMismatch
	}
	if err := validateServiceMessageInput(record.CapabilityID, record.Input); err != nil {
		return "", err
	}
	var ledger transportExecution
	missing, err := readPrivateJSON(transport.executionPath(fmt.Sprint(record.Input["idempotency-key"])), &ledger)
	if err != nil {
		return "", err
	}
	if missing || ledger.OperationID != id {
		return "", ErrOperationRequestMismatch
	}
	return transport.executeMessage(ctx, record.CapabilityID, record.Input, func(callCtx context.Context) (string, error) {
		return transport.applyMessage(callCtx, record.CapabilityID, record.Input)
	})
}

func (transport *ServiceTransport) applyMessage(ctx context.Context, capability string, input map[string]any) (string, error) {
	target := MessageTarget{Type: fmt.Sprint(input["target-type"]), ID: fmt.Sprint(input["target-id"])}
	if err := transport.gate(target); err != nil {
		return "", err
	}
	format, content, key := fmt.Sprint(input["format"]), fmt.Sprint(input["content"]), fmt.Sprint(input["idempotency-key"])
	messageID, _ := input["message-id"].(string)
	boundary, ok := ctx.Value(executionBoundaryKey{}).(executionBoundary)
	if !ok {
		return "", ErrOperationRequestMismatch
	}
	if capability != "im.sdk.message.send" {
		binding, err := transport.readBinding(messageID)
		if err != nil {
			return "", err
		}
		if binding.Target != target {
			return "", ErrOperationRequestMismatch
		}
		if capability == "im.message.edit" {
			expected := "patch:" + secretHash(messageID+":"+fmt.Sprint(binding.Revision)+":"+secretHash(content))
			if !binding.Writable || expected != key || binding.OperationID == "" && !binding.Restored {
				return "", ErrOperationRequestMismatch
			}
			if err := transport.client.PatchCard(ctx, messageID, content); err != nil {
				return "", err
			}
			binding.OperationID, binding.LastPatchFingerprint, binding.Revision = boundary.operationID, secretHash(content), binding.Revision+1
			return messageID, writePrivateJSON(transport.bindingPath(messageID), binding)
		}
	}
	var err error
	switch capability {
	case "im.sdk.message.send":
		messageID, err = transport.client.Send(ctx, target, format, content, key)
	case "im.message.reply":
		messageID, err = transport.client.Reply(ctx, messageID, format, content, key)
	default:
		return "", errors.New("unsupported_service_message_capability")
	}
	if err != nil {
		return messageID, err
	}
	return messageID, transport.persistSentBinding(messageID, target, format, boundary.operationID)
}
