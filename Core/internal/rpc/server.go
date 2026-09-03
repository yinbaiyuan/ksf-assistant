package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	managedfeishu "codexusagebar/core/internal/feishu"
	"codexusagebar/core/internal/service"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Server struct {
	service *service.Service
	reader  io.Reader
	writer  io.Writer
	writeMu sync.Mutex
	stop    chan struct{}
}

func New(core *service.Service, reader io.Reader, writer io.Writer) *Server {
	return &Server{service: core, reader: reader, writer: writer, stop: make(chan struct{})}
}

func (server *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(server.reader)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		select {
		case <-server.stop:
			return nil
		default:
		}
		server.handle(ctx, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (server *Server) handle(parent context.Context, line []byte) {
	var call request
	if err := json.Unmarshal(line, &call); err != nil {
		server.write(response{JSONRPC: "2.0", Error: &responseError{Code: -32700, Message: "invalid JSON"}})
		return
	}
	if call.JSONRPC != "2.0" || call.Method == "" {
		server.write(response{JSONRPC: "2.0", ID: call.ID, Error: &responseError{Code: -32600, Message: "invalid JSON-RPC request"}})
		return
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	result, err := server.dispatch(ctx, call.Method, call.Params)
	if len(call.ID) == 0 || string(call.ID) == "null" {
		return
	}
	if err != nil {
		server.write(response{JSONRPC: "2.0", ID: call.ID, Error: &responseError{Code: -32000, Message: err.Error()}})
		return
	}
	server.write(response{JSONRPC: "2.0", ID: call.ID, Result: result})
}

func (server *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		return server.service.Initialize(ctx), nil
	case "health/read":
		return map[string]any{"status": "ok", "at": time.Now()}, nil
	case "dashboard/read":
		var input service.DashboardRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.Dashboard(ctx, input), nil
	case "token/history/read":
		var input service.TokenHistoryRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.TokenHistory(ctx, input)
	case "token/history/compare":
		var input service.TokenHistoryRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.TokenHistoryComparison(ctx, input)
	case "pricing/catalog/read":
		var input service.PricingCatalogRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.PricingCatalog(input), nil
	case "task/create":
		var input service.CreateTaskRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.CreateTask(ctx, input)
	case "task/submit":
		var input service.SubmitTaskRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		if err := server.service.SubmitTask(ctx, input); err != nil {
			return nil, err
		}
		return map[string]bool{"submitted": true}, nil
	case "project/launch/prepare":
		var input service.PrepareLaunchRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.PrepareProjectLaunch(ctx, input)
	case "feishu/taskLink/create":
		var input service.TaskLinkRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.CreateTaskLink(ctx, input)
	case "feishu/taskLink/release":
		var input service.TaskLinkRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.ReleaseTaskLink(ctx, input)
	case "feishu/taskLink/interrupt":
		var input service.TaskLinkRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.InterruptTaskLink(ctx, input)
	case "feishu/test":
		var input struct {
			TargetAlias string `json:"targetAlias"`
		}
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		if err := server.service.SendFeishuTest(ctx, input.TargetAlias); err != nil {
			return nil, err
		}
		return map[string]bool{"sent": true}, nil
	case "feishu/profile/read":
		return server.service.FeishuProfile(ctx)
	case "feishu/auth/configure":
		var input struct {
			AppID     string `json:"appId"`
			AppSecret string `json:"appSecret"`
		}
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		if err := server.service.ConfigureFeishu(ctx, input.AppID, input.AppSecret); err != nil {
			return nil, err
		}
		return map[string]bool{"configured": true}, nil
	case "feishu/auth/start":
		return server.service.StartFeishuAuth(ctx)
	case "feishu/auth/finish":
		if err := server.service.FinishFeishuAuth(ctx); err != nil {
			return nil, err
		}
		return map[string]bool{"authenticated": true}, nil
	case "feishu/permissions/read":
		return server.service.FeishuPermissions(ctx)
	case "feishu/setup/read":
		return server.service.FeishuSetup()
	case "feishu/setup/begin":
		var input struct {
			Mode      string `json:"mode"`
			AppID     string `json:"appId"`
			AppSecret string `json:"appSecret"`
		}
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.BeginFeishuSetup(ctx, input.Mode, input.AppID, input.AppSecret)
	case "feishu/setup/continue":
		return server.service.ContinueFeishuSetup(ctx)
	case "feishu/setup/verify":
		return server.service.VerifyFeishuSetup(ctx)
	case "feishu/setup/cancel":
		return server.service.CancelFeishuSetup()
	case "feishu/settings/read":
		return server.service.FeishuSettings()
	case "feishu/settings/update":
		var input managedfeishu.Settings
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.UpdateFeishuSettings(input)
	case "feishu/supervisor/restart":
		return server.service.ControlFeishuService(ctx, "restart")
	case "feishu/profile/set":
		var input struct {
			Profile string `json:"profile"`
		}
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.SetFeishuProfile(ctx, input.Profile)
	case "feishu/service/control":
		var input struct {
			Action string `json:"action"`
		}
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.ControlFeishuService(ctx, input.Action)
	case "shutdown":
		server.service.Close()
		close(server.stop)
		return map[string]bool{"stopped": true}, nil
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func decodeParams(data json.RawMessage, target any) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

func (server *Server) write(value response) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	server.writeMu.Lock()
	defer server.writeMu.Unlock()
	_, _ = server.writer.Write(append(data, '\n'))
}
