package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"ksfassistant/core/internal/feishuprotocol"
)

func (server *Server) dispatchAuth(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == "feishu/auth/start" {
		var input feishuprotocol.AuthStartRequest
		if err := decodeDesktopControlParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.StartDesktopFeishuAuth(ctx, input)
	}
	if err := decodeDesktopControlParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	switch method {
	case "feishu/auth/status":
		return server.service.FeishuAuthStatus(ctx)
	case "feishu/auth/finish":
		return server.service.FinishDesktopFeishuAuth(ctx)
	default:
		return server.service.LogoutFeishuAuth(ctx)
	}
}

func decodeDesktopControlParams(params json.RawMessage, target any) error {
	if len(params) == 0 {
		return nil
	}
	if len(bytes.TrimSpace(params)) == 0 || bytes.TrimSpace(params)[0] != '{' {
		return errors.New("桌面控制参数必须是对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("桌面控制参数无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("桌面控制参数无效")
	}
	return nil
}
