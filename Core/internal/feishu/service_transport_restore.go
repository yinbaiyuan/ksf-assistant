package feishu

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

type ServiceMessageOwnershipVerifier interface {
	VerifyBotMessage(context.Context, MessageTarget, string) error
}

func CardRestorationRetryable(err error) bool {
	if err == nil || errors.Is(err, ErrOperationRequestMismatch) || errors.Is(err, context.Canceled) {
		return false
	}
	for _, code := range []string{"restoration_verifier_required", "restoration_message_missing", "restoration_not_bot_card", "restoration_message_get_failed", "invalid_restoration", "not authorized", "unsafe", "insecure"} {
		if strings.Contains(err.Error(), code) {
			return false
		}
	}
	return true
}

type cardRestoration struct {
	Version            int           `json:"version"`
	Target             MessageTarget `json:"target"`
	MessageFingerprint string        `json:"messageFingerprint"`
	Evidence           string        `json:"evidence"`
	VerifierIdentity   string        `json:"verifierIdentity"`
	CompletedAt        time.Time     `json:"completedAt"`
}

func (transport *ServiceTransport) RestoreCardBinding(ctx context.Context, target MessageTarget, messageID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	verifier, ok := transport.client.(ServiceMessageOwnershipVerifier)
	if !ok || verifier == nil || (reflect.ValueOf(verifier).Kind() == reflect.Ptr && reflect.ValueOf(verifier).IsNil()) {
		return errors.New("restoration_verifier_required")
	}
	identity := "injected-verifier"
	if official, ok := verifier.(*OfficialMessageClient); ok {
		if official.client == nil || official.appID == "" {
			return errors.New("restoration_verifier_required")
		}
		identity = official.appID
	}
	if strings.TrimSpace(messageID) == "" || len(messageID) > 400 {
		return errors.New("invalid_restoration_message")
	}
	if err := transport.authorize(target); err != nil {
		return err
	}
	path := transport.bindingPath(messageID)
	markerPath := filepath.Join(transport.root, "transport-restorations-v1", secretHash(messageID)+".json")
	return withProcessFileLock(path+".lock", func() error {
		var marker cardRestoration
		missingMarker, err := readPrivateJSON(markerPath, &marker)
		if err != nil {
			return err
		}
		if !missingMarker {
			if marker.Version != 1 || marker.Target != target || marker.MessageFingerprint != secretHash(messageID) {
				return ErrOperationRequestMismatch
			}
			binding, err := transport.readBinding(messageID)
			if err != nil {
				return err
			}
			if binding.Target != target || !binding.Writable {
				return ErrOperationRequestMismatch
			}
			if marker.Evidence == "cli-bot-owned" && marker.VerifierIdentity == identity {
				return nil
			}
		}
		var binding transportBinding
		missing, err := readPrivateJSON(path, &binding)
		if err != nil {
			return err
		}
		if !missing {
			if binding.Target != target || !binding.Writable {
				return ErrOperationRequestMismatch
			}
		}
		verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := verifier.VerifyBotMessage(verifyCtx, target, messageID); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := transport.authorize(target); err != nil {
			return err
		}
		if missing {
			if err := writePrivateJSON(path, transportBinding{Target: target, Writable: true, Restored: true}); err != nil {
				return err
			}
		}
		marker = cardRestoration{Version: 1, Target: target, MessageFingerprint: secretHash(messageID), Evidence: "cli-bot-owned", VerifierIdentity: identity, CompletedAt: time.Now().UTC()}
		return writePrivateJSON(markerPath, marker)
	})
}

func (client *OfficialMessageClient) VerifyBotMessage(ctx context.Context, target MessageTarget, messageID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if client == nil || client.client == nil || client.appID == "" {
		return errors.New("restoration_verifier_required")
	}
	if strings.TrimSpace(messageID) == "" || len(messageID) > 400 || strings.TrimSpace(target.ID) == "" || (target.Type != "chat_id" && target.Type != "open_id") {
		return errors.New("invalid_restoration_message")
	}

	message, err := client.ReadMessage(ctx, messageID)
	if err != nil {
		var failure *CLICommandError
		if errors.As(err, &failure) {
			if failure.HTTPStatus == 429 || failure.HTTPStatus >= 500 || failure.ExitCode == 4 {
				return fmt.Errorf("restoration_service_temporarily_unavailable: %w", err)
			}
			return fmt.Errorf("restoration_message_get_failed: %w", err)
		}
		return err
	}
	if message["msg_type"] != "interactive" || message["deleted"] == true {
		return errors.New("restoration_not_bot_card")
	}
	sender, _ := message["sender"].(map[string]any)
	if sender == nil || sender["sender_type"] != "app" || sender["id_type"] != "app_id" || sender["id"] != client.appID {
		return errors.New("restoration_not_bot_card")
	}
	if target.Type == "chat_id" && message["chat_id"] != target.ID {
		return ErrOperationRequestMismatch
	}
	return nil
}
