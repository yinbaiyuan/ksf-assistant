package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const serviceMessageInputProfile = "service-message-v1"

type TransportAuthorizationError struct{ Prepared PreparedOperation }

func (err *TransportAuthorizationError) Error() string {
	return "service_message_confirmation_required: " + err.Prepared.Operation.ID
}

type transportExecution struct {
	Version     int       `json:"version"`
	OperationID string    `json:"operationId"`
	Fingerprint string    `json:"fingerprint"`
	Phase       string    `json:"phase"`
	Status      string    `json:"status,omitempty"`
	MessageID   string    `json:"messageId,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func validateServiceMessageInput(capabilityID string, input map[string]any) error {
	definition, known := CapabilityByID(capabilityID)
	if !known || !CapabilityPublished(definition) || !contains([]string{"im.sdk.message.send", "im.message.reply", "im.message.edit"}, capabilityID) {
		return errors.New("unsupported_service_message_capability")
	}
	for key, value := range input {
		if !contains([]string{"target-type", "target-id", "message-id", "format", "content", "idempotency-key"}, key) {
			return errors.New("invalid_service_message_field")
		}
		if _, ok := value.(string); !ok {
			return errors.New("invalid_service_message_value")
		}
	}
	for _, key := range []string{"target-type", "target-id", "format", "content", "idempotency-key"} {
		if strings.TrimSpace(fmt.Sprint(input[key])) == "" || input[key] == nil {
			return errors.New("missing_service_message_field")
		}
	}
	if !contains([]string{"open_id", "chat_id"}, fmt.Sprint(input["target-type"])) {
		return errors.New("invalid_service_message_target")
	}
	if len(fmt.Sprint(input["target-id"])) > 400 || len(fmt.Sprint(input["idempotency-key"])) > 400 || len(fmt.Sprint(input["content"])) > 512*1024 {
		return errors.New("service_message_too_large")
	}
	format := fmt.Sprint(input["format"])
	if !contains([]string{"text", "card", "post"}, format) {
		return errors.New("unsupported_service_message_format")
	}
	if format != "text" {
		var object map[string]any
		if json.Unmarshal([]byte(fmt.Sprint(input["content"])), &object) != nil || object == nil {
			return errors.New("invalid_service_message_content")
		}
	}
	if capabilityID == "im.sdk.message.send" {
		if input["message-id"] != nil {
			return errors.New("unexpected_service_reply_target")
		}
	} else {
		messageID, _ := input["message-id"].(string)
		if messageID == "" || len(messageID) > 400 {
			return errors.New("invalid_service_message_id")
		}
	}
	if capabilityID == "im.message.edit" && format != "card" {
		return errors.New("service_edit_requires_card")
	}
	return nil
}

func (service *OperationService) PrepareBoundMessage(capabilityID string, input map[string]any, source string) (PreparedOperation, error) {
	if err := validateServiceMessageInput(capabilityID, input); err != nil {
		return PreparedOperation{}, err
	}
	definition, _ := CapabilityByID(capabilityID)
	settings, err := NewSettingsStore(filepath.Dir(service.root)).Load()
	if err != nil {
		return PreparedOperation{}, err
	}
	if err := validateCapabilityRuntimeGate(definition, settings); err != nil {
		return PreparedOperation{}, err
	}
	view, challenge, err := service.prepareWithProfile(definition, input, source, nil, serviceMessageInputProfile)
	return PreparedOperation{Operation: view, Challenge: challenge}, err
}

func (transport *ServiceTransport) executionPath(key string) string {
	return filepath.Join(transport.root, "transport-executions-v1", secretHash(key)+".json")
}

func (transport *ServiceTransport) executeMessage(ctx context.Context, capabilityID string, input map[string]any, execute func(context.Context) (string, error)) (string, error) {
	if err := validateServiceMessageInput(capabilityID, input); err != nil {
		return "", err
	}
	path := transport.executionPath(fmt.Sprint(input["idempotency-key"]))
	fingerprint, err := operationFingerprint(capabilityID, input)
	if err != nil {
		return "", err
	}
	messageID := ""
	err = withProcessFileLock(path+".lock", func() error {
		operations := NewOperationService(transport.root, NewCapabilityPolicyStore(transport.root), nil)
		var ledger transportExecution
		missing, err := readPrivateJSON(path, &ledger)
		if err != nil {
			return err
		}
		if missing {
			prepared, err := operations.PrepareBoundMessage(capabilityID, input, "core-service-transport")
			if err != nil {
				return err
			}
			ledger = transportExecution{Version: 1, OperationID: prepared.Operation.ID, Fingerprint: fingerprint, Phase: "queued", UpdatedAt: time.Now().UTC()}
			if err := writePrivateJSON(path, ledger); err != nil {
				return err
			}
			if prepared.Operation.Status == OperationAwaitingConfirmation {
				return &TransportAuthorizationError{Prepared: prepared}
			}
		} else if ledger.Version != 1 || ledger.Fingerprint != fingerprint {
			return ErrOperationRequestMismatch
		}
		view, err := operations.Status(ledger.OperationID)
		if err != nil {
			return err
		}
		// Cancelled/expired reviews and local authorization contention can be
		// prepared afresh because neither condition can reach the remote write.
		// Keep the old audit record. Ordinary message effects never replay.
		replaceCard, _ := ctx.Value(cardReplacementKey{}).(bool)
		// A completed uncertain full-card edit can be superseded by a fresh
		// governed replacement. Never resume an in-flight operation or extend this
		// exception to sends/replies. The original audit receipt stays unchanged.
		if serviceMessageCanReprepare(view, ledger) || (replaceCard && capabilityID == "im.message.edit" && view.Status == OperationOutcomeUnknown && ledger.Phase == "terminal") {
			prepared, err := operations.PrepareBoundMessage(capabilityID, input, "core-service-transport")
			if err != nil {
				return err
			}
			ledger.OperationID = prepared.Operation.ID
			ledger.Phase = "queued"
			ledger.Status = ""
			ledger.MessageID = ""
			ledger.UpdatedAt = time.Now().UTC()
			if err := writePrivateJSON(path, ledger); err != nil {
				return err
			}
			if prepared.Operation.Status == OperationAwaitingConfirmation {
				return &TransportAuthorizationError{Prepared: prepared}
			}
			view = prepared.Operation
		}
		if view.Status == OperationAwaitingConfirmation {
			prepared, err := operations.renewBoundMessageConfirmation(view.ID)
			if err != nil {
				return err
			}
			return &TransportAuthorizationError{Prepared: prepared}
		}
		if view.Status == OperationSucceeded {
			record, err := operations.load(ledger.OperationID)
			if err != nil {
				return err
			}
			messageID, _ = record.Result["messageID"].(string)
			return nil
		}
		if view.Status == OperationRunning || view.Status == OperationVerifying || ledger.Phase == "writing" {
			if view.Status == OperationQueued || view.Status == OperationRunning || view.Status == OperationVerifying {
				if _, err := operations.MarkOutcomeUnknown(ledger.OperationID, "transport_interrupted"); err != nil {
					return err
				}
			}
			return errors.New("transport_outcome_unknown_do_not_replay")
		}
		if view.Status != OperationQueued {
			return fmt.Errorf("transport_operation_%s: %s", view.Status, view.ID)
		}
		result, err := operations.ExecuteBoundMessage(ctx, ledger.OperationID, capabilityID, input, func(callCtx context.Context) (map[string]any, error) {
			if err := ValidateMessageExecution(callCtx, capabilityID, input); err != nil {
				return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
			}
			ledger.Phase, ledger.UpdatedAt = "writing", time.Now().UTC()
			if err := writePrivateJSON(path, ledger); err != nil {
				return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
			}
			messageID, err = execute(callCtx)
			if err != nil {
				return map[string]any{"messageID": messageID}, &CapabilityExecutionError{Phase: "write", Err: err}
			}
			return map[string]any{"messageID": messageID}, nil
		})
		ledger.Phase, ledger.Status, ledger.MessageID, ledger.UpdatedAt = "terminal", result.Status, messageID, time.Now().UTC()
		if persistErr := writePrivateJSON(path, ledger); persistErr != nil {
			return errors.Join(err, persistErr)
		}
		return err
	})
	return messageID, err
}

func serviceMessageCanReprepare(view OperationView, ledger transportExecution) bool {
	if (view.Status == OperationExpired || view.Status == OperationCancelled) && view.AttemptCount == 0 && ledger.Phase == "queued" {
		return true
	}
	return view.Status == OperationFailed && view.ErrorCode == "approval_authorization_busy" && ledger.Phase == "terminal" && ledger.Status == "failed"
}

func (transport *ServiceTransport) Send(ctx context.Context, target MessageTarget, format, value, id string) (string, error) {
	part, ok := ctx.Value(executionMessagePartKey{}).(executionMessagePart)
	boundary, bound := ctx.Value(executionBoundaryKey{}).(executionBoundary)
	if !ok || !bound || boundary.operations.root != filepath.Join(transport.root, "operations") || part.target != target || part.format != format || part.value != value || part.id != id || boundary.capabilityID != "im.sdk.message.send" {
		return "", ErrOperationRequestMismatch
	}
	if err := beforeRemoteWrite(ctx); err != nil {
		return "", err
	}
	if err := transport.gate(target); err != nil {
		return "", err
	}
	path := transport.executionPath("part:" + id)
	fingerprint, _ := operationFingerprint(boundary.capabilityID, map[string]any{"target": target, "format": format, "value": value, "operationId": boundary.operationID})
	messageID := ""
	err := withProcessFileLock(path+".lock", func() error {
		var ledger transportExecution
		missing, err := readPrivateJSON(path, &ledger)
		if err != nil {
			return err
		}
		if !missing {
			if ledger.Fingerprint != fingerprint || ledger.OperationID != boundary.operationID {
				return ErrOperationRequestMismatch
			}
			if ledger.Phase == "terminal" && ledger.Status == "sent" {
				messageID = ledger.MessageID
				return nil
			}
			return &CapabilityExecutionError{Phase: "write", Err: errors.New("transport_part_outcome_unknown")}
		}
		ledger = transportExecution{Version: 1, OperationID: boundary.operationID, Fingerprint: fingerprint, Phase: "writing", UpdatedAt: time.Now().UTC()}
		if err := writePrivateJSON(path, ledger); err != nil {
			return err
		}
		if err := beforeRemoteWrite(ctx); err != nil {
			return err
		}
		if err := transport.gate(target); err != nil {
			return err
		}
		messageID, err = transport.client.Send(ctx, target, format, value, id)
		if err != nil {
			return &CapabilityExecutionError{Phase: "write", Err: err}
		}
		if err := transport.persistSentBinding(messageID, target, format, boundary.operationID); err != nil {
			return &CapabilityExecutionError{Phase: "write", Err: err}
		}
		ledger.Phase, ledger.Status, ledger.MessageID, ledger.UpdatedAt = "terminal", "sent", messageID, time.Now().UTC()
		if err := writePrivateJSON(path, ledger); err != nil {
			return &CapabilityExecutionError{Phase: "write", Err: err}
		}
		return nil
	})
	return messageID, err
}

func (transport *ServiceTransport) persistSentBinding(messageID string, target MessageTarget, format, operationID string) error {
	if messageID == "" {
		return errors.New("transport_message_id_missing")
	}
	path := transport.bindingPath(messageID)
	return withProcessFileLock(path+".lock", func() error {
		var current transportBinding
		missing, err := readPrivateJSON(path, &current)
		if err != nil {
			return err
		}
		if !missing {
			if current.Target != target || current.OperationID != operationID {
				return ErrOperationRequestMismatch
			}
			return nil
		}
		return writePrivateJSON(path, transportBinding{Target: target, Writable: format == "card", OperationID: operationID})
	})
}

// A repeated explicit delivery request may need a fresh desktop review after the
// previous host lost its response. Keep the operation and frozen input; invalidate
// the prior challenge without approving or dispatching any external effect.
func (service *OperationService) renewBoundMessageConfirmation(id string) (PreparedOperation, error) {
	var prepared PreparedOperation
	err := service.update(id, func(record *OperationRecord) error {
		if record.InputProfile != serviceMessageInputProfile || record.Status != OperationAwaitingConfirmation || record.ChallengeExpiresAt == nil || !service.now().Before(*record.ChallengeExpiresAt) {
			return errors.New("confirmation_not_pending")
		}
		policy, err := service.policy.Load()
		if err != nil {
			return err
		}
		if policy.Revision != record.PolicyRevision || policy.Decision(CapabilityDefinition{ID: record.CapabilityID, Risk: record.Risk}) != CapabilityConfirmEach {
			return errors.New("capability_policy_changed")
		}
		token, err := randomSecret(24)
		if err != nil {
			return err
		}
		record.ChallengeHash = secretHash(token)
		record.UpdatedAt = service.now().UTC()
		prepared = PreparedOperation{Operation: publicOperation(*record), Challenge: token}
		return nil
	})
	return prepared, err
}
