package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type MessageCLIRequest struct {
	Resource string
	Method   string
	Params   map[string]string
	Body     map[string]any
	File     string
}

type MessageCLI interface {
	CallMessage(context.Context, MessageCLIRequest) (map[string]any, error)
}

type OfficialMessageClient struct {
	client MessageCLI
	appID  string
}

func NewOfficialMessageClient(appID string, runner MessageCLI) (*OfficialMessageClient, error) {
	if strings.TrimSpace(appID) == "" || runner == nil {
		return nil, errors.New("official CLI application identity and runner are required")
	}
	return &OfficialMessageClient{client: runner, appID: strings.TrimSpace(appID)}, nil
}

func (client *OfficialMessageClient) Send(ctx context.Context, target MessageTarget, format, value, idempotencyKey string) (string, error) {
	if target.Type != "chat_id" && target.Type != "open_id" {
		return "", errors.New("unsupported_target_type")
	}
	msgType, content, err := client.prepareMessageContent(ctx, format, value)
	if err != nil {
		return "", err
	}
	result, err := client.client.CallMessage(ctx, MessageCLIRequest{Resource: "messages", Method: "create", Params: map[string]string{"receive_id_type": target.Type}, Body: map[string]any{"receive_id": target.ID, "msg_type": msgType, "content": content, "uuid": idempotencyKey}})
	return messageResultID(result, err)
}

func (client *OfficialMessageClient) prepareMessageContent(ctx context.Context, format, value string) (string, string, error) {
	if format != "image" && format != "file" {
		return officialMessageContent(format, value)
	}
	request := MessageCLIRequest{Resource: "images", Method: "create", Body: map[string]any{"image_type": "message"}, File: value}
	key := "image_key"
	if format == "file" {
		request.Resource = "files"
		request.Body = map[string]any{"file_type": "stream"}
		key = "file_key"
	}
	result, err := client.client.CallMessage(ctx, request)
	if err != nil {
		return "", "", err
	}
	remoteKey, _ := result[key].(string)
	if remoteKey == "" {
		return "", "", errors.New("CLI media upload returned no resource key")
	}
	content, _ := json.Marshal(map[string]string{key: remoteKey})
	return format, string(content), nil
}

func (client *OfficialMessageClient) Reply(ctx context.Context, messageID, format, value, idempotencyKey string) (string, error) {
	msgType, content, err := client.prepareMessageContent(ctx, format, value)
	if err != nil {
		return "", err
	}
	result, err := client.client.CallMessage(ctx, MessageCLIRequest{Resource: "messages", Method: "reply", Params: map[string]string{"message_id": messageID}, Body: map[string]any{"msg_type": msgType, "content": content, "uuid": idempotencyKey}})
	return messageResultID(result, err)
}

func messageResultID(result map[string]any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	messageID, _ := result["message_id"].(string)
	if messageID == "" {
		return "", errors.New("CLI message operation returned no message id")
	}
	return messageID, nil
}

func (client *OfficialMessageClient) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	var card map[string]any
	if json.Unmarshal([]byte(cardJSON), &card) != nil || card == nil {
		return errors.New("invalid_card_json")
	}
	_, err := client.client.CallMessage(ctx, MessageCLIRequest{Resource: "messages", Method: "patch", Params: map[string]string{"message_id": messageID}, Body: map[string]any{"content": cardJSON}})
	return err
}

func (client *OfficialMessageClient) ReadMessage(ctx context.Context, messageID string) (map[string]any, error) {
	if messageID == "" || len(messageID) > 400 {
		return nil, errors.New("invalid_message_id")
	}
	result, err := client.client.CallMessage(ctx, MessageCLIRequest{Resource: "messages", Method: "get", Params: map[string]string{"message_id": messageID}})
	if err != nil {
		return nil, err
	}
	items, _ := result["items"].([]any)
	if len(items) != 1 {
		return nil, errors.New("restoration_message_missing")
	}
	message, _ := items[0].(map[string]any)
	if message == nil || message["message_id"] != messageID {
		return nil, errors.New("restoration_message_missing")
	}
	return message, nil
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
		if json.Unmarshal([]byte(value), &card) != nil || card == nil {
			return "", "", errors.New("invalid_card_json")
		}
		return "interactive", value, nil
	default:
		return "", "", errors.New("unsupported_message_format")
	}
}
