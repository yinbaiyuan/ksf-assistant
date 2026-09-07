package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/domain"
	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
)

type ConfigurationSummary struct {
	State  string `json:"state"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Tone   string `json:"tone"`
}

type ConfigurationFact struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	CheckedAt string `json:"checkedAt,omitempty"`
	Stale     bool   `json:"stale"`
}

type ConfigurationAction struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Enabled      bool   `json:"enabled"`
	Reason       string `json:"reason,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
}

type ConfigurationIssue struct {
	Component string `json:"component"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type ConfigurationSnapshot struct {
	Diagnostics     ConfigurationDiagnostics          `json:"diagnostics"`
	SchemaVersion   int                               `json:"schemaVersion"`
	Epoch           string                            `json:"epoch"`
	Revision        uint64                            `json:"revision"`
	ContextRevision string                            `json:"contextRevision"`
	ObservedAt      string                            `json:"observedAt"`
	Refreshing      bool                              `json:"refreshing"`
	Summary         ConfigurationSummary              `json:"summary"`
	Facts           []ConfigurationFact               `json:"facts"`
	Actions         []ConfigurationAction             `json:"actions"`
	Setup           managedfeishu.SetupState          `json:"setup"`
	Auth            *feishuprotocol.AuthStatus        `json:"auth,omitempty"`
	Overview        *domain.FeishuSettingsOverview    `json:"overview,omitempty"`
	Connection      domain.FeishuSnapshot             `json:"connection"`
	Flow            *feishuprotocol.ConfigurationFlow `json:"flow,omitempty"`
	Issues          []ConfigurationIssue              `json:"issues"`
}

type ConfigurationActionRequest struct {
	Action          string `json:"action"`
	RequestID       string `json:"requestId"`
	Epoch           string `json:"epoch"`
	Revision        uint64 `json:"revision"`
	ContextRevision string `json:"contextRevision"`
	Confirm         bool   `json:"confirm"`
	AppID           string `json:"appId,omitempty"`
	AppSecret       string `json:"appSecret,omitempty"`
	TargetAlias     string `json:"targetAlias,omitempty"`
	Feature         string `json:"feature,omitempty"`
	Mode            string `json:"mode,omitempty"`
	FlowID          string `json:"flowId,omitempty"`
}

type ConfigurationActionResult struct {
	Code     string                `json:"code,omitempty"`
	Outcome  string                `json:"outcome"`
	Snapshot ConfigurationSnapshot `json:"snapshot"`
	Message  string                `json:"message"`
}

type configurationRequestRecord struct {
	digest  string
	outcome string
	message string
}

type configurationData struct {
	setup             managedfeishu.SetupState
	settings          *managedfeishu.Settings
	connection        domain.FeishuSnapshot
	evidence          feishuprotocol.ConfigurationEvidence
	priorUserIdentity string
	priorUserAt       time.Time
	flow              *feishuprotocol.ConfigurationFlow
	quickAt           time.Time
	evidenceAt        time.Time
	quickFailed       bool
	evidenceFailed    bool
	invalidated       bool
	flowFailed        bool
}

type configurationRuntime struct {
	mu             sync.Mutex
	actionMu       sync.Mutex
	epoch          string
	revision       uint64
	mutation       uint64
	data           configurationData
	semantic       string
	refreshing     bool
	refreshingSlow bool
	acting         bool
	closed         bool
	forceSlow      bool
	lastQuick      time.Time
	lastSlow       time.Time
	cancel         context.CancelFunc
	requests       map[string]configurationRequestRecord
}

func configurationHash(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (state *configurationRuntime) initialize() {
	if state.epoch != "" {
		return
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		panic("configuration epoch unavailable")
	}
	state.epoch = hex.EncodeToString(random[:])
	state.revision = 1
	state.requests = map[string]configurationRequestRecord{}
	state.data.setup = managedfeishu.SetupState{Version: 1, Stage: "unknown"}
	state.data.connection = normalizedFeishuSnapshot(domain.FeishuSnapshot{Availability: "unknown", ProcessState: "unknown"})
}

func (service *Service) closeConfiguration() {
	state := &service.configuration
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	state.mutation++
	if state.cancel != nil {
		state.cancel()
	}
}

func (service *Service) readConfigurationEvidence(ctx context.Context) (feishuprotocol.ConfigurationEvidence, error) {
	var evidence feishuprotocol.ConfigurationEvidence
	if service.managedFeishuSupervisor == nil {
		return evidence, errors.New("configuration_desktop_unavailable")
	}
	if err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodConfigurationEvidence, map[string]any{}, &evidence); err != nil {
		return evidence, err
	}
	if evidence.SchemaVersion != 1 || !configurationEvidenceState(evidence.ApplicationState) {
		return evidence, errors.New("configuration_evidence_invalid")
	}
	return evidence, nil
}

func configurationEvidenceState(value string) bool {
	return value == "present" || value == "missing" || value == "unknown" || value == "failed" || value == "stale"
}

func (service *Service) ReadFeishuConfiguration(_ context.Context, refresh bool) ConfigurationSnapshot {
	state := &service.configuration
	state.mu.Lock()
	defer state.mu.Unlock()
	state.initialize()
	now := time.Now()
	if refresh && !state.refreshing && !state.acting {
		go service.reconcileConfigurationReceipts()
	}
	state.forceSlow = state.forceSlow || refresh
	if !state.closed && !state.acting && !state.refreshing && (refresh || now.Sub(state.lastQuick) >= feishuRefreshFloor) {
		state.refreshing = true
		state.lastQuick = now
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		state.cancel = cancel
		mutation := state.mutation
		slow := state.forceSlow || now.Sub(state.lastSlow) >= 2*time.Minute
		state.refreshingSlow = slow
		state.forceSlow = false
		if slow {
			state.lastSlow = now
		}
		go service.refreshConfiguration(ctx, cancel, mutation, slow)
	}
	snapshot := state.snapshot(now)
	snapshot.Diagnostics.RecentOperations = service.configurationFailures()
	for _, r := range snapshot.Diagnostics.RecentOperations {
		if r.Outcome == "unknown" || r.Outcome == "pending" {
			for i := range snapshot.Actions {
				if snapshot.Actions[i].ID == r.Action {
					snapshot.Actions[i].Enabled = false
					snapshot.Actions[i].Reason = "原请求尚待核实，请刷新查询。"
				}
			}
		}
		if r.Outcome == "unknown" || r.Outcome == "failed" {
			snapshot.Issues = append(snapshot.Issues, ConfigurationIssue{"operation", r.Action, configurationOperationTitle(r.Action) + "：" + r.Message})
		}
	}
	return snapshot
}

func (service *Service) configurationQuickRead(ctx context.Context, previous configurationData) configurationData {
	result := previous
	result.quickFailed, result.flowFailed = false, false
	if service.managedFeishuSupervisor == nil {
		result.quickFailed, result.flowFailed = true, true
		return result
	}
	quickCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var settings managedfeishu.Settings
	var setup managedfeishu.SetupState
	var flow *feishuprotocol.ConfigurationFlow
	if err := service.managedFeishuSupervisor.Call(quickCtx, feishuprotocol.SettingsRead, map[string]any{}, &settings); err != nil || settings.Validate() != nil {
		result.quickFailed = true
	} else {
		result.settings = &settings
	}
	if err := service.managedFeishuSupervisor.Call(quickCtx, feishuprotocol.SetupRead, map[string]any{}, &setup); err == nil {
		setup.VerificationURL, setup.UserCode = "", ""
		result.setup = setup
	}
	if connection, err := service.fetchFeishuSnapshot(quickCtx); err != nil {
		result.quickFailed = true
	} else {
		result.connection = service.composeIntegrationSnapshot(connection)
	}
	if err := service.managedFeishuSupervisor.Call(quickCtx, feishuprotocol.MethodConfigurationFlow, map[string]any{}, &flow); err != nil {
		result.flowFailed = true
	} else {
		result.flow = flow
	}
	if !result.quickFailed {
		result.quickAt = time.Now()
	}
	return result
}

func (service *Service) refreshConfiguration(ctx context.Context, cancel context.CancelFunc, mutation uint64, slow bool) {
	defer cancel()
	state := &service.configuration
	state.mu.Lock()
	previous := state.data
	state.mu.Unlock()
	var bridgeEpoch uint64
	if service.managedFeishuSupervisor != nil {
		bridgeEpoch = service.managedFeishuSupervisor.Generation()
	}
	current := service.configurationQuickRead(ctx, previous)
	state.mu.Lock()
	valid := state.mutation == mutation && !state.closed && (service.managedFeishuSupervisor == nil || service.managedFeishuSupervisor.IsCurrentGeneration(bridgeEpoch))
	// Completion is new authorization evidence, not a routine clock tick. Verify
	// identity in this observation instead of waiting for the two-minute calibration.
	// Consume the edge once; failures retain the existing bounded retry interval.
	completedUserFlow := !current.flowFailed && current.flow != nil && current.flow.Kind == "user" && current.flow.State == "completed"
	newCompletion := completedUserFlow && (previous.flow == nil || previous.flow.ID != current.flow.ID || previous.flow.State != "completed")
	if valid && !slow && newCompletion {
		slow = true
		state.lastSlow = time.Now()
	}
	if valid && !slow {
		state.data = current
	}
	state.mu.Unlock()
	if valid && slow && !current.quickFailed {
		evidence, err := service.readConfigurationEvidence(ctx)
		current.evidenceFailed = err != nil || evidence.ContextRevision == ""
		if !current.evidenceFailed {
			current.invalidated = false
			if confirmedFeishuUserAuth(current.evidence.Auth) && current.evidence.IdentityRevision != "" {
				current.priorUserIdentity, current.priorUserAt = current.evidence.IdentityRevision, current.evidenceAt
			}
			current.evidence, current.evidenceAt = evidence, time.Now()
		} else if current.evidenceAt.IsZero() && err == nil {
			current.evidence = evidence
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.mutation == mutation {
		state.refreshing = false
		if slow && current.quickFailed {
			state.forceSlow = true
		}
		if current.evidenceFailed {
			state.lastSlow = time.Now().Add(-2*time.Minute + 10*time.Second)
		}
		if !state.closed && (service.managedFeishuSupervisor == nil || service.managedFeishuSupervisor.IsCurrentGeneration(bridgeEpoch)) {
			state.data = current
			snapshot := state.snapshot(time.Now())
			defer func() { go service.reconcileConnectionRecovery(current, snapshot) }()
		} else {
			state.data.evidenceFailed, state.data.quickFailed = true, true
		}
	}
}

func configurationFactValue(state, present, missing string) string {
	switch state {
	case "present":
		return present
	case "missing":
		return missing
	case "failed":
		return "检查失败"
	case "stale":
		return "状态已变化，需重新检查"
	default:
		return "尚未验证"
	}
}

func (state *configurationRuntime) snapshot(now time.Time) ConfigurationSnapshot {
	data := state.data
	evidence := data.evidence
	stale := data.invalidated || data.evidenceFailed || data.evidenceAt.IsZero() || now.Sub(data.evidenceAt) > 2*time.Minute
	quickStale := data.quickFailed || data.quickAt.IsZero() || now.Sub(data.quickAt) > 10*time.Second
	result := ConfigurationSnapshot{SchemaVersion: 1, Epoch: state.epoch, Revision: state.revision, ObservedAt: now.UTC().Format(time.RFC3339Nano), Refreshing: state.refreshing && state.refreshingSlow || state.acting,
		Setup: data.setup, Auth: evidence.Auth, Connection: data.connection, Flow: data.flow, Facts: []ConfigurationFact{}, Actions: []ConfigurationAction{}, Issues: []ConfigurationIssue{}}
	if evidence.ContextRevision != "" && data.settings != nil {
		result.ContextRevision = configurationHash([]any{evidence.ContextRevision, data.settings})
	}
	add := func(id, title, status, present, missing, source string, outdated bool, checked time.Time) {
		if !configurationEvidenceState(status) {
			status = "unknown"
		}
		fact := ConfigurationFact{ID: id, Title: title, State: status, Value: configurationFactValue(status, present, missing), Source: source, Stale: outdated}
		if !checked.IsZero() {
			fact.CheckedAt = checked.UTC().Format(time.RFC3339Nano)
		}
		result.Facts = append(result.Facts, fact)
	}
	add("application", "接入应用", evidence.ApplicationState, "已验证", "未配置", "官方 CLI · default", stale, data.evidenceAt)
	if evidence.ApplicationID != "" {
		result.Facts[len(result.Facts)-1].Value = evidence.ApplicationID
	}
	userState := "unknown"
	if confirmedFeishuUserAuth(evidence.Auth) {
		userState = "present"
	} else if evidence.Auth != nil && evidence.Auth.Status == "unauthorized" {
		userState = "missing"
	} else if evidence.Auth != nil && evidence.Auth.Status == "failed" {
		userState = "failed"
	}
	add("user", "用户授权", userState, "已授权", "未授权", "官方 CLI · user", stale, data.evidenceAt)
	if evidence.UserName != "" && userState == "present" {
		result.Facts[len(result.Facts)-1].Value += " · " + evidence.UserName
	}
	if (userState == "failed" || userState == "unknown") && evidence.IdentityRevision != "" && evidence.IdentityRevision == data.priorUserIdentity && !data.priorUserAt.IsZero() {
		result.Facts[len(result.Facts)-1].Value += "；此前已授权（" + data.priorUserAt.UTC().Format(time.RFC3339) + "）"
	}
	add("bot", "机器人身份", evidence.BotState, "已验证", "不可用", "官方 CLI · bot", stale, data.evidenceAt)
	permissionState := "unknown"
	if evidence.UserPermissions == "present" && evidence.ApplicationPermissions == "present" {
		permissionState = "present"
	}
	if evidence.UserPermissions == "missing" || evidence.ApplicationPermissions == "missing" {
		permissionState = "missing"
	}
	add("permissions", "用户功能权限", permissionState, "已核验当前权限清单", "部分权限未具备", "官方 CLI · 应用权限与用户授权范围", stale, data.evidenceAt)
	connectionState := "unknown"
	if !data.quickAt.IsZero() {
		connectionState = "missing"
		if data.connection.InboundConnection {
			connectionState = "present"
		}
	}
	add("connection", "消息与卡片回调", connectionState, "已连接", "未连接", "飞书服务实际连接", quickStale, data.quickAt)
	add("operator", "任务控制权限", evidence.OperatorState, "已允许", "未允许", "KSFAssistant 访问规则", stale, data.evidenceAt)
	if data.settings != nil {
		result.Overview = &domain.FeishuSettingsOverview{State: data.connection.Availability, Summary: "飞书接入状态", Profile: feishuprotocol.ManagedEventProfile, Health: domain.FeishuSettingsHealth{Core: "running", Bridge: feishuServiceHealth(data.connection), Inbound: feishuInboundHealth(data.connection)}, Permissions: domain.FeishuPermissionOverview{Application: evidence.ApplicationPermissions, User: evidence.UserPermissions, Missing: []string{}}, Features: []domain.FeishuFeatureOverview{}, Targets: append([]string{}, data.connection.TargetAliases...)}
	}
	desktopState := "unknown"
	if capability, ok := data.connection.Capabilities["desktopIPC"]; ok {
		if capability.State == "ready" {
			desktopState = "present"
		} else {
			desktopState = "missing"
		}
	}
	add("desktop", "Codex Desktop 控制通道", desktopState, "已连接", "不可达", "Core 宿主观测", quickStale, data.quickAt)
	result.Summary = ConfigurationSummary{State: "checking", Title: "正在检查飞书配置", Detail: "应用、用户授权、消息连接和远程控制分别核验。", Tone: "neutral"}
	switch {
	case state.closed:
		result.Summary = ConfigurationSummary{"unavailable", "核心服务已停止", "已有配置不会被清除。", "warning"}
	case data.quickFailed && data.evidenceAt.IsZero():
		result.Summary = ConfigurationSummary{"unavailable", "暂时无法读取飞书服务状态", "请检查运行组件；这不代表应用未配置或用户授权已失效。", "warning"}
	case data.invalidated && !data.evidenceFailed:
		result.Summary = ConfigurationSummary{"checking", "正在核验配置变更", "保留此前状态，等待本次操作后的检查结果。", "neutral"}
	case evidence.ApplicationState == "missing" && !stale:
		result.Summary = ConfigurationSummary{"not_configured", "尚未配置飞书应用", "扫码创建应用，或接入已有应用。", "neutral"}
	case evidence.ApplicationState == "present" && !stale && connectionState == "present" && !quickStale:
		result.Summary = ConfigurationSummary{"connected", "飞书消息连接正常", "用户授权、远程控制与各项功能分别管理；连接成功不代表任务卡片全部验收通过。", "success"}
	case evidence.ApplicationState == "present" && !stale:
		result.Summary = ConfigurationSummary{"configured", "已接入飞书应用", "消息连接与功能模式请查看下方状态。", "neutral"}
	case data.evidenceFailed || evidence.ApplicationState == "failed" || !data.evidenceAt.IsZero() && stale:
		result.Summary = ConfigurationSummary{"unknown", "配置状态待复核", "检查未完成或证据已过期，保留此前状态；不会清空配置或改变功能。", "warning"}
	}
	if data.quickFailed {
		result.Issues = append(result.Issues, ConfigurationIssue{"runtime", "runtime_check_failed", "无法读取最新运行状态；此前状态仅供参考。"})
	}
	if data.evidenceFailed {
		result.Issues = append(result.Issues, ConfigurationIssue{"authorization", "authorization_check_failed", "应用或授权检查未完成；请重新检查，不要重复发起写入。"})
	}
	if data.flowFailed {
		result.Issues = append(result.Issues, ConfigurationIssue{"flow", "flow_check_failed", "授权状态更新失败，请刷新重试。"})
	}
	for _, problem := range evidence.Problems {
		result.Issues = append(result.Issues, ConfigurationIssue{"evidence", problem, "部分配置证据未能验证，请重新检查。"})
	}
	flowPending := result.Flow != nil && result.Flow.State == "pending"
	if result.Flow != nil && result.Flow.ExpiresAt != "" {
		if expiry, err := time.Parse(time.RFC3339Nano, result.Flow.ExpiresAt); err != nil || !expiry.After(now) {
			copy := *result.Flow
			copy.State, copy.VerificationURL, copy.UserCode, copy.QRDataURL = "expired", "", "", ""
			result.Flow, flowPending = &copy, false
		}
	}
	if result.Flow != nil && result.Flow.State != "pending" {
		copy := *result.Flow
		copy.VerificationURL, copy.UserCode, copy.QRDataURL = "", "", ""
		result.Flow = &copy
	}
	ready := !stale && !quickStale && !state.acting && !state.closed && result.ContextRevision != ""
	app := ready && evidence.ApplicationState == "present"
	addAction := func(id, title string, enabled bool, confirmation string) {
		action := ConfigurationAction{ID: id, Title: title, Enabled: enabled, Confirmation: confirmation}
		if !enabled {
			switch {
			case state.closed:
				action.Reason = "核心服务已停止。"
			case state.acting:
				action.Reason = "正在处理另一项配置操作。"
			case flowPending:
				action.Reason = "请先完成或取消当前授权流程。"
			case stale || quickStale:
				action.Reason = "请先取得当前配置与运行状态的有效检查结果。"
			case id == "create_app" && evidence.CreationBlocked:
				action.Reason = "已有业务配置或历史记录，请恢复原应用；不会清理数据后创建替代应用。"
			case id == "create_app" || id == "connect_app":
				action.Reason = "已有应用配置，不会覆盖或重复创建。"
			case id == "bind_operator":
				action.Reason = "仅当前用户身份已验证且尚未绑定时可操作。"
			case id == "logout":
				action.Reason = "当前没有已验证的用户授权可注销。"
			case id == "enable_outbound":
				action.Reason = "需要机器人身份可用，且主动出站尚未启用。"
			case id == "test_message":
				action.Reason = "需要已验证的机器人及本人单聊绑定。"
			default:
				action.Reason = "当前没有符合条件的操作，请查看对应状态。"
			}
		}
		result.Actions = append(result.Actions, action)
	}
	addAction("create_app", "扫码创建应用", ready && evidence.ApplicationState == "missing" && !evidence.CreationBlocked && !flowPending, "将通过官方 CLI 创建新的飞书应用，不会自动进行用户授权。")
	addAction("connect_app", "接入已有应用", ready && evidence.ApplicationState == "missing" && !flowPending, "将保存提供的飞书应用配置，不会自动绑定远程操作者。")
	authTitle := "授权用户身份"
	if userState == "present" {
		authTitle = "重新授权"
		if permissionState == "missing" {
			authTitle = "补充用户授权"
		}
	}
	addAction("start_auth", authTitle, app && !flowPending, "将申请当前功能所需用户权限；不会自动授予远程控制权限。")
	addAction("logout", "注销用户授权", app && userState == "present" && !flowPending, "先断开已连接任务并尝试更新飞书卡片，再退出登录；卡片更新失败也会注销，不会停止 Codex 任务。")
	operatorLabel := evidence.UserName
	if operatorLabel == "" && len(evidence.IdentityRevision) >= 12 {
		operatorLabel = "身份 " + evidence.IdentityRevision[:12]
	}
	if operatorLabel == "" {
		operatorLabel = "当前 CLI 用户"
	}
	addAction("bind_operator", "绑定当前用户为远程操作者", app && userState == "present" && evidence.OperatorState == "missing" && !flowPending, "允许当前已验证的飞书用户（"+operatorLabel+"）通过消息控制已授权的 Codex 任务，并更新“我”目标。")
	addAction("test_message", "发送测试消息", app && evidence.BotState == "present" && evidence.OperatorState == "present" && !flowPending, "以机器人身份向已核验的本人单聊发送一条固定测试消息；结果未知时不会重发。")
	addAction("restart", "重新连接飞书服务", !state.acting && !state.closed && !flowPending, "重启受管飞书服务，短暂中断消息连接；保留配置与业务队列。")
	flowReady := !state.acting && !state.closed && !data.flowFailed && result.Flow != nil
	addAction("cancel_flow", "取消本次等待", flowReady && flowPending, "仅停止当前流程的本地等待，不能撤销飞书端已完成的操作或已有授权。")
	addAction("finish_app", "检查应用创建结果", flowReady && result.Flow.Kind == "app" && (flowPending || result.Flow.State == "completed"), "")
	addAction("finish_auth", "检查用户授权结果", flowReady && result.Flow.Kind == "user" && (flowPending || result.Flow.State == "completed"), "")
	state.convergePresentation(&result, now)
	semanticFacts := append([]ConfigurationFact{}, result.Facts...)
	for index := range semanticFacts {
		semanticFacts[index].CheckedAt = ""
	}
	semantic := configurationHash([]any{result.ContextRevision, result.Summary, semanticFacts, result.Flow, result.Issues, result.Diagnostics, result.Actions})
	if state.semantic != semantic {
		state.semantic = semantic
		state.revision++
	}
	result.Revision = state.revision
	return result
}

func configurationActionByID(snapshot ConfigurationSnapshot, id string) (ConfigurationAction, bool) {
	for _, action := range snapshot.Actions {
		if action.ID == id {
			return action, true
		}
	}
	return ConfigurationAction{}, false
}

func validateConfigurationAction(request ConfigurationActionRequest) error {
	if request.Action == "set_feature" || request.Action == "enable_outbound" {
		return errors.New("configuration_action_retired")
	}
	if request.RequestID == "" || len(request.RequestID) > 128 || strings.ContainsAny(request.RequestID, "\x00\r\n") || len(request.AppSecret) > 4096 || len(request.AppID) > 128 || len(request.TargetAlias) > 200 {
		return errors.New("configuration_invalid_request")
	}
	if request.Action != "connect_app" && (request.AppID != "" || request.AppSecret != "") || request.Action != "test_message" && request.TargetAlias != "" || request.Action != "set_feature" && (request.Feature != "" || request.Mode != "") {
		return errors.New("configuration_unexpected_parameters")
	}
	if request.Action == "connect_app" && (strings.TrimSpace(request.AppID) == "" || request.AppSecret == "") {
		return errors.New("configuration_credentials_required")
	}
	if request.Action == "test_message" && request.TargetAlias == "" {
		return errors.New("configuration_target_required")
	}

	return nil
}

func (service *Service) applyFeishuConfiguration(ctx context.Context, request ConfigurationActionRequest) (ConfigurationActionResult, error) {
	if err := validateConfigurationAction(request); err != nil {
		return ConfigurationActionResult{}, err
	}
	state := &service.configuration
	if !state.actionMu.TryLock() {
		return ConfigurationActionResult{}, errors.New("configuration_busy")
	}
	defer state.actionMu.Unlock()
	state.mu.Lock()
	state.initialize()
	snapshot := state.snapshot(time.Now())
	digest := configurationHash(request)
	if record, exists := state.requests[request.RequestID]; exists {
		state.mu.Unlock()
		if record.digest != digest {
			return ConfigurationActionResult{}, errors.New("configuration_request_changed")
		}
		return ConfigurationActionResult{Outcome: record.outcome, Snapshot: snapshot, Message: record.message}, nil
	}
	if state.closed || snapshot.Epoch != request.Epoch || request.Revision == 0 || request.Revision > snapshot.Revision || snapshot.ContextRevision != request.ContextRevision {
		state.mu.Unlock()
		return ConfigurationActionResult{Outcome: "failed", Snapshot: snapshot, Message: "配置状态已变化，请查看当前状态后重新操作。"}, nil
	}
	action, exists := configurationActionByID(snapshot, request.Action)
	if !exists || !action.Enabled || action.Confirmation != "" && !request.Confirm {
		state.mu.Unlock()
		return ConfigurationActionResult{}, errors.New("configuration_action_not_allowed")
	}
	if len(state.requests) >= 128 {
		state.mu.Unlock()
		return ConfigurationActionResult{Outcome: "failed", Snapshot: snapshot, Message: "本次运行的配置操作数量已达上限，请重启应用后继续；本次尚未执行。"}, nil
	}
	if request.Action == "cancel_flow" || request.Action == "finish_app" || request.Action == "finish_auth" {
		if snapshot.Flow == nil || snapshot.Flow.ID != request.FlowID {
			state.mu.Unlock()
			return ConfigurationActionResult{}, errors.New("configuration_flow_changed")
		}
	} else if request.FlowID != "" {
		state.mu.Unlock()
		return ConfigurationActionResult{}, errors.New("configuration_unexpected_flow")
	}
	state.acting = true
	state.mutation++
	if state.cancel != nil {
		state.cancel()
	}
	state.refreshing = false
	previous := state.data
	state.mu.Unlock()
	defer func() { state.mu.Lock(); state.acting = false; state.mu.Unlock() }()
	fresh := service.configurationQuickRead(ctx, previous)
	flowAction := request.Action == "cancel_flow" || request.Action == "finish_app" || request.Action == "finish_auth"
	var checkErr error
	if !flowAction && request.Action != "restart" {
		var evidence feishuprotocol.ConfigurationEvidence
		evidence, checkErr = service.readConfigurationEvidence(ctx)
		fresh.evidenceFailed = checkErr != nil || evidence.ContextRevision == ""
		if !fresh.evidenceFailed {
			fresh.invalidated = false
			fresh.evidence = evidence
			fresh.evidenceAt = time.Now()
		}
	}
	state.mu.Lock()
	state.data = fresh
	state.acting = false
	checked := state.snapshot(time.Now())
	state.acting = true
	checkedAction, allowed := configurationActionByID(checked, request.Action)
	contextMatches := checked.ContextRevision == request.ContextRevision && checkErr == nil
	if flowAction {
		contextMatches = fresh.flow != nil && fresh.flow.ID == request.FlowID && !fresh.flowFailed
	}
	if request.Action == "restart" {
		contextMatches = true
	}
	if state.closed || ctx.Err() != nil || !contextMatches || !allowed || !checkedAction.Enabled || request.Action == "test_message" && (request.TargetAlias != fresh.evidence.OperatorAlias || !containsString(fresh.connection.TargetAliases, request.TargetAlias)) {
		state.mu.Unlock()
		return ConfigurationActionResult{Outcome: "failed", Snapshot: checked, Message: "操作前复核未通过或状态已变化；尚未执行，请检查当前状态。"}, nil
	}
	if err := service.saveConfigurationReceipt(ConfigurationReceipt{SchemaVersion: 1, RequestID: request.RequestID, Digest: digest, Action: request.Action, ContextRevision: request.ContextRevision, Outcome: "unknown", Stage: "submitted", ApplicationID: fresh.evidence.ApplicationID, PermissionRevision: fresh.evidence.PermissionRevision, Message: "已提交，正在核验执行结果。"}); err != nil {
		state.mu.Unlock()
		return ConfigurationActionResult{}, err
	}
	state.requests[request.RequestID] = configurationRequestRecord{digest: digest, outcome: "unknown", message: "执行结果尚未确认，不会重复执行。"}
	state.mu.Unlock()
	outcome, message, code := service.executeConfigurationAction(ctx, request, checked, fresh.settings, fresh.evidence)
	state.mu.Lock()
	state.acting = false
	// A read or message result is not an identity transition. Keep the visible
	// observation until its replacement is ready; mutations still require preflight.
	state.data.invalidated = request.Action != "test_message" && request.Action != "restart"
	if request.Action == "logout" && outcome == "completed" {
		state.data.evidence.Auth = &feishuprotocol.AuthStatus{SchemaVersion: 1, Status: "unauthorized", Identity: "user", Profile: "default"}
		state.data.evidence.UserName = ""
		state.data.evidence.OperatorState = "missing"
	}
	if outcome == "completed" && (request.Action == "cancel_flow" || request.Action == "logout" || request.Action == "finish_app") {
		state.data.flow = nil
	}
	state.lastQuick = time.Time{}
	state.lastSlow = time.Time{}
	state.forceSlow = true
	state.requests[request.RequestID] = configurationRequestRecord{digest: digest, outcome: outcome, message: message}
	state.mu.Unlock()
	return ConfigurationActionResult{Outcome: outcome, Code: code, Snapshot: service.ReadFeishuConfiguration(ctx, true), Message: message}, nil
}

func (service *Service) executeConfigurationAction(ctx context.Context, request ConfigurationActionRequest, snapshot ConfigurationSnapshot, expected *managedfeishu.Settings, expectedIdentity feishuprotocol.ConfigurationEvidence) (string, string, string) {
	var err error
	pending := false
	switch request.Action {
	case "create_app":
		var result map[string]any
		result, err = service.BeginFeishuSetup(ctx, managedfeishu.SetupModeNew, "", "")
		pending = result["status"] == "pending"
	case "connect_app":
		_, err = service.BeginFeishuSetup(ctx, managedfeishu.SetupModeExisting, request.AppID, request.AppSecret)
		if err == nil {
			err = service.restartFeishuSupervisor(ctx)
		}
	case "start_auth":
		var result feishuprotocol.AuthStatus
		result, err = service.StartDesktopFeishuAuth(ctx, feishuprotocol.AuthStartRequest{Scope: "required"})
		pending = result.Status == "pending"
	case "finish_auth":
		var result feishuprotocol.AuthStatus
		result, err = service.FinishDesktopFeishuAuth(ctx)
		pending = result.Status == "pending"
		if err == nil && !pending && (!confirmedFeishuUserAuth(&result) || len(result.MissingCapabilities) > 0) {
			err = errors.New("authorization_not_verified")
		}
	case "finish_app":
		var result map[string]any
		err = service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodAuthConfigFinish, map[string]any{}, &result)
		pending = result["status"] == "pending"
		if err == nil && !pending {
			if result["status"] != "completed" || result["flow"] != "app-create" {
				err = errors.New("application_not_verified")
			} else {
				err = service.restartFeishuSupervisor(ctx)
			}
		}
	case "cancel_flow":
		var result feishuprotocol.ConfigurationFlow
		err = service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodConfigurationCancel, feishuprotocol.ConfigurationCancelRequest{FlowID: request.FlowID, Kind: snapshot.Flow.Kind}, &result)
		if err == nil && (result.ID != request.FlowID || result.Kind != snapshot.Flow.Kind || result.State != "cancelled") {
			err = errors.New("cancellation_not_verified")
		}
	case "logout":
		var result feishuprotocol.AuthStatus
		result, err = service.LogoutFeishuAuth(ctx)
		if err == nil && result.Status != "unauthorized" {
			err = errors.New("logout_not_verified")
		}
	case "bind_operator":
		err = service.BindFeishuOperatorWithExpected(ctx, request.Confirm, expectedIdentity)
	case "test_message":
		err = service.sendConfigurationTest(ctx, request.TargetAlias, request.RequestID)
	case "restart":
		err = service.restartFeishuSupervisor(ctx)
	default:
		return "failed", "不支持的配置操作。", "configuration_preflight_failed"
	}
	if err != nil {
		if errors.Is(err, integration.ErrLogoutCardsPending) {
			return "failed", "尚未完成本地任务连接的退出准备，请刷新后重试；Codex 任务不会被停止。", "logout_cards_pending"
		}
		if errors.Is(err, ErrFeishuOperatorContextConflict) {
			return "failed", "确认时的应用或用户身份已变化；本次未绑定，请检查后重新确认。", "configuration_preflight_failed"
		}
		if errors.Is(err, ErrFeishuSettingsConflict) {
			return "failed", "功能设置已被其他操作修改；本次未覆盖，请检查后重新确认。", "configuration_preflight_failed"
		}
		code := err.Error()
		for _, known := range []string{"application_permissions_missing", "application_permissions_unverified", "test_not_submitted_self_unverified", "test_not_submitted_transport_unavailable", "test_definitive_failure", "authorization_not_verified", "logout_not_verified"} {
			if strings.Contains(code, known) {
				return "failed", "本次操作未完成（" + known + "），请查看诊断后处理。", known
			}
		}
		if request.Action == "restart" {
			code := "connection_restart_failed"
			if errors.Is(err, context.DeadlineExceeded) {
				code = "connection_restart_timeout"
			}
			if errors.Is(err, context.Canceled) {
				code = "connection_restart_canceled"
			}
			return "unknown", "恢复连接尚未确认，正在检查服务状态；无需重复操作。", code
		}
		return "unknown", "本次操作的回执尚未核实；可查询原请求，禁止重复执行。", "configuration_receipt_unverified"
	}
	if pending {
		if request.Action == "start_auth" || request.Action == "finish_auth" {
			return "pending", "等待你在飞书完成授权，完成后会自动核验登录结果。", "authorization_interaction_pending"
		}
		return "pending", "等待飞书确认应用创建结果。", "authorization_interaction_pending"
	}
	if request.Action == "cancel_flow" {
		return "completed", "本次本地等待已结束；远端可能已经完成，请以重新检查结果为准。", ""
	}
	if request.Action == "test_message" {
		return "completed", "测试消息已发送，已核对消息回执。", ""
	}
	return "completed", "本次操作已完成，正在重新核验当前状态。", ""
}
