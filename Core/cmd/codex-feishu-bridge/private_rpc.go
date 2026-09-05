package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codexusagebar/core/internal/corebridge"
	"codexusagebar/core/internal/domain"
	"codexusagebar/core/internal/feishu"
	"codexusagebar/core/internal/privateipc"
)

type bridgeRPCServer struct {
	dataRoot   string
	mu         sync.RWMutex
	settings   feishu.Settings
	messages   *feishu.OfficialMessageClient
	runtime    *inboundRuntime
	core       *coreCapabilityClient
	capability *feishu.CapabilityService
	revision   atomic.Uint64
}

func newBridgeRPCServer(dataRoot string, settings feishu.Settings, capability *feishu.CapabilityService) *bridgeRPCServer {
	if capability == nil {
		panic("bridge RPC server requires the process capability service")
	}
	return &bridgeRPCServer{dataRoot: dataRoot, settings: settings, capability: capability}
}

func (server *bridgeRPCServer) setRuntime(messages *feishu.OfficialMessageClient, runtime *inboundRuntime, core *coreCapabilityClient) {
	server.mu.Lock()
	server.messages = messages
	server.runtime = runtime
	server.core = core
	server.mu.Unlock()
}

func (server *bridgeRPCServer) HandlePrivateRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case corebridge.MethodBridgeInitialize:
		var request corebridge.InitializeRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if request.Protocol != corebridge.Protocol {
			return nil, privateipc.NewError(-32602, "private IPC protocol mismatch")
		}
		return corebridge.InitializeResult{Protocol: corebridge.Protocol, Version: version}, nil
	case corebridge.MethodBridgeSnapshotRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return server.snapshot(ctx), nil
	case corebridge.MethodTaskLinkCreate:
		var request taskLinkRPCRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.createTaskLink(ctx, request)
	case corebridge.MethodTaskLinkRelease:
		var request taskLinkRPCRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.releaseTaskLink(ctx, request.TaskKey)
	case corebridge.MethodTaskLinkInterrupt:
		var request taskLinkRPCRequest
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.interruptTaskLink(ctx, request.TaskKey)
	case corebridge.MethodInputOutcome:
		var outcome corebridge.InputOutcome
		if err := decodeBridgeParams(params, &outcome); err != nil {
			return nil, err
		}
		server.mu.RLock()
		runtime := server.runtime
		server.mu.RUnlock()
		if runtime == nil {
			return nil, privateipc.NewError(-32062, "task runtime unavailable")
		}
		if err := runtime.applyInputOutcome(ctx, outcome); err != nil {
			return nil, err
		}
		return map[string]bool{"updated": true}, nil
	case corebridge.MethodMessageTest:
		var request struct {
			TargetAlias string `json:"targetAlias"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return map[string]bool{"sent": true}, server.sendTest(ctx, request.TargetAlias)
	case corebridge.MethodProfileSet:
		var request struct {
			Profile string `json:"profile"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.setProfile(request.Profile)
	case corebridge.MethodPermissionsRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		runner := feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: server.dataRoot, WorkingDirectory: server.dataRoot}
		if probe := feishu.ProbeLarkCLI(ctx, runner.Binary); probe.State != "ready" {
			return nil, privateipc.NewError(-32061, "fixed lark-cli capability unavailable")
		}
		return feishu.AuthPermissions(ctx, runner)
	case corebridge.MethodAuthConfigure:
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
	case corebridge.MethodAuthStart:
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
			return nil, err
		}
		return server.attachQR(result)
	case corebridge.MethodAuthFinish:
		var request struct {
			DeviceCode string `json:"deviceCode"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return feishu.FinishUserAuthFlow(ctx, server.authRunner(), server.dataRoot, request.DeviceCode)
	case corebridge.MethodAuthEnsureUser:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return feishu.EnsureCurrentUser(ctx, server.authRunner(), feishu.NewClientConfigStore(server.dataRoot))
	case corebridge.MethodSettingsReload:
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
	case corebridge.MethodOperationPrepare:
		var request struct {
			CapabilityID string         `json:"capabilityId"`
			Input        map[string]any `json:"input"`
			Source       string         `json:"source"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		if request.Source == "" {
			request.Source = "core"
		}
		result, err := server.capability.Prepare(ctx, request.CapabilityID, request.Input, request.Source)
		if err != nil {
			advice := feishu.CapabilityServiceErrorAdvice(err)
			return feishu.PreparedOperation{ErrorCode: advice.ErrorCode, NextAction: advice.NextAction}, nil
		}
		if err == nil && result.Submitted {
			_ = feishu.WakeQueue(server.dataRoot, "actionbox")
		}
		return result, err
	case corebridge.MethodOperationConfirm:
		var request struct {
			OperationID string `json:"operationId"`
			Challenge   string `json:"challenge"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		result, err := server.capability.Confirm(ctx, request.OperationID, request.Challenge)
		if err != nil && result.Operation.ID != "" && operationTerminal(result.Operation.Status) {
			result.ErrorCode = result.Operation.ErrorCode
			result.NextAction = result.Operation.NextAction
			return result, nil
		}
		if err != nil {
			advice := feishu.CapabilityServiceErrorAdvice(err)
			return feishu.PreparedOperation{Operation: result.Operation, ErrorCode: advice.ErrorCode, NextAction: advice.NextAction}, nil
		}
		if err == nil && result.Submitted {
			_ = feishu.WakeQueue(server.dataRoot, "actionbox")
		}
		return result, err
	case corebridge.MethodOperationCancel:
		var request struct {
			OperationID string `json:"operationId"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.capability.Cancel(request.OperationID)
	case corebridge.MethodOperationStatus:
		var request struct {
			OperationID string `json:"operationId"`
		}
		if err := decodeBridgeParams(params, &request); err != nil {
			return nil, err
		}
		return server.capability.Status(request.OperationID)
	case corebridge.MethodPolicyRead:
		if err := requireNoBridgeParams(params); err != nil {
			return nil, err
		}
		return server.capability.ReadPolicy()
	case corebridge.MethodPolicyUpdate:
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
	return feishu.CapabilityExecutor{Binary: strings.TrimSpace(os.Getenv("LARK_CLI_BIN")), Profile: strings.TrimSpace(os.Getenv("LARK_CLI_PROFILE")), DataRoot: server.dataRoot, WorkingDirectory: server.dataRoot}
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
	for _, key := range []string{"status", "flow", "profile", "verificationUrl", "userCode", "next"} {
		if value, found := result[key]; found {
			public[key] = value
		}
	}
	public["qrDataURL"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	return public, nil
}

type taskLinkRPCRequest struct {
	TaskKey     string `json:"taskKey,omitempty"`
	ThreadID    string `json:"threadId,omitempty"`
	Title       string `json:"title,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	TargetAlias string `json:"targetAlias,omitempty"`
	CWD         string `json:"cwd,omitempty"`
}

func (server *bridgeRPCServer) snapshot(ctx context.Context) domain.FeishuSnapshot {
	server.mu.RLock()
	settings, messages, core := server.settings, server.messages, server.core
	server.mu.RUnlock()
	queueHealth := feishu.QueueHealthSnapshot(server.dataRoot, settings)
	capabilities := map[string]domain.CapabilityHealth{
		"feishuInbound": {State: "unavailable"}, "feishuOutbound": {State: "unavailable"},
		"codexAppServer": {State: "unavailable"}, "desktopIPC": {State: "unavailable"}, "ksfContext": {State: "unavailable"},
		"larkCLI": {State: "disabled"}, "outbox": {State: switchState(settings.Outbound.Enabled)}, "docbox": {State: switchState(settings.Docbox.Enabled)}, "actionbox": {State: switchState(settings.Actionbox.Enabled)},
	}
	if messages != nil {
		capabilities["feishuOutbound"] = domain.CapabilityHealth{State: "ready"}
	}
	if settings.Profile != feishu.ProfilePrimary {
		capabilities["feishuInbound"] = domain.CapabilityHealth{State: "disabled"}
	}
	probe := feishu.ProbeLarkCLI(ctx, strings.TrimSpace(os.Getenv("LARK_CLI_BIN")))
	capabilities["larkCLI"] = domain.CapabilityHealth{State: probe.State, Detail: probe.Detail}
	if core != nil {
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		coreState, err := core.capabilities(readCtx)
		cancel()
		if err == nil && coreState.Protocol == corebridge.Protocol {
			for key, value := range coreState.Capabilities {
				capabilities[key] = domain.CapabilityHealth{State: value.State, Detail: value.Detail}
			}
		}
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
	file, linksErr := feishu.NewTaskLinkStore(server.dataRoot).Load()
	links := []domain.FeishuTaskLink{}
	if linksErr == nil {
		public := feishu.PublicLinks(file.Links)
		encoded, _ := json.Marshal(public)
		_ = json.Unmarshal(encoded, &links)
	}
	eventState, _ := feishu.NewEventConsumerStateStore(server.dataRoot).Read()
	connection, _ := eventState["connection"].(map[string]any)
	connected := strings.TrimSpace(stringValue(connection["state"])) == "connected"
	if settings.Profile == feishu.ProfilePrimary && connected {
		capabilities["feishuInbound"] = domain.CapabilityHealth{State: "ready"}
	} else if settings.Profile == feishu.ProfilePrimary && messages != nil {
		capabilities["feishuInbound"] = domain.CapabilityHealth{State: "degraded", Detail: "official SDK inbound disconnected"}
	}
	if settings.Outbound.Enabled && messages == nil {
		capabilities["outbox"] = domain.CapabilityHealth{State: "degraded", Detail: "Feishu outbound unavailable"}
	}
	if capabilities["larkCLI"].State != "ready" {
		if settings.Docbox.Enabled {
			capabilities["docbox"] = domain.CapabilityHealth{State: "degraded", Detail: "fixed lark-cli capability unavailable"}
		}
		if settings.Actionbox.Enabled {
			capabilities["actionbox"] = domain.CapabilityHealth{State: "degraded", Detail: "fixed lark-cli capability unavailable"}
		}
	}
	for name, health := range queueHealth {
		if health.State == "degraded" {
			capabilities[name] = domain.CapabilityHealth{State: "degraded", Detail: "queue state unavailable"}
		}
	}
	ready := settings.Outbound.Enabled && !settings.Outbound.DryRun && messages != nil
	blockers := taskLinkBlockers(settings)
	if messages == nil {
		blockers = append(blockers, "feishuOutbound")
	}
	availability, message := "ready", ""
	if messages == nil {
		availability, message = "unavailable", "飞书凭据缺失或传输尚未连接。"
	} else if !settings.Outbound.Enabled {
		availability, message = "unavailable", "飞书服务尚未启用主动出站。"
	} else if settings.Outbound.DryRun {
		availability = "dryRun"
	}
	return domain.FeishuSnapshot{
		Revision: server.revision.Add(1), RuntimeKind: "go", Availability: availability, Message: message,
		ProcessState: "running", Configured: messages != nil, ProcessPID: os.Getpid(), ProcessRunning: true,
		Profile: settings.Profile, ProfileValid: settings.Profile == feishu.ProfilePrimary || settings.Profile == feishu.ProfileManualOnly,
		InboundConnection: connected, TargetAliases: aliases, TaskLinkProtocolVersion: 2, TaskLinkReady: ready,
		ReadinessBlockers: blockers, Links: links, Capabilities: capabilities,
		Queues: publicQueueHealth(queueHealth),
	}
}

func publicQueueHealth(values map[string]feishu.QueueHealth) map[string]domain.QueueHealth {
	result := make(map[string]domain.QueueHealth, len(values))
	for key, value := range values {
		result[key] = domain.QueueHealth{State: value.State, Revision: value.Revision, Pending: value.Pending, Running: value.Running, Terminal: value.Terminal, Processed: value.Processed, LastError: value.LastError}
	}
	return result
}

func (server *bridgeRPCServer) createTaskLink(ctx context.Context, request taskLinkRPCRequest) (domain.FeishuTaskLink, error) {
	server.mu.RLock()
	messages := server.messages
	server.mu.RUnlock()
	if messages == nil {
		return domain.FeishuTaskLink{}, privateipc.NewError(-32062, "Feishu outbound unavailable")
	}
	store := feishu.NewTaskLinkStore(server.dataRoot)
	link, err := store.Upsert(request.ThreadID, request.Title, request.ProjectName, request.TargetAlias)
	if err != nil {
		return domain.FeishuTaskLink{}, err
	}
	if strings.TrimSpace(request.CWD) != "" {
		link, err = store.UpdateByID(link.ID, func(value *feishu.TaskLink) { value.SetExtraString("workingDirectory", request.CWD) })
		if err != nil {
			return domain.FeishuTaskLink{}, err
		}
	}
	if link.RootMessageID == "" {
		config, err := feishu.NewClientConfigStore(server.dataRoot).Load()
		if err != nil {
			return domain.FeishuTaskLink{}, err
		}
		target, err := config.ResolveMessageTarget(request.TargetAlias)
		if err != nil {
			return domain.FeishuTaskLink{}, err
		}
		card, err := feishu.TaskLinkCardJSON(link)
		if err != nil {
			return domain.FeishuTaskLink{}, err
		}
		key, err := feishu.TaskLinkCardIdempotencyKey(link)
		if err != nil {
			return domain.FeishuTaskLink{}, err
		}
		messageID, err := messages.Send(ctx, target, "card", card, key)
		if err != nil {
			return domain.FeishuTaskLink{}, err
		}
		link, err = store.Update(link.TaskKey, func(value *feishu.TaskLink) {
			value.Target = target
			value.RootMessageID = messageID
			value.MessageIDs = appendUnique(value.MessageIDs, messageID)
		})
		if err != nil {
			return domain.FeishuTaskLink{}, err
		}
	}
	server.mu.RLock()
	runtime := server.runtime
	server.mu.RUnlock()
	if runtime != nil {
		runtime.observeDesktopTask(link.TaskKey)
	}
	return publicDomainTaskLink(link), nil
}

func (server *bridgeRPCServer) releaseTaskLink(ctx context.Context, taskKey string) (domain.FeishuTaskLink, error) {
	store := feishu.NewTaskLinkStore(server.dataRoot)
	link, err := store.Release(taskKey)
	if err != nil {
		return domain.FeishuTaskLink{}, err
	}
	server.mu.RLock()
	messages := server.messages
	server.mu.RUnlock()
	if messages != nil && feishu.TaskLinkCardMessageID(link) != "" {
		_ = feishu.SyncTaskLinkCard(ctx, store, messages, link, "")
	}
	return publicDomainTaskLink(link), nil
}

func (server *bridgeRPCServer) interruptTaskLink(ctx context.Context, taskKey string) (domain.FeishuTaskLink, error) {
	store := feishu.NewTaskLinkStore(server.dataRoot)
	link, found, err := store.FindByTaskKey(taskKey)
	if err != nil || !found {
		if err == nil {
			err = errors.New("task link not found")
		}
		return domain.FeishuTaskLink{}, err
	}
	server.mu.RLock()
	runtime := server.runtime
	server.mu.RUnlock()
	if runtime == nil {
		return domain.FeishuTaskLink{}, privateipc.NewError(-32053, "Codex control capability unavailable")
	}
	if link.ActiveTurnID != "" {
		if err := runtime.interruptTurn(ctx, link); err != nil {
			return domain.FeishuTaskLink{}, err
		}
	}
	link, err = store.Update(taskKey, func(value *feishu.TaskLink) {
		value.TurnState, value.TurnOwner, value.ActionRequired = "interrupted", "none", "none"
		value.Phase, value.Detail = "已停止", "当前任务已标记为中断。"
	})
	return publicDomainTaskLink(link), err
}

func (server *bridgeRPCServer) sendTest(ctx context.Context, alias string) error {
	server.mu.RLock()
	messages := server.messages
	server.mu.RUnlock()
	if messages == nil {
		return privateipc.NewError(-32062, "Feishu outbound unavailable")
	}
	config, err := feishu.NewClientConfigStore(server.dataRoot).Load()
	if err != nil {
		return err
	}
	target, err := config.ResolveMessageTarget(alias)
	if err != nil {
		return err
	}
	_, err = messages.Send(ctx, target, "text", "CodexAssistant 飞书服务连接测试成功", "codexassistant-test-"+time.Now().UTC().Format("20060102150405"))
	return err
}

func (server *bridgeRPCServer) setProfile(profile string) (map[string]any, error) {
	store := feishu.NewSettingsStore(server.dataRoot)
	settings, err := store.Load()
	if err != nil {
		return nil, err
	}
	previous := settings.Profile
	settings.Profile = profile
	if err := store.Save(settings); err != nil {
		return nil, err
	}
	server.mu.Lock()
	server.settings = settings
	server.mu.Unlock()
	return map[string]any{"status": "updated", "previousProfile": previous, "profile": profile}, nil
}

func publicDomainTaskLink(link feishu.TaskLink) domain.FeishuTaskLink {
	public := feishu.PublicLinks([]feishu.TaskLink{link})
	var result domain.FeishuTaskLink
	if len(public) > 0 {
		encoded, _ := json.Marshal(public[0])
		_ = json.Unmarshal(encoded, &result)
	}
	return result
}

func decodeBridgeParams(raw json.RawMessage, target any) error {
	if err := privateipc.DecodeStrict(raw, target, true); err != nil {
		return privateipc.NewError(-32602, "invalid bridge RPC params")
	}
	return nil
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
