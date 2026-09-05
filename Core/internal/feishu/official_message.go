package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type OfficialMessageClient struct {
	client *lark.Client
	appID  string
}

func NewOfficialMessageClient(appID, appSecret string, options ...lark.ClientOptionFunc) (*OfficialMessageClient, error) {
	if strings.TrimSpace(appID) == "" || appSecret == "" {
		return nil, errors.New("official SDK credentials are required")
	}
	base := []lark.ClientOptionFunc{lark.WithLogLevel(larkcore.LogLevelError), lark.WithLogger(discardSDKLogger{})}
	base = append(base, options...)
	return &OfficialMessageClient{client: lark.NewClient(strings.TrimSpace(appID), appSecret, base...), appID: strings.TrimSpace(appID)}, nil
}

func (client *OfficialMessageClient) Send(ctx context.Context, target MessageTarget, format, value, idempotencyKey string) (string, error) {
	msgType, content, err := client.prepareMessageContent(ctx, format, value)
	if err != nil {
		return "", err
	}
	if target.Type != "chat_id" && target.Type != "open_id" {
		return "", errors.New("unsupported_target_type")
	}
	body := larkim.NewCreateMessageReqBodyBuilder().ReceiveId(target.ID).MsgType(msgType).Content(content).Uuid(idempotencyKey).Build()
	req := larkim.NewCreateMessageReqBuilder().ReceiveIdType(target.Type).Body(body).Build()
	response, err := client.client.Im.Message.Create(ctx, req)
	if err != nil {
		return "", err
	}
	if !response.Success() {
		return "", fmt.Errorf("Feishu message create failed: code=%d", response.Code)
	}
	if response.Data == nil || response.Data.MessageId == nil || *response.Data.MessageId == "" {
		return "", errors.New("Feishu message create returned no message id")
	}
	return *response.Data.MessageId, nil
}

func (client *OfficialMessageClient) prepareMessageContent(ctx context.Context, format, value string) (string, string, error) {
	if format != "image" && format != "file" {
		return officialMessageContent(format, value)
	}
	info, err := os.Lstat(value)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 {
		return "", "", errors.New("unsafe_media_file")
	}
	file, err := os.Open(value)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	if format == "image" {
		if info.Size() > 10*1024*1024 {
			return "", "", errors.New("image_too_large")
		}
		body := larkim.NewCreateImageReqBodyBuilder().ImageType("message").Image(file).Build()
		response, err := client.client.Im.Image.Create(ctx, larkim.NewCreateImageReqBuilder().Body(body).Build())
		if err != nil {
			return "", "", err
		}
		if !response.Success() || response.Data == nil || response.Data.ImageKey == nil {
			return "", "", fmt.Errorf("Feishu image upload failed: code=%d", response.Code)
		}
		data, _ := json.Marshal(map[string]string{"image_key": *response.Data.ImageKey})
		return "image", string(data), nil
	}
	if info.Size() > 30*1024*1024 {
		return "", "", errors.New("file_too_large")
	}
	body := larkim.NewCreateFileReqBodyBuilder().FileType("stream").FileName(filepath.Base(value)).File(file).Build()
	response, err := client.client.Im.File.Create(ctx, larkim.NewCreateFileReqBuilder().Body(body).Build())
	if err != nil {
		return "", "", err
	}
	if !response.Success() || response.Data == nil || response.Data.FileKey == nil {
		return "", "", fmt.Errorf("Feishu file upload failed: code=%d", response.Code)
	}
	data, _ := json.Marshal(map[string]string{"file_key": *response.Data.FileKey})
	return "file", string(data), nil
}

func (client *OfficialMessageClient) Reply(ctx context.Context, messageID, format, value, idempotencyKey string) (string, error) {
	msgType, content, err := officialMessageContent(format, value)
	if err != nil {
		return "", err
	}
	body := larkim.NewReplyMessageReqBodyBuilder().MsgType(msgType).Content(content).Uuid(idempotencyKey).Build()
	req := larkim.NewReplyMessageReqBuilder().MessageId(messageID).Body(body).Build()
	response, err := client.client.Im.Message.Reply(ctx, req)
	if err != nil {
		return "", err
	}
	if !response.Success() {
		return "", fmt.Errorf("Feishu message reply failed: code=%d", response.Code)
	}
	if response.Data == nil || response.Data.MessageId == nil || *response.Data.MessageId == "" {
		return "", errors.New("Feishu message reply returned no message id")
	}
	return *response.Data.MessageId, nil
}

func (client *OfficialMessageClient) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	var card map[string]any
	if json.Unmarshal([]byte(cardJSON), &card) != nil {
		return errors.New("invalid_card_json")
	}
	body := larkim.NewPatchMessageReqBodyBuilder().Content(cardJSON).Build()
	req := larkim.NewPatchMessageReqBuilder().MessageId(messageID).Body(body).Build()
	response, err := client.client.Im.Message.Patch(ctx, req)
	if err != nil {
		return err
	}
	if !response.Success() {
		return fmt.Errorf("Feishu message patch failed: code=%d", response.Code)
	}
	return nil
}

func officialMessageContent(format, value string) (string, string, error) {
	switch format {
	case "text":
		data, _ := json.Marshal(map[string]string{"text": value})
		return "text", string(data), nil
	case "markdown":
		card := map[string]any{"schema": "2.0", "body": map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": value}}}}
		data, _ := json.Marshal(card)
		return "interactive", string(data), nil
	case "card":
		var card map[string]any
		if json.Unmarshal([]byte(value), &card) != nil {
			return "", "", errors.New("invalid_card_json")
		}
		return "interactive", value, nil
	default:
		return "", "", errors.New("unsupported_message_format")
	}
}
