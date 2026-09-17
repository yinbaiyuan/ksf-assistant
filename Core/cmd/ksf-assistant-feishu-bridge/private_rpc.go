package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privateipc"
)

type bridgeRPCServer struct {
	dataRoot          string
	mu                sync.RWMutex
	settings          feishu.Settings
	messages          *feishu.OfficialMessageClient
	transport         *feishu.ServiceTransport
	runtimeError      string
	scheduler         *feishu.WorkScheduler
	capability        *feishu.CapabilityService
	revision          atomic.Uint64
	initialized       bool
	bindingState      string
	bindingDetail     string
	restorationDelays []time.Duration
}

func newBridgeRPCServer(dataRoot string, settings feishu.Settings, capability *feishu.CapabilityService) *bridgeRPCServer {
	if capability == nil {
		panic("bridge RPC server requires the process capability service")
	}
	return &bridgeRPCServer{dataRoot: dataRoot, settings: settings, capability: capability}
}

func (server *bridgeRPCServer) setRuntime(messages *feishu.OfficialMessageClient) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.messages = messages
	if messages == nil {
		server.transport = nil
	} else {
		server.transport = feishu.NewServiceTransport(server.dataRoot, messages)
	}
	server.capability.SetMessageTransport(server.transport)
}

func (server *bridgeRPCServer) degrade(err error) {
	if err != nil {
		server.mu.Lock()
		server.runtimeError = err.Error()
		server.mu.Unlock()
	}
}

func (server *bridgeRPCServer) restoreCardBindings(ctx context.Context, transport *feishu.ServiceTransport, bindings []feishuprotocol.CardBinding) {
	delays := server.restorationDelays
	if len(delays) == 0 {
		delays = []time.Duration{0, time.Second, 2 * time.Second, 5 * time.Second, 15 * time.Second}
	}
	pending := append([]feishuprotocol.CardBinding(nil), bindings...)
	permanent := 0
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		remaining := []feishuprotocol.CardBinding{}
		for _, binding := range pending {
			if ctx.Err() != nil {
				return
			}
			if transport == nil {
				permanent++
				continue
			}
			err := transport.RestoreCardBinding(ctx, feishu.MessageTarget{Type: binding.TargetType, ID: binding.TargetID}, binding.MessageID)
			if err != nil {
				if feishu.CardRestorationRetryable(err) {
					remaining = append(remaining, binding)
				} else {
					permanent++
				}
			}
		}
		pending = remaining
		server.mu.Lock()
		server.bindingState, server.bindingDetail = "ready", ""
		if len(pending)+permanent > 0 {
			server.bindingState = "degraded"
			server.bindingDetail = fmt.Sprintf("%d cards awaiting restoration; %d require ownership or permission review", len(pending), permanent)
		}
		server.mu.Unlock()
		if len(pending) == 0 {
			return
		}
	}
	server.mu.Lock()
	server.bindingDetail = fmt.Sprintf("restoration retries exhausted for %d cards; %d require review; reconnect to retry", len(pending), permanent)
	server.mu.Unlock()
}

func (server *bridgeRPCServer) HandlePrivateRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case feishuprotocol.MediaCleanup:
		var request struct {
			Directory string `json:"directory"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return map[string]bool{"removed": true}, feishu.CleanupInboundAssets(server.dataRoot, request.Directory)
	case feishuprotocol.AuditRecord:
		var request feishuprotocol.AuditRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if len(request.Event) > 100 || len(request.Fields) > 20 || !(strings.HasPrefix(request.Event, "desktop_user_input_") || strings.HasPrefix(request.Event, "inbound_") || strings.HasPrefix(request.Event, "task_link_")) {
			return nil, errors.New("unsupported integration audit event")
		}
		return map[string]bool{"recorded": true}, feishu.NewAuditLog(server.dataRoot).Record(request.Event, request.Fields)
	case feishuprotocol.MethodBridgeInitialize:
		var request feishuprotocol.InitializeRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if request.Protocol != feishuprotocol.Protocol {
			return nil, privateipc.NewError(-32602, "private IPC protocol mismatch")
		}
		if len(request.CardBindings) > 4096 {
			return nil, privateipc.NewError(-32602, "too many card bindings")
		}
		server.mu.Lock()
		if server.initialized && len(request.CardBindings) > 0 {
			server.mu.Unlock()
			return nil, errors.New("card restoration is only accepted during initialization")
		}
		server.initialized = true
		transport := server.transport
		server.mu.Unlock()
		if len(request.CardBindings) > 0 {
			server.mu.Lock()
			server.bindingState = "degraded"
			server.mu.Unlock()
			connection, ok := privateipc.ConnectionContext(ctx)
			if !ok {
				connection = ctx
			}
			go server.restoreCardBindings(connection, transport, request.CardBindings)
		}
		return feishuprotocol.InitializeResult{Protocol: feishuprotocol.Protocol, Version: version}, nil
	case feishuprotocol.MethodBridgeSnapshotRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return server.snapshot(ctx), nil
	case feishuprotocol.MessageSend, feishuprotocol.MessageReply:
		var request feishuprotocol.MessageRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		server.mu.RLock()
		transport := server.transport
		server.mu.RUnlock()
		if transport == nil {
			return nil, errors.New("Feishu outbound unavailable")
		}
		result, err := transport.Message(ctx, method == feishuprotocol.MessageReply, request)
		return result, transportRPCError(err)
	case feishuprotocol.CardPatch:
		var request feishuprotocol.CardRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		server.mu.RLock()
		transport := server.transport
		server.mu.RUnlock()
		if transport == nil {
			return nil, errors.New("Feishu outbound unavailable")
		}
		return map[string]bool{"updated": true}, transportRPCError(transport.Patch(ctx, request))
	case feishuprotocol.MediaStage:
		var request feishuprotocol.MediaRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		var message feishu.InboundMessage
		if err := decodeBridgeParams(request.Message, &message); err != nil {
			return nil, err
		}
		server.mu.RLock()
		transport := server.transport
		server.mu.RUnlock()
		if transport == nil {
			return nil, errors.New("Feishu transport unavailable")
		}
		if err := transport.CheckInbound(message); err != nil {
			return nil, err
		}
		return feishu.StageInboundMessage(ctx, server.dataRoot, server.authRunner(), message, request.MaxBytes)
	case feishuprotocol.ConfigRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.NewClientConfigStore(server.dataRoot).Load()
	case feishuprotocol.SettingsRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.NewSettingsStore(server.dataRoot).Load()
	case feishuprotocol.MethodSettingsCompareAndSwap:
		var request struct {
			Expected *feishu.Settings `json:"expected"`
			Settings *feishu.Settings `json:"settings"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if request.Expected == nil || request.Settings == nil {
			return nil, privateipc.NewError(-32602, "expected and settings are required")
		}
		if request.Expected.Version != 2 || request.Settings.Version != 2 {
			return nil, errors.New("configuration_action_retired")
		}
		store := feishu.NewSettingsStore(server.dataRoot)
		if err := store.CompareAndSwap(*request.Expected, *request.Settings); err != nil {
			if errors.Is(err, feishu.ErrSettingsConflict) {
				return nil, privateipc.NewError(-32064, "feishu_settings_changed")
			}
			return nil, err
		}
		settings, err := store.Load()
		if err != nil {
			return nil, err
		}
		server.mu.Lock()
		server.settings = settings
		server.mu.Unlock()
		return settings, nil
	case feishuprotocol.SettingsWrite:
		var settings feishu.Settings
		if err := decodeBridgeParams(params, &settings); err != nil {
			return nil, err
		}
		if settings.Version != 2 {
			return nil, errors.New("configuration_action_retired")
		}
		store := feishu.NewSettingsStore(server.dataRoot)
		if err := store.Save(settings); err != nil {
			return nil, err
		}
		settings, err := store.Load()
		if err != nil {
			return nil, err
		}
		server.mu.Lock()
		server.settings = settings
		server.mu.Unlock()
		return settings, nil
	case feishuprotocol.SetupRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.NewSetupStore(server.dataRoot).Load()
	case feishuprotocol.SetupWrite:
		var setup feishu.SetupState
		if err := decodeBridgeParams(params, &setup); err != nil {
			return nil, err
		}
		return setup, feishu.NewSetupStore(server.dataRoot).Save(setup)
	case feishuprotocol.MethodMessageTest, "bridge/message/test/result":
		var request struct {
			TargetAlias string `json:"targetAlias"`
			RequestID   string `json:"requestId"`
			Confirm     bool   `json:"confirm"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if request.RequestID == "" || len(request.RequestID) > 128 {
			return nil, errors.New("test_request_invalid")
		}
		if method == "bridge/message/test/result" {
			server.mu.RLock()
			transport := server.transport
			server.mu.RUnlock()
			if transport == nil {
				return nil, errors.New("test_transport_unavailable")
			}
			messageID, outcome, err := transport.MessageReceipt(testMessageKey(request.RequestID))
			return map[string]string{"messageId": messageID, "outcome": outcome}, err
		}
		if !request.Confirm {
			return nil, errors.New("test_confirmation_required")
		}
		result, err := server.sendTest(ctx, request.TargetAlias, request.RequestID)
		return result, transportRPCError(err)
	case feishuprotocol.MethodPermissionsRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return nil, privateipc.NewError(-32601, "standalone permission inspection is retired")
	case "bridge/auth/cancel":
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return nil, privateipc.NewError(-32602, "取消操作必须指定当前配置会话")
	case feishuprotocol.MethodConfigurationEvidence:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		evidence, err := feishu.ReadConfigurationEvidence(ctx, server.authRunner(), server.dataRoot)
		evidence.ServiceVersion = version
		return evidence, err
	case feishuprotocol.MethodConfigurationFlow:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.ReadConfigurationFlow(server.dataRoot), nil
	case feishuprotocol.MethodConfigurationCancel:
		var request feishuprotocol.ConfigurationCancelRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if request.FlowID == "" || request.Kind != "app" && request.Kind != "user" {
			return nil, privateipc.NewError(-32602, "取消操作必须指定有效的配置会话")
		}
		return feishu.CancelConfigurationFlow(server.dataRoot, request.FlowID, request.Kind)
	case feishuprotocol.MethodAuthStatus:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.ReadAuthStatus(ctx, server.authRunner(), server.dataRoot)
	case feishuprotocol.MethodAuthLogout:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.LogoutUserAuth(ctx, server.authRunner(), server.dataRoot)
	case feishuprotocol.MethodAuthConfigure:
		var request struct {
			AppID     string `json:"appId"`
			AppSecret string `json:"appSecret"`
			Brand     string `json:"brand"`
			Profile   string `json:"profile"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return feishu.ConfigureExistingApp(ctx, server.authRunner(), request.AppID, request.AppSecret, request.Brand, request.Profile)
	case feishuprotocol.MethodAuthStart:
		var request struct {
			Kind      string `json:"kind"`
			Profile   string `json:"profile"`
			Scope     string `json:"scope"`
			CreateNew bool   `json:"createNew"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		var result map[string]any
		var err error
		if request.Kind == "config" {
			result, err = feishu.StartAppConfiguration(ctx, server.authRunner(), server.dataRoot, request.Profile, request.CreateNew)
		} else {
			result, err = feishu.StartUserAuth(ctx, server.authRunner(), server.dataRoot, request.Scope)
		}
		if err != nil {
			if feishu.IsAppConfigurationNotStarted(err) {
				return nil, privateipc.NewError(-32066, "application_start_preflight_failed")
			}
			return nil, err
		}
		return server.attachQR(result)
	case feishuprotocol.MethodAuthConfigFinish:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		result, err := feishu.FinishAppConfiguration(ctx, server.authRunner(), server.dataRoot)
		if err != nil {
			return nil, err
		}
		return server.attachQR(result)
	case feishuprotocol.MethodAuthFinish:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.FinishUserAuthFlow(ctx, server.authRunner(), server.dataRoot, "")
	case feishuprotocol.MethodAuthEnsureUser:
		var request feishu.OperatorBindingExpectation
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if !request.Valid() {
			return nil, privateipc.NewError(-32602, "verified application and identity context are required")
		}
		result, err := feishu.EnsureCurrentUserWithExpected(ctx, server.authRunner(), feishu.NewClientConfigStore(server.dataRoot), request)
		if errors.Is(err, feishu.ErrOperatorContextConflict) || errors.Is(err, feishu.ErrClientConfigConflict) {
			return nil, privateipc.NewError(-32065, "feishu_operator_context_changed")
		}
		return result, err
	case feishuprotocol.MethodSettingsReload:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		settings, err := feishu.NewSettingsStore(server.dataRoot).Load()
		if err != nil {
			return nil, err
		}
		server.mu.Lock()
		server.settings = settings
		server.mu.Unlock()
		return settings, nil
	case feishuprotocol.MethodOperationConfirm:
		var request struct {
			OperationID string `json:"operationId"`
			Challenge   string `json:"challenge"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		result, err := server.capability.ConfirmServiceMessage(ctx, request.OperationID, request.Challenge)
		if err != nil && result.Operation.ID != "" && operationTerminal(result.Operation.Status) {
			result.ErrorCode = result.Operation.ErrorCode
			result.NextAction = result.Operation.NextAction
			return result, nil
		}
		if err != nil {
			advice := feishu.CapabilityServiceErrorAdvice(err)
			return feishu.PreparedOperation{Operation: result.Operation, ErrorCode: advice.ErrorCode, NextAction: advice.NextAction}, nil
		}
		return result, err
	case feishuprotocol.MethodOperationCancel:
		var request struct {
			OperationID string `json:"operationId"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.capability.Cancel(request.OperationID)
	case feishuprotocol.MethodOperationStatus:
		var request struct {
			OperationID string `json:"operationId"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.capability.Status(request.OperationID)
	case feishuprotocol.MethodPolicyRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return server.capability.ReadPolicy()
	case feishuprotocol.MethodPolicyUpdate:
		var request struct {
			Policy           feishu.CapabilityPolicy `json:"policy"`
			ExpectedRevision uint64                  `json:"expectedRevision"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.capability.UpdatePolicy(request.Policy, request.ExpectedRevision)
	default:
		return nil, privateipc.ErrMethodNotFound
	}
}

func (server *bridgeRPCServer) authRunner() feishu.CapabilityExecutor {
	return feishu.CapabilityExecutor{Profile: "default", DataRoot: server.dataRoot, WorkingDirectory: server.dataRoot}
}

func (server *bridgeRPCServer) mailEventsEnabled() bool {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.settings.MailEvents.Enabled
}

func (server *bridgeRPCServer) attachQR(result map[string]any) (map[string]any, error) {
	path, _ := result["qrPath"].(string)
	if path == "" {
		return result, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("invalid private QR path")
	}
	root, err := filepath.Abs(server.dataRoot)
	if err != nil {
		return nil, errors.New("invalid private data root")
	}
	relative, err := filepath.Rel(root, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("private QR path escaped data root")
	}
	data, err := os.ReadFile(abs)
	if err != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil, errors.New("private QR file is invalid")
	}
	public := map[string]any{}
	for _, key := range []string{"schemaVersion", "status", "identity", "identityValid", "profileValid", "grantedScopeCount", "missingCapabilities", "flow", "profile", "verificationUrl", "userCode", "next"} {
		if value, found := result[key]; found {
			public[key] = value
		}
	}
	public["qrDataURL"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	return public, nil
}

func (server *bridgeRPCServer) snapshot(ctx context.Context) feishuprotocol.Snapshot {
	server.mu.RLock()
	settings, messages, runtimeError := server.settings, server.messages, server.runtimeError
	bindingState, bindingDetail := server.bindingState, server.bindingDetail
	scheduler := server.scheduler
	server.mu.RUnlock()
	if scheduler != nil {
		if err := scheduler.Health(); err != nil {
			runtimeError = "work scheduler unavailable"
		}
	}
	queueHealth := feishu.QueueHealthSnapshot(server.dataRoot, settings)
	capabilities := map[string]feishuprotocol.CapabilityHealth{
		"feishuInbound": {State: "unavailable"}, "feishuOutbound": {State: "unavailable"},
		"outbox": {State: "ready"},
	}
	if bindingState != "" {
		capabilities["cardBindings"] = feishuprotocol.CapabilityHealth{State: bindingState, Detail: bindingDetail}
	}
	if messages != nil {
		capabilities["feishuOutbound"] = feishuprotocol.CapabilityHealth{State: "ready"}
	}
	config, configErr := feishu.NewClientConfigStore(server.dataRoot).Load()
	aliases := []string{}
	if configErr == nil {
		for alias, target := range config.MessageTargets {
			if target.Type == "open_id" && containsString(config.DirectAllowedAliases, alias) {
				aliases = append(aliases, alias)
			}
		}
	}
	sort.Strings(aliases)
	eventState, _ := feishu.NewEventConsumerStateStore(server.dataRoot).Read()
	connection, _ := eventState["connection"].(map[string]any)
	connected := messages != nil && eventState["transport"] == "official-sdk" && strings.TrimSpace(stringValue(connection["state"])) == "connected"
	if connected {
		capabilities["feishuInbound"] = feishuprotocol.CapabilityHealth{State: "ready"}
	} else if messages != nil {
		capabilities["feishuInbound"] = feishuprotocol.CapabilityHealth{State: "degraded", Detail: "official SDK connection is not ready"}
	}
	if messages == nil {
		capabilities["outbox"] = feishuprotocol.CapabilityHealth{State: "degraded", Detail: "Feishu outbound unavailable"}
	}
	for name, health := range queueHealth {
		if health.State == "degraded" {
			capabilities[name] = feishuprotocol.CapabilityHealth{State: "degraded", Detail: "queue state unavailable"}
		}
	}
	blockers := []string{}
	if messages == nil {
		blockers = append(blockers, "feishuOutbound")
	}
	if !connected {
		blockers = append(blockers, "feishuInbound")
	}
	availability, message := "ready", ""
	if messages == nil {
		availability, message = "unavailable", "飞书凭据缺失或传输尚未连接。"

	} else if !connected {
		availability, message = "degraded", "飞书事件消费者尚未就绪。"
	}
	if runtimeError != "" {
		availability, message = "degraded", "Feishu runtime processing unavailable"
	}
	return feishuprotocol.Snapshot{
		Revision: server.revision.Add(1), RuntimeKind: "go", Availability: availability, Message: message,
		ProcessState: "running", Configured: messages != nil, ProcessPID: os.Getpid(), ProcessRunning: true,
		Profile: feishuprotocol.ManagedEventProfile, ProfileValid: true,
		InboundConnection: connected, TargetAliases: aliases,
		ReadinessBlockers: blockers, Capabilities: capabilities,
		Queues: publicQueueHealth(queueHealth),
	}
}

func publicQueueHealth(values map[string]feishu.QueueHealth) map[string]feishuprotocol.QueueHealth {
	result := make(map[string]feishuprotocol.QueueHealth, len(values))
	for key, value := range values {
		result[key] = feishuprotocol.QueueHealth{State: value.State, Revision: value.Revision, Pending: value.Pending, Running: value.Running, Terminal: value.Terminal, Processed: value.Processed, LastError: value.LastError}
	}
	return result
}

func testMessageKey(requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	return "ksfassistant-test-" + hex.EncodeToString(sum[:16])
}
func (server *bridgeRPCServer) sendTest(ctx context.Context, alias, requestID string) (feishuprotocol.MessageResult, error) {
	server.mu.RLock()
	transport := server.transport
	server.mu.RUnlock()
	if transport == nil {
		return feishuprotocol.MessageResult{}, errors.New("test_not_submitted_transport_unavailable")
	}
	evidence, err := feishu.ReadConfigurationEvidence(ctx, server.authRunner(), server.dataRoot)
	if err != nil || evidence.OperatorState != "present" || evidence.OperatorAlias != alias || alias == "" {
		return feishuprotocol.MessageResult{}, errors.New("test_not_submitted_self_unverified")
	}
	config, err := feishu.NewClientConfigStore(server.dataRoot).Load()
	if err != nil {
		return feishuprotocol.MessageResult{}, err
	}
	target, err := config.ResolveMessageTarget(alias)
	if err != nil || target.Type != "open_id" {
		return feishuprotocol.MessageResult{}, errors.New("test_not_submitted_self_unverified")
	}
	result, err := transport.Message(ctx, false, feishuprotocol.MessageRequest{TargetType: target.Type, TargetID: target.ID, Format: "text", Content: "【KSFAssistant 接入验收】这是一条由本人确认发送的连接测试消息，无需回复。", IdempotencyKey: testMessageKey(requestID)})
	var authorization *feishu.TransportAuthorizationError
	if errors.As(err, &authorization) {
		// The configuration confirmation covers this exact fixed self-test only.
		confirmed, confirmErr := server.capability.Confirm(ctx, authorization.Prepared.Operation.ID, authorization.Prepared.Challenge)
		if confirmErr != nil {
			return result, confirmErr
		}
		result.MessageID, _ = confirmed.Result["messageID"].(string)
		if confirmed.Operation.Status != feishu.OperationSucceeded {
			return result, errors.New("test_result_unknown")
		}
		err = nil
	}
	if err == nil && result.MessageID == "" {
		err = errors.New("test_receipt_missing")
	}
	return result, err
}

func decodeBridgeParams(raw json.RawMessage, target any) error {
	if err := privateipc.DecodeStrict(raw, target, true); err != nil {
		return privateipc.NewError(-32602, "invalid bridge RPC params")
	}
	return nil
}

func transportRPCError(err error) error {
	var authorization *feishu.TransportAuthorizationError
	if !errors.As(err, &authorization) {
		var retry interface{ RetryDelay() time.Duration }
		if err != nil && errors.As(err, &retry) && retry.RetryDelay() > 0 {
			data, _ := json.Marshal(map[string]any{"retryAfterMs": retry.RetryDelay().Milliseconds()})
			return &privateipc.RPCError{Code: -32064, Message: err.Error(), Data: data}
		}
		return err
	}
	data, encodeErr := json.Marshal(map[string]any{"status": "authorization_required", "operation": authorization.Prepared.Operation, "challenge": authorization.Prepared.Challenge, "submitted": false, "nextAction": "confirm"})
	if encodeErr != nil {
		return encodeErr
	}
	return &privateipc.RPCError{Code: -32063, Message: "Feishu message requires explicit confirmation", Data: data}
}

func requireNoBridgeParams(raw json.RawMessage) error {
	if err := privateipc.RequireNoParams(raw); err != nil {
		return privateipc.NewError(-32602, "invalid bridge RPC params")
	}
	return nil
}

func switchState(enabled bool) string {
	if enabled {
		return "ready"
	}
	return "disabled"
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
