package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OpenAPIMessageTransport is the small, product-owned Feishu transport used by
// task messages and CardKit. It intentionally is not an arbitrary OpenAPI
// proxy and exposes only the operations accepted by MessageCLIRequest.
type OpenAPIMessageTransport struct {
	credentials OfficialCredentials
	httpClient  *http.Client
	mu          sync.Mutex
	token       string
	expiresAt   time.Time
}

type OfficialBotInfo struct {
	OpenID string
	Name   string
}

func (transport *OpenAPIMessageTransport) InspectBot(ctx context.Context) (OfficialBotInfo, error) {
	token, err := transport.tenantToken(ctx)
	if err != nil {
		return OfficialBotInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, transport.endpoint("/open-apis/bot/v3/info"), nil)
	if err != nil {
		return OfficialBotInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := transport.httpClient.Do(req)
	if err != nil {
		return OfficialBotInfo{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maximumCapabilityOutputBytes+1))
	if err != nil || len(data) > maximumCapabilityOutputBytes {
		return OfficialBotInfo{}, errors.New("Feishu bot response is invalid")
	}
	var result struct {
		Code int `json:"code"`
		Bot  struct {
			OpenID string `json:"open_id"`
			Name   string `json:"app_name"`
		} `json:"bot"`
	}
	if json.Unmarshal(data, &result) != nil || resp.StatusCode != http.StatusOK || result.Code != 0 || result.Bot.OpenID == "" {
		return OfficialBotInfo{}, errors.New("Feishu bot identity is unavailable")
	}
	return OfficialBotInfo{OpenID: result.Bot.OpenID, Name: result.Bot.Name}, nil
}

func NewOpenAPIMessageTransport(credentials OfficialCredentials) (*OpenAPIMessageTransport, error) {
	if strings.TrimSpace(credentials.AppID) == "" || strings.TrimSpace(credentials.AppSecret) == "" {
		return nil, errors.New("official Feishu credentials are required")
	}
	return &OpenAPIMessageTransport{
		credentials: credentials,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (transport *OpenAPIMessageTransport) endpoint(path string) string {
	host := "https://open.feishu.cn"
	if brandOrDefault(transport.credentials.Brand) == "lark" {
		host = "https://open.larksuite.com"
	}
	return host + path
}

func (transport *OpenAPIMessageTransport) tenantToken(ctx context.Context) (string, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.token != "" && time.Until(transport.expiresAt) > 2*time.Minute {
		return transport.token, nil
	}
	body, _ := json.Marshal(map[string]string{
		"app_id": transport.credentials.AppID, "app_secret": transport.credentials.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transport.endpoint("/open-apis/auth/v3/tenant_access_token/internal"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := transport.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maximumCapabilityOutputBytes+1))
	if err != nil || len(data) > maximumCapabilityOutputBytes {
		return "", errors.New("Feishu token response is invalid")
	}
	var result struct {
		Code              int    `json:"code"`
		Message           string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if json.Unmarshal(data, &result) != nil || resp.StatusCode != http.StatusOK || result.Code != 0 || result.TenantAccessToken == "" {
		return "", fmt.Errorf("Feishu tenant token unavailable: status=%d code=%d", resp.StatusCode, result.Code)
	}
	transport.token = result.TenantAccessToken
	expires := result.Expire
	if expires <= 0 {
		expires = 3600
	}
	transport.expiresAt = time.Now().Add(time.Duration(expires) * time.Second)
	return transport.token, nil
}

func (transport *OpenAPIMessageTransport) CallMessage(ctx context.Context, request MessageCLIRequest) (map[string]any, error) {
	method, path, err := openAPIMessageRequest(request)
	if err != nil {
		return nil, err
	}
	token, err := transport.tenantToken(ctx)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if request.Body != nil {
		encoded, marshalErr := json.Marshal(request.Body)
		if marshalErr != nil {
			return nil, marshalErr
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, transport.endpoint(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if request.Body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := transport.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maximumCapabilityOutputBytes+1))
	if err != nil || len(data) > maximumCapabilityOutputBytes {
		return nil, errors.New("Feishu OpenAPI response is invalid")
	}
	var envelope struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil, errors.New("Feishu OpenAPI returned invalid JSON")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code != 0 {
		return nil, &CLIExecutionError{
			// A decoded Feishu error envelope is a confirmed rejection: the
			// remote service received the request and did not apply it. Reserve
			// unknown for transport failures where no response was received.
			Code: "feishu_openapi_failed", Started: true, Outcome: "rejected",
			Structured: map[string]any{"type": "api", "code": float64(envelope.Code), "http_status": resp.StatusCode},
		}
	}
	if envelope.Data == nil {
		envelope.Data = map[string]any{}
	}
	return envelope.Data, nil
}

func openAPIMessageRequest(request MessageCLIRequest) (string, string, error) {
	if request.File != "" {
		return "", "", errors.New("media_upload_not_supported_by_internal_transport")
	}
	if request.Resource == "cardkit" {
		command, err := cardKitCommand(request)
		if err != nil || len(command) < 3 || command[0] != "api" {
			return "", "", errors.New("invalid_cardkit_request")
		}
		return command[1], command[2], nil
	}
	messageID := url.PathEscape(request.Params["message_id"])
	switch request.Resource + "." + request.Method {
	case "messages.create":
		kind := request.Params["receive_id_type"]
		if kind != "chat_id" && kind != "open_id" {
			return "", "", errors.New("unsupported_target_type")
		}
		return http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=" + url.QueryEscape(kind), nil
	case "messages.reply":
		if messageID == "" {
			return "", "", errors.New("invalid_message_id")
		}
		return http.MethodPost, "/open-apis/im/v1/messages/" + messageID + "/reply", nil
	case "messages.patch":
		if messageID == "" {
			return "", "", errors.New("invalid_message_id")
		}
		return http.MethodPatch, "/open-apis/im/v1/messages/" + messageID, nil
	case "messages.get":
		if messageID == "" {
			return "", "", errors.New("invalid_message_id")
		}
		return http.MethodGet, "/open-apis/im/v1/messages/" + messageID, nil
	default:
		return "", "", errors.New("unsupported_internal_feishu_operation")
	}
}
