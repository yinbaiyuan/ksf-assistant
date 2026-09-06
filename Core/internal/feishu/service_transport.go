package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

type ServiceMessageClient interface {
	Send(context.Context, MessageTarget, string, string, string) (string, error)
	Reply(context.Context, string, string, string, string) (string, error)
	PatchCard(context.Context, string, string) error
}

type transportBinding struct {
	Target               MessageTarget `json:"target"`
	Writable             bool          `json:"writable"`
	OperationID          string        `json:"operationId,omitempty"`
	Revision             uint64        `json:"revision,omitempty"`
	LastPatchFingerprint string        `json:"lastPatchFingerprint,omitempty"`
	Restored             bool          `json:"restored,omitempty"`
	InboundFingerprint   string        `json:"inboundFingerprint,omitempty"`
}

type ServiceTransport struct {
	root   string
	client ServiceMessageClient
	mu     sync.Mutex
}

func NewServiceTransport(root string, client ServiceMessageClient) *ServiceTransport {
	return &ServiceTransport{root: root, client: client}
}

func (transport *ServiceTransport) bindingPath(messageID string) string {
	digest := sha256.Sum256([]byte(messageID))
	return filepath.Join(transport.root, "transport-bindings-v1", hex.EncodeToString(digest[:])+".json")
}

func (transport *ServiceTransport) readBinding(messageID string) (transportBinding, error) {
	var binding transportBinding
	missing, err := readPrivateJSON(transport.bindingPath(messageID), &binding)
	if err != nil {
		return binding, err
	}
	if missing || messageID == "" {
		return binding, errors.New("unbound Feishu message")
	}
	return binding, nil
}

func (transport *ServiceTransport) authorize(target MessageTarget) error {
	config, err := NewClientConfigStore(transport.root).Load()
	if err != nil {
		return err
	}
	for alias, candidate := range config.MessageTargets {
		if candidate != target {
			continue
		}
		if target.Type == "chat_id" {
			return nil
		}
		for _, allowed := range config.DirectAllowedAliases {
			if alias == allowed {
				return nil
			}
		}
	}
	return errors.New("Feishu target is not authorized")
}

func (transport *ServiceTransport) gate(target MessageTarget) error {
	if transport.client == nil {
		return errors.New("Feishu outbound unavailable")
	}
	settings, err := NewSettingsStore(transport.root).Load()
	if err != nil {
		return err
	}
	if !settings.Outbound.Enabled || settings.Outbound.DryRun {
		return errors.New("Feishu outbound disabled or dry-run")
	}
	if target.Type == "chat_id" && !settings.Group.Enabled {
		return errors.New("Feishu group interaction disabled")
	}
	return transport.authorize(target)
}

func (transport *ServiceTransport) BindInbound(message InboundMessage) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	target := MessageTarget{Type: "chat_id", ID: message.ChatID}
	if message.ChatType == "p2p" {
		target = MessageTarget{Type: "open_id", ID: message.SenderOpenID}
	}
	if err := transport.authorize(target); err != nil {
		return err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	path := transport.bindingPath(message.MessageID)
	return withProcessFileLock(path+".lock", func() error {
		var current transportBinding
		missing, err := readPrivateJSON(path, &current)
		if err != nil {
			return err
		}
		fingerprint := hex.EncodeToString(digest[:])
		if !missing {
			if current.Target != target || current.InboundFingerprint != fingerprint {
				return ErrOperationRequestMismatch
			}
			return nil
		}
		return writePrivateJSON(path, transportBinding{Target: target, InboundFingerprint: fingerprint})
	})
}

func (transport *ServiceTransport) BindCard(card InboundCardAction) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	target := MessageTarget{Type: "open_id", ID: card.OperatorOpenID}
	if err := transport.authorize(target); err != nil {
		return err
	}
	if prior, err := transport.readBinding(card.MessageID); err == nil {
		if prior.Target != target || !prior.Writable {
			return errors.New("Feishu card binding mismatch")
		}
		return nil
	}
	return errors.New("card_requires_existing_governed_message_binding")
}

func (transport *ServiceTransport) CheckInbound(message InboundMessage) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	binding, err := transport.readBinding(message.MessageID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	if binding.InboundFingerprint != hex.EncodeToString(digest[:]) {
		return errors.New("inbound resource binding mismatch")
	}
	return transport.authorize(binding.Target)
}

func (transport *ServiceTransport) Message(ctx context.Context, reply bool, request feishuprotocol.MessageRequest) (feishuprotocol.MessageResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	target := MessageTarget{Type: request.TargetType, ID: request.TargetID}
	capabilityID := "im.sdk.message.send"
	if reply {
		binding, err := transport.readBinding(request.MessageID)
		if err != nil {
			return feishuprotocol.MessageResult{}, err
		}
		target, capabilityID = binding.Target, "im.message.reply"
	}
	if err := transport.gate(target); err != nil {
		return feishuprotocol.MessageResult{}, err
	}
	input := map[string]any{"target-type": target.Type, "target-id": target.ID, "format": request.Format, "content": request.Content, "idempotency-key": request.IdempotencyKey}
	if reply {
		input["message-id"] = request.MessageID
	}
	messageID, err := transport.executeMessage(ctx, capabilityID, input, func(callCtx context.Context) (string, error) {
		return transport.applyMessage(callCtx, capabilityID, input)
	})
	return feishuprotocol.MessageResult{MessageID: messageID}, err
}

func (transport *ServiceTransport) Patch(ctx context.Context, request feishuprotocol.CardRequest) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	binding, err := transport.readBinding(request.MessageID)
	if err != nil {
		return err
	}
	if !binding.Writable || binding.OperationID == "" && !binding.Restored {
		return errors.New("message is not a governed writable card")
	}
	if err := transport.gate(binding.Target); err != nil {
		return err
	}
	fingerprint := secretHash(request.Content)
	if binding.LastPatchFingerprint == fingerprint {
		view, err := NewOperationService(transport.root, NewCapabilityPolicyStore(transport.root), nil).Status(binding.OperationID)
		if err != nil {
			return err
		}
		if view.Status != OperationSucceeded {
			return errors.New("transport_patch_outcome_unconfirmed")
		}
		return nil
	}
	key := "patch:" + secretHash(request.MessageID+":"+fmt.Sprint(binding.Revision)+":"+fingerprint)
	input := map[string]any{"target-type": binding.Target.Type, "target-id": binding.Target.ID, "message-id": request.MessageID, "format": "card", "content": request.Content, "idempotency-key": key}
	_, err = transport.executeMessage(ctx, "im.message.edit", input, func(callCtx context.Context) (string, error) {
		return transport.applyMessage(callCtx, "im.message.edit", input)
	})
	return err
}
