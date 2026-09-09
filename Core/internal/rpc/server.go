package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/privateipc"
	"ksfassistant/core/internal/service"
	"ksfassistant/core/internal/userapproval"
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
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type Server struct {
	service  *service.Service
	reader   io.Reader
	writer   io.Writer
	writeMu  sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
}

func New(core *service.Service, reader io.Reader, writer io.Writer) *Server {
	return &Server{service: core, reader: reader, writer: writer, stop: make(chan struct{})}
}

func (server *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan []byte)
	readError := make(chan error, 1)
	go func() {
		var scanError error
		defer func() { readError <- scanError; close(lines) }()
		scanner := bufio.NewScanner(server.reader)
		scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				scanError = ctx.Err()
				return
			}
		}
		scanError = scanner.Err()
	}()
	normal := make(chan []byte, 32)
	reads := make(chan []byte, 4)
	var workers sync.WaitGroup
	var closeWorkersOnce sync.Once
	closeWorkers := func() { closeWorkersOnce.Do(func() { close(normal); close(reads) }) }
	for _, queue := range []chan []byte{normal, reads} {
		workers.Add(1)
		go func(queue <-chan []byte) {
			defer workers.Done()
			for line := range queue {
				if ctx.Err() == nil {
					server.handle(ctx, line)
				}
			}
		}(queue)
	}
	defer func() {
		cancel()
		closeWorkers()
		if closer, ok := server.reader.(io.Closer); ok {
			_ = closer.Close()
		}
		workers.Wait()
	}()
	initialized := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-server.stop:
			return nil
		case line, open := <-lines:
			if !open {
				closeWorkers()
				workers.Wait()
				return <-readError
			}
			var call request
			_ = json.Unmarshal(line, &call)
			switch call.Method {
			case "initialize":
				if initialized {
					server.write(response{JSONRPC: "2.0", ID: call.ID, Error: &responseError{Code: -32000, Message: "desktop_already_initialized"}})
				} else {
					initialized = true
					server.handle(ctx, line)
				}
			case "userApproval/poll", "userApproval/decide", "health/read":
				server.handle(ctx, line)
			case "shutdown":
				cancel()
				closeWorkers()
				workers.Wait()
				server.handle(context.WithoutCancel(ctx), line)
				return nil
			default:
				queue := normal
				if call.Method == "dashboard/read" || call.Method == "feishu/configuration/read" {
					queue = reads
				}
				select {
				case queue <- line:
				default:
					server.write(response{JSONRPC: "2.0", ID: call.ID, Error: &responseError{Code: -32000, Message: "desktop_request_busy"}})
				}
			}
		}
	}
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
	ctx, cancel := context.WithTimeout(parent, desktopRequestTimeout(call.Method))
	defer cancel()
	result, err := server.dispatch(ctx, call.Method, call.Params)
	if len(call.ID) == 0 || string(call.ID) == "null" {
		return
	}
	if err != nil {
		failure := &responseError{Code: -32000, Message: err.Error()}
		var privateError *privateipc.RPCError
		if errors.As(err, &privateError) && privateError.Code == -32063 {
			failure.Code = privateError.Code
			failure.Data = privateError.Data
		}
		server.write(response{JSONRPC: "2.0", ID: call.ID, Error: failure})
		return
	}
	server.write(response{JSONRPC: "2.0", ID: call.ID, Result: result})
	if call.Method == "shutdown" {
		server.stopOnce.Do(func() { close(server.stop) })
	}
}

func (server *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "feishu/auth/configure", "feishu/auth/start", "feishu/auth/finish", "feishu/auth/logout", "feishu/setup/begin", "feishu/setup/continue", "feishu/setup/activate", "feishu/setup/cancel", "feishu/settings/update", "feishu/features/update", "feishu/supervisor/restart", "feishu/service/control", "feishu/test":
		return nil, errors.New("configuration_legacy_mutation_disabled: 请使用当前桌面的飞书配置操作")
	}
	switch method {
	case "feishu/configuration/read":
		var input struct {
			Refresh bool `json:"refresh"`
		}
		if err := decodeConfigurationParams(params, &input, "refresh"); err != nil {
			return nil, err
		}
		return server.service.ReadFeishuConfiguration(ctx, input.Refresh), nil
	case "feishu/configuration/result":
		var input struct {
			RequestID string `json:"requestId"`
		}
		if err := decodeConfigurationParams(params, &input, "requestId"); err != nil {
			return nil, err
		}
		return server.service.ConfigurationResult(ctx, input.RequestID)
	case "feishu/configuration/action":
		var input service.ConfigurationActionRequest
		if len(params) > 16*1024 {
			return nil, errors.New("configuration_request_too_large")
		}
		if err := decodeConfigurationParams(params, &input, "action", "requestId", "epoch", "revision", "contextRevision", "confirm", "appId", "appSecret", "targetAlias", "feature", "mode", "flowId"); err != nil {
			return nil, err
		}
		return server.service.ApplyFeishuConfiguration(ctx, input)
	case "userApproval/poll":
		var input struct {
			Interactive *bool `json:"interactive"`
		}
		if err := userapproval.DecodeParams(params, &input, "interactive"); err != nil || input.Interactive == nil {
			return nil, errors.New("approval_invalid_request")
		}
		return server.service.UserApprovalPoll(*input.Interactive), nil
	case "userApproval/decide":
		var input struct {
			ID      string `json:"id"`
			Approve *bool  `json:"approve"`
		}
		if err := userapproval.DecodeParams(params, &input, "id", "approve"); err != nil || input.ID == "" || input.Approve == nil {
			return nil, errors.New("approval_invalid_request")
		}
		return server.service.UserApprovalDecide(input.ID, *input.Approve), nil
	case "initialize":
		var input service.InitializeRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.Initialize(ctx, input), nil
	case "integration/context/update":
		var input service.IntegrationContextRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.UpdateIntegrationContext(input)
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
	case "workspace/task/create":
		var input service.CreateWorkspaceTaskRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.CreateWorkspaceTask(ctx, input)
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
	case "feishu/operation/prepare":
		var input service.FeishuOperationPrepareRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.PrepareFeishuOperation(ctx, input)
	case "feishu/operation/confirm":
		var input service.FeishuOperationConfirmRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.ConfirmFeishuOperation(ctx, input)
	case "feishu/operation/cancel":
		var input service.FeishuOperationRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.CancelFeishuOperation(ctx, input)
	case "feishu/operation/status":
		var input service.FeishuOperationRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.FeishuOperationStatus(ctx, input)
	case "feishu/policy/read":
		return server.service.FeishuCapabilityPolicy(ctx)
	case "feishu/policy/update":
		var input service.FeishuPolicyUpdateRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.UpdateFeishuCapabilityPolicy(ctx, input)
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
	case "feishu/auth/status", "feishu/auth/start", "feishu/auth/finish", "feishu/auth/logout":
		return server.dispatchAuth(ctx, method, params)
	case "feishu/permissions/read":
		return server.service.FeishuPermissions(ctx)
	case "toolchain/status", "toolchain/install":
		return server.dispatchToolchain(method, params)
	case "feishu/settings/overview/read":
		return server.service.FeishuSettingsOverview(ctx)
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
	case "feishu/setup/activate":
		var input struct {
			TargetAlias string `json:"targetAlias"`
		}
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.ActivateFeishuSetup(ctx, input.TargetAlias)
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
	case "feishu/features/update":
		var input service.FeishuFeatureUpdateRequest
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		return server.service.UpdateFeishuFeature(ctx, input)
	case "feishu/supervisor/restart":
		return server.service.ControlFeishuService(ctx, "restart")
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
