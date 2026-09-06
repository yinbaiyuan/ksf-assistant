package feishu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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
			if marker.Evidence == "sdk-bot-owned" && marker.VerifierIdentity == identity {
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
		marker = cardRestoration{Version: 1, Target: target, MessageFingerprint: secretHash(messageID), Evidence: "sdk-bot-owned", VerifierIdentity: identity, CompletedAt: time.Now().UTC()}
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
	response, err := client.client.Im.Message.Get(ctx, larkim.NewGetMessageReqBuilder().MessageId(messageID).Build())
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("restoration_message_missing")
	}
	if response.ApiResp != nil && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError) {
		return fmt.Errorf("restoration_service_temporarily_unavailable: status=%d", response.StatusCode)
	}
	if !response.Success() {
		return fmt.Errorf("restoration_message_get_failed: code=%d", response.Code)
	}
	if response.Data == nil || len(response.Data.Items) != 1 {
		return errors.New("restoration_message_missing")
	}
	message := response.Data.Items[0]
	if message == nil || message.MessageId == nil || *message.MessageId != messageID || message.MsgType == nil || *message.MsgType != "interactive" || (message.Deleted != nil && *message.Deleted) {
		return errors.New("restoration_not_bot_card")
	}
	sender := message.Sender
	if sender == nil || sender.SenderType == nil || *sender.SenderType != "app" || sender.IdType == nil || *sender.IdType != "app_id" || sender.Id == nil || *sender.Id != client.appID {
		return errors.New("restoration_not_bot_card")
	}
	if target.Type == "chat_id" && (message.ChatId == nil || *message.ChatId != target.ID) {
		return ErrOperationRequestMismatch
	}
	return nil
}
