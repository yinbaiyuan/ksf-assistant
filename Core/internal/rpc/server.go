package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

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
			FeishuBridgeRoot string `json:"feishuBridgeRoot"`
			TargetAlias      string `json:"targetAlias"`
		}
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		if err := server.service.SendFeishuTest(ctx, input.FeishuBridgeRoot, input.TargetAlias); err != nil {
			return nil, err
		}
		return map[string]bool{"sent": true}, nil
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
