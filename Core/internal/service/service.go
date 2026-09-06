package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/bridge"
	"ksfassistant/core/internal/codex"
	"ksfassistant/core/internal/desktop"
	"ksfassistant/core/internal/domain"
	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/pricing"
	"ksfassistant/core/internal/tokens"
)

const Version = "0.11.0-preview.3"

const (
	rateRefreshInterval      = 5 * time.Minute
	tokenRefreshInterval     = 30 * time.Minute
	activeLocalTokenInterval = 10 * time.Second
	idleLocalTokenInterval   = 30 * time.Minute
	threadRefreshInterval    = 10 * time.Second
	projectRefreshInterval   = 15 * time.Second
	feishuRefreshFloor       = 2500 * time.Millisecond
)

type DashboardRequest struct {
	KSFRoot          string                  `json:"ksfRoot"`
	PinnedProjectIDs []string                `json:"pinnedProjectIds"`
	PricingSelection domain.PricingSelection `json:"pricingSelection,omitempty"`
}

type InitializeRequest struct {
	Integrations IntegrationContextRequest `json:"integrations"`
}

type IntegrationContextRequest struct {
	KSFRoot string `json:"ksfRoot"`
}

type FeishuFeatureUpdateRequest struct {
	Feature          string `json:"feature"`
	Mode             string `json:"mode"`
	ConfirmRealWrite bool   `json:"confirmRealWrite"`
}

type FeishuOperationPrepareRequest struct {
	CapabilityID string         `json:"capabilityId"`
	Input        map[string]any `json:"input"`
	Source       string         `json:"source,omitempty"`
}

type FeishuOperationConfirmRequest struct {
	OperationID string `json:"operationId"`
	Challenge   string `json:"challenge"`
}

type FeishuOperationRequest struct {
	OperationID string `json:"operationId"`
}

type FeishuPolicyUpdateRequest struct {
	Policy           managedfeishu.CapabilityPolicy `json:"policy"`
	ExpectedRevision uint64                         `json:"expectedRevision"`
}

type CreateTaskRequest struct {
	ProjectID string `json:"projectId"`
	KSFRoot   string `json:"ksfRoot"`
	Purpose   string `json:"purpose"`
}

type TaskLinkRequest struct {
	ThreadID    string `json:"threadId"`
	Title       string `json:"title"`
	ProjectName string `json:"projectName"`
	TargetAlias string `json:"targetAlias"`
}

type SubmitTaskRequest struct {
	ThreadID string `json:"threadId"`
	HostID   string `json:"hostId"`
	CWD      string `json:"cwd"`
	Prompt   string `json:"prompt"`
}

type PrepareLaunchRequest struct {
	ProjectID string `json:"projectId"`
	KSFRoot   string `json:"ksfRoot"`
}

type TokenHistoryRequest struct {
	DayCount         int                     `json:"dayCount"`
	PricingSelection domain.PricingSelection `json:"pricingSelection,omitempty"`
	RepriceOnly      bool                    `json:"repriceOnly,omitempty"`
}

type PricingCatalogRequest struct {
	CustomPlans []domain.PricingPlan `json:"customPlans,omitempty"`
}

type Service struct {
	approvalMu              sync.Mutex
	userApprovals           *approvalState
	integrationRuntime      *integration.Runtime
	localGateway            *localipc.Server
	feishuGeneration        uint64
	home                    string
	codex                   *codex.Client
	ksf                     bridge.KSFClient
	desktop                 *desktop.ActivityClient
	mu                      sync.Mutex
	lastUsage               domain.UsageSnapshot
	lastThreads             []domain.CodexThread
	lastRateAttempt         time.Time
	lastTokenAttempt        time.Time
	lastLocalTokenAttempt   time.Time
	lastThreadAttempt       time.Time
	projectSources          map[string]projectSourceCache
	feishuDataRoot          string
	managedFeishuSupervisor *managedfeishu.Supervisor
	hostContextStore        integration.HostContextStore
	lastFeishuAt            time.Time
	lastFeishu              domain.FeishuSnapshot
	lastTokenHistoryAt      time.Time
	lastTokenHistoryDays    int
	lastTokenHistory        []domain.DailyUsageBucket
	lastServerHistoryAt     time.Time
	lastServerHistory       []domain.DailyUsageBucket
	trackingAt              map[string]time.Time
}

type projectSourceCache struct {
	catalog       []domain.Project
	projections   map[string]domain.TaskProjection
	usage         map[string]domain.ProjectUsageSummary
	launchActions map[string]domain.ProjectLaunchAction
	threadStamp   string
	refreshedAt   time.Time
	message       string
}

func New() *Service {
	home, _ := os.UserHomeDir()
	dataRoot := os.Getenv("FEISHU_BRIDGE_DATA_DIR")
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = filepath.Join(home, ".config", "feishu-bridge")
	}
	var managedSupervisor *managedfeishu.Supervisor
	var service *Service
	hostContextStore := integration.NewHostContextStore(dataRoot)
	if executable := strings.TrimSpace(os.Getenv("KSF_ASSISTANT_FEISHU_BRIDGE")); executable != "" {
		environment := []string{
			"FEISHU_BRIDGE_DATA_DIR=" + dataRoot,
		}
		if larkCLI := strings.TrimSpace(os.Getenv("KSF_ASSISTANT_LARK_CLI")); larkCLI != "" {
			environment = append(environment, "LARK_CLI_BIN="+larkCLI)
		}
		managedSupervisor = managedfeishu.NewSupervisor(managedfeishu.SupervisorOptions{
			Executable:  executable,
			Directory:   filepath.Dir(executable),
			DataRoot:    dataRoot,
			Environment: environment,
			OnConnect: func(ctx context.Context, generation uint64) error {
				return service.connectManagedBridge(ctx, generation)
			},
		})

	}
	service = &Service{
		home:                    home,
		feishuDataRoot:          dataRoot,
		managedFeishuSupervisor: managedSupervisor,
		hostContextStore:        hostContextStore,
		desktop:                 desktop.New(desktop.DefaultEndpoint(home)),
		trackingAt:              map[string]time.Time{},
		projectSources:          map[string]projectSourceCache{},
		lastUsage:               domain.UsageSnapshot{Buckets: []domain.RateLimitBucket{}, DailyUsageBuckets: []domain.DailyUsageBucket{}, Status: "loading"},
	}
	if managedSupervisor != nil {
		_ = managedSupervisor.SetHandler(service)
	}
	return service
}

func ksfAssistantSupportRoot(home string) string {
	if root, err := os.UserConfigDir(); err == nil && strings.TrimSpace(root) != "" {
		return filepath.Join(root, "KSFAssistant")
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "KSFAssistant")
	case "windows":
		return filepath.Join(home, "AppData", "Local", "KSFAssistant")
	default:
		return filepath.Join(home, ".config", "KSFAssistant")
	}
}

func (service *Service) Initialize(ctx context.Context, request InitializeRequest) map[string]any {
	executable, err := configureCodexProcessEnvironment(service.home)
	if err == nil {
		service.codex = &codex.Client{Executable: executable, Timeout: 15 * time.Second}
		_ = service.codex.Start(ctx)
	}
	_ = service.desktop.Start(ctx)
	hostContext, hostContextErr := service.UpdateIntegrationContext(request.Integrations)
	integrationErr := service.initializeBusinessIntegration()
	bridgeStartErr := service.startFeishuSupervisor()
	if integrationErr != nil {
		bridgeStartErr = errors.Join(bridgeStartErr, integrationErr)
	}
	result := map[string]any{
		"protocol": domain.Protocol,
		"version":  Version,
		"platform": runtime.GOOS,
		"capabilities": map[string]bool{
			"usage": true, "localTokens": true, "tokenHistory": true, "tokenHistoryComparison": true, "tokenCostEstimate": true, "projects": true, "taskActivity": true,
			"taskCreation": true, "projectLaunch": true, "feishuTaskLinks": true, "feishuServiceManagement": true, "feishuCapabilityGovernance": true, "userWriteApproval": true,
		},
	}
	if hostContextErr == nil {
		result["integrations"] = hostContext
	} else {
		result["integrationError"] = "KSF 上下文暂不可用，飞书传输仍可独立运行"
	}
	if bridgeStartErr != nil {
		result["feishuError"] = bridgeStartErr.Error()
	}
	return result
}

func (service *Service) UpdateIntegrationContext(request IntegrationContextRequest) (integration.HostContext, error) {
	return service.hostContextStore.SaveKSFRoot(request.KSFRoot)
}

func configureCodexProcessEnvironment(home string) (string, error) {
	executable, err := codex.LocateExecutable(home)
	if err != nil {
		return "", err
	}
	if err := os.Setenv("CODEX_BIN", executable); err != nil {
		return "", fmt.Errorf("unable to configure Codex executable: %w", err)
	}
	return executable, nil
}

func (service *Service) Dashboard(ctx context.Context, request DashboardRequest) domain.DashboardSnapshot {
	now := time.Now()
	activity := service.desktop.Snapshot(now)
	desktopActivity := activity
	activeHint := activity.RunningCount > 0 || activity.WaitingCount > 0
	usage, threads := service.readCodex(ctx, now, activeHint)
	plan, _ := pricing.Resolve(request.PricingSelection)
	if usage.LocalDailyUsage != nil {
		estimate := pricing.Estimate(plan, usage.LocalDailyUsage.Tokens, usage.LocalDailyUsage.Breakdown)
		usage.LocalDailyCost = &estimate
	}
	fallbackObservations := codex.Observations(threads)
	observations := activity.Observations
	if activity.Availability != "available" || len(observations) == 0 {
		observations = fallbackObservations
		activity = domain.SummarizeActivity(observations, now)
		if service.codex == nil {
			activity.Availability = "offline"
		}
	}
	projects := service.readProjects(ctx, request, threads, observations, now)
	service.enrichTaskRuntime(ctx, request.KSFRoot, &projects, desktopActivity, now)
	feishu := normalizedFeishuSnapshot(domain.FeishuSnapshot{Availability: "notConfigured"})
	if service.hasFeishuRuntime() {
		feishu = service.readFeishu(ctx, now)
	}
	return domain.DashboardSnapshot{Protocol: domain.Protocol, CoreVersion: Version, Platform: runtime.GOOS, ObservedAt: now, Usage: usage, Activity: activity, Projects: projects, Feishu: feishu}
}

func (service *Service) hasFeishuRuntime() bool {
	return service.managedFeishuSupervisor != nil
}

func (service *Service) PricingCatalog(request PricingCatalogRequest) domain.PricingCatalog {
	return pricing.Catalog(request.CustomPlans)
}

func (service *Service) CreateTask(ctx context.Context, request CreateTaskRequest) (map[string]string, error) {
	if service.codex == nil {
		return nil, errors.New("未找到 Codex，无法新建任务")
	}
	root, err := canonicalDirectory(request.KSFRoot)
	if err != nil {
		return nil, errors.New("KSF 根目录无效，无法新建任务")
	}
	catalog, err := service.ksf.Catalog(ctx, root)
	if err != nil {
		return nil, err
	}
	project, ok := projectByID(catalog.Projects, request.ProjectID)
	if !ok {
		return nil, errors.New("项目不存在或尚未同步")
	}
	name, prompt, err := taskBootstrap(project, root, request.Purpose)
	if err != nil {
		return nil, err
	}
	threadID, err := service.codex.CreateDraftThread(ctx, root, name)
	if err != nil {
		return nil, err
	}
	return map[string]string{"threadId": threadID, "name": name, "prompt": prompt, "submission": "desktop-required"}, nil
}

func (service *Service) SubmitTask(ctx context.Context, request SubmitTaskRequest) error {
	return service.desktop.StartTurn(ctx, request.ThreadID, request.HostID, request.CWD, request.Prompt)
}

func (service *Service) CreateTaskLink(ctx context.Context, request TaskLinkRequest) (domain.FeishuTaskLink, error) {
	if service.integrationRuntime == nil {
		return domain.FeishuTaskLink{}, errors.New("Core integration unavailable")
	}
	cwd := ""
	if service.desktop != nil {
		if state, found := service.desktop.CachedConversationState(request.ThreadID); found {
			cwd = recursiveControlString(state, "cwd", "workingDirectory", "workspaceRoot")
		}
	}
	link, err := service.integrationRuntime.CreateTaskLink(ctx, integration.CreateTaskLinkRequest{ThreadID: request.ThreadID, Title: request.Title, ProjectName: request.ProjectName, TargetAlias: request.TargetAlias, CWD: cwd})
	return publicIntegrationLink(link), err
}
func (service *Service) ReleaseTaskLink(ctx context.Context, request TaskLinkRequest) (domain.FeishuTaskLink, error) {
	if service.integrationRuntime == nil {
		return domain.FeishuTaskLink{}, errors.New("Core integration unavailable")
	}
	link, err := service.integrationRuntime.Release(ctx, domain.PublicTaskKey(request.ThreadID))
	return publicIntegrationLink(link), err
}
func (service *Service) InterruptTaskLink(ctx context.Context, request TaskLinkRequest) (domain.FeishuTaskLink, error) {
	if service.integrationRuntime == nil {
		return domain.FeishuTaskLink{}, errors.New("Core integration unavailable")
	}
	link, err := service.integrationRuntime.Interrupt(ctx, domain.PublicTaskKey(request.ThreadID))
	return publicIntegrationLink(link), err
}

func (service *Service) SendFeishuTest(ctx context.Context, target string) error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu is unavailable")
	}
	return service.managedFeishuSupervisor.Call(ctx, "bridge/message/test", map[string]any{"targetAlias": target}, nil)
}

func (service *Service) PrepareFeishuOperation(ctx context.Context, request FeishuOperationPrepareRequest) (managedfeishu.PreparedOperation, error) {
	if service.managedFeishuSupervisor == nil {
		return managedfeishu.PreparedOperation{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result managedfeishu.PreparedOperation
	err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodOperationPrepare, request, &result)
	return result, err
}

func (service *Service) ConfirmFeishuOperation(ctx context.Context, request FeishuOperationConfirmRequest) (managedfeishu.PreparedOperation, error) {
	if service.managedFeishuSupervisor == nil {
		return managedfeishu.PreparedOperation{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result managedfeishu.PreparedOperation
	err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodOperationConfirm, request, &result)
	return result, err
}

func (service *Service) CancelFeishuOperation(ctx context.Context, request FeishuOperationRequest) (managedfeishu.OperationView, error) {
	if service.managedFeishuSupervisor == nil {
		return managedfeishu.OperationView{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result managedfeishu.OperationView
	err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodOperationCancel, request, &result)
	return result, err
}

func (service *Service) FeishuOperationStatus(ctx context.Context, request FeishuOperationRequest) (managedfeishu.OperationView, error) {
	if service.managedFeishuSupervisor == nil {
		return managedfeishu.OperationView{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result managedfeishu.OperationView
	err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodOperationStatus, request, &result)
	return result, err
}

func (service *Service) FeishuCapabilityPolicy(ctx context.Context) (managedfeishu.CapabilityPolicy, error) {
	if service.managedFeishuSupervisor == nil {
		return managedfeishu.CapabilityPolicy{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result managedfeishu.CapabilityPolicy
	err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodPolicyRead, map[string]any{}, &result)
	return result, err
}

func (service *Service) UpdateFeishuCapabilityPolicy(ctx context.Context, request FeishuPolicyUpdateRequest) (managedfeishu.CapabilityPolicy, error) {
	if service.managedFeishuSupervisor == nil {
		return managedfeishu.CapabilityPolicy{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result managedfeishu.CapabilityPolicy
	err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodPolicyUpdate, request, &result)
	return result, err
}

func (service *Service) FeishuProfile(ctx context.Context) (map[string]any, error) {
	settings, err := service.FeishuSettings()
	return map[string]any{"status": "ok", "eventConsumer": map[string]any{"profile": settings.Profile, "profileValid": err == nil, "desiredConnection": settings.Profile == managedfeishu.ProfilePrimary}}, err
}

func (service *Service) ConfigureFeishu(ctx context.Context, appID, appSecret string) error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu is unavailable")
	}
	var result map[string]any
	return service.managedFeishuSupervisor.Call(ctx, "bridge/auth/configure", map[string]any{"appId": appID, "appSecret": appSecret, "brand": "feishu", "profile": "default"}, &result)
}

func (service *Service) FeishuSettings() (managedfeishu.Settings, error) {
	return (remoteSettingsStore{service}).Load()
}

func (service *Service) UpdateFeishuSettings(settings managedfeishu.Settings) (managedfeishu.Settings, error) {
	store := remoteSettingsStore{service}
	previous, err := store.Load()
	if err != nil {
		return managedfeishu.Settings{}, err
	}
	if err := store.Save(settings); err != nil {
		return managedfeishu.Settings{}, err
	}
	if service.managedFeishuSupervisor == nil {
		return store.Load()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var applied managedfeishu.Settings
	if err := service.managedFeishuSupervisor.Call(ctx, "bridge/settings/reload", map[string]any{}, &applied); err != nil {
		_ = store.Save(previous)
		return managedfeishu.Settings{}, err
	}
	if err := service.restartFeishuSupervisor(ctx); err != nil {
		_ = store.Save(previous)
		_ = service.restartFeishuSupervisor(context.Background())
		return managedfeishu.Settings{}, err
	}
	return store.Load()
}

func (service *Service) FeishuSetup() (managedfeishu.SetupState, error) {
	return (remoteSetupStore{service}).Load()
}

func (service *Service) BeginFeishuSetup(ctx context.Context, mode, appID, appSecret string) (map[string]any, error) {
	state := managedfeishu.DefaultSetupState()
	state.Mode = mode
	var result map[string]any
	var err error
	switch mode {
	case managedfeishu.SetupModeExisting:
		if strings.TrimSpace(appID) == "" || appSecret == "" {
			return nil, errors.New("请在软件内填写 App ID 和 App Secret")
		}
		err = service.ConfigureFeishu(ctx, strings.TrimSpace(appID), appSecret)
		state.Stage = managedfeishu.SetupAppConfigured
		result = map[string]any{"status": "configured", "flow": "existing-app"}
	case managedfeishu.SetupModeNew:
		if service.managedFeishuSupervisor == nil {
			return nil, errors.New("KSFAssistant Feishu is unavailable")
		}
		err = service.managedFeishuSupervisor.Call(ctx, "bridge/auth/start", map[string]any{"kind": "config", "profile": "default", "createNew": true}, &result)
		state.Stage = managedfeishu.SetupAppPending
		if result != nil {
			state.VerificationURL, _ = result["verificationUrl"].(string)
		}
	default:
		return nil, errors.New("不支持的飞书配置方式")
	}
	store := remoteSetupStore{service}
	if err != nil {
		state.Stage = managedfeishu.SetupFailed
		state.LastError = safeSetupError(err)
		_ = store.Save(state)
		return nil, err
	}
	if err := store.Save(state); err != nil {
		return nil, err
	}
	if service.managedFeishuSupervisor != nil {
		service.managedFeishuSupervisor.SetConfigured(false)
	}
	result["setup"] = state
	return result, nil
}

func (service *Service) ContinueFeishuSetup(ctx context.Context) (map[string]any, error) {
	store := remoteSetupStore{service}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	var result map[string]any
	verifyAfterSave := false
	switch state.Stage {
	case managedfeishu.SetupAppPending, managedfeishu.SetupAppConfigured:
		result, err = service.StartFeishuAuth(ctx)
		if err == nil {
			state.Stage = managedfeishu.SetupAuthorizationPending
			state.VerificationURL, _ = result["verificationUrl"].(string)
			state.UserCode, _ = result["userCode"].(string)
		}
	case managedfeishu.SetupAuthorizationPending:
		err = service.ensureCurrentFeishuUser(ctx)
		if err != nil {
			err = service.FinishFeishuAuth(ctx)
			if err == nil {
				err = service.ensureCurrentFeishuUser(ctx)
			}
		}
		if err == nil {
			state.Stage = managedfeishu.SetupPlatformPending
			state.VerificationURL = ""
			state.UserCode = ""
			result = map[string]any{"status": "authenticated"}
			verifyAfterSave = true
		}
	case managedfeishu.SetupPlatformPending, managedfeishu.SetupFailed:
		return service.VerifyFeishuSetup(ctx)
	case managedfeishu.SetupReady:
		return map[string]any{"status": "ready", "setup": state}, nil
	default:
		return nil, errors.New("请先开始飞书配置")
	}
	if err != nil {
		state.LastError = safeSetupError(err)
		_ = store.Save(state)
		return nil, err
	}
	state.LastError = ""
	if err := store.Save(state); err != nil {
		return nil, err
	}
	if verifyAfterSave {
		return service.VerifyFeishuSetup(ctx)
	}
	result["setup"] = state
	return result, nil
}

func (service *Service) VerifyFeishuSetup(ctx context.Context) (map[string]any, error) {
	permissions, err := service.FeishuPermissions(ctx)
	store := remoteSetupStore{service}
	state, loadErr := store.Load()
	if loadErr != nil {
		return nil, loadErr
	}
	if err != nil {
		state.Stage = managedfeishu.SetupFailed
		state.LastError = safeSetupError(err)
		_ = store.Save(state)
		return nil, err
	}
	state.Stage = managedfeishu.SetupPlatformPending
	state.ReadyToActivate = false
	if permissionsReady(permissions) {
		if err := service.ensureCurrentFeishuUser(ctx); err != nil {
			state.Stage = managedfeishu.SetupFailed
			state.LastError = safeSetupError(err)
			_ = store.Save(state)
			return nil, err
		}
		if err := service.prepareFeishuDryRun(ctx); err != nil {
			state.Stage = managedfeishu.SetupFailed
			state.LastError = safeSetupError(err)
			_ = store.Save(state)
			return nil, err
		}
		state.ReadyToActivate = true
	}
	state.LastError = ""
	if err := store.Save(state); err != nil {
		return nil, err
	}
	if service.managedFeishuSupervisor != nil {
		service.managedFeishuSupervisor.SetConfigured(feishuSetupConfiguresBridge(state.Stage))
	}
	return map[string]any{"status": state.Stage, "setup": state, "permissions": permissions["permissions"]}, nil
}

func (service *Service) ActivateFeishuSetup(ctx context.Context, targetAlias string) (map[string]any, error) {
	store := remoteSetupStore{service}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	targetAlias = strings.TrimSpace(targetAlias)
	if !state.ReadyToActivate || (state.Stage != managedfeishu.SetupPlatformPending && state.Stage != managedfeishu.SetupFailed) {
		return nil, errors.New("请先重新检查飞书后台设置")
	}
	if targetAlias == "" {
		return nil, errors.New("请选择软件内显示的测试目标")
	}
	snapshot := service.readFeishu(ctx, time.Now())
	if !containsString(snapshot.TargetAliases, targetAlias) {
		return nil, errors.New("测试目标已失效，请重新检查飞书配置")
	}

	settingsStore := remoteSettingsStore{service}
	previous, err := settingsStore.Load()
	if err != nil {
		return nil, err
	}
	activated := activateFeishuSettings(previous)
	if err := service.saveAndRestartFeishuSettings(ctx, activated); err != nil {
		return nil, err
	}
	_, err = service.waitForFeishuAvailability(ctx, "ready", 45*time.Second)
	if err == nil {
		err = service.SendFeishuTest(ctx, targetAlias)
	}
	if err != nil {
		rollbackContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = service.saveAndRestartFeishuSettings(rollbackContext, prepareFeishuDryRunSettings(previous))
		state.Stage = managedfeishu.SetupFailed
		state.ReadyToActivate = true
		state.LastError = safeSetupError(err)
		_ = store.Save(state)
		return nil, err
	}

	state.Stage = managedfeishu.SetupReady
	state.ReadyToActivate = false
	state.LastError = ""
	if err := store.Save(state); err != nil {
		return nil, err
	}
	if service.managedFeishuSupervisor != nil {
		service.managedFeishuSupervisor.SetConfigured(true)
	}
	service.clearFeishuCache()
	return map[string]any{"status": state.Stage, "setup": state}, nil
}

func (service *Service) prepareFeishuDryRun(ctx context.Context) error {
	store := remoteSettingsStore{service}
	settings, err := store.Load()
	if err != nil {
		return err
	}
	settings = prepareFeishuDryRunSettings(settings)
	if err := service.saveAndRestartFeishuSettings(ctx, settings); err != nil {
		return err
	}
	snapshot, err := service.waitForFeishuAvailability(ctx, "dryRun", 45*time.Second)
	if err != nil {
		return err
	}
	target := preferredFeishuSetupTarget(snapshot.TargetAliases)
	if target == "" {
		return errors.New("未找到软件自动授权的飞书测试目标")
	}
	return service.SendFeishuTest(ctx, target)
}

func (service *Service) saveAndRestartFeishuSettings(ctx context.Context, settings managedfeishu.Settings) error {
	store := remoteSettingsStore{service}
	previous, err := store.Load()
	if err != nil {
		return err
	}
	if err := store.Save(settings); err != nil {
		return err
	}
	if err := service.restartFeishuSupervisor(ctx); err != nil {
		if rollbackErr := store.Save(previous); rollbackErr != nil {
			return fmt.Errorf("重启飞书服务失败，且无法恢复原设置：%v；恢复失败：%w", err, rollbackErr)
		}
		return err
	}
	service.clearFeishuCache()
	return nil
}

func (service *Service) waitForFeishuAvailability(ctx context.Context, expected string, timeout time.Duration) (domain.FeishuSnapshot, error) {
	if service.managedFeishuSupervisor == nil {
		return domain.FeishuSnapshot{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	return waitForSetupSnapshot(ctx, expected, timeout, service.fetchFeishuSnapshot)
}

func prepareFeishuDryRunSettings(settings managedfeishu.Settings) managedfeishu.Settings {
	settings.Profile = managedfeishu.ProfilePrimary
	settings.Outbound.Enabled = true
	settings.Outbound.DryRun = true
	return settings
}

func activateFeishuSettings(settings managedfeishu.Settings) managedfeishu.Settings {
	settings.Outbound.Enabled = true
	settings.Outbound.DryRun = false
	return settings
}

func feishuSetupCanBecomeReady(settings managedfeishu.Settings) bool {
	return settings.Outbound.Enabled && !settings.Outbound.DryRun
}

func preferredFeishuSetupTarget(aliases []string) string {
	for _, alias := range aliases {
		if alias == "我" {
			return alias
		}
	}
	if len(aliases) == 1 {
		return aliases[0]
	}
	return ""
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (service *Service) CancelFeishuSetup() (managedfeishu.SetupState, error) {
	if service.managedFeishuSupervisor == nil {
		return managedfeishu.SetupState{}, errors.New("飞书授权服务不可用，尚未取消配置")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodAuthCancel, map[string]any{}, nil); err != nil {
		return managedfeishu.SetupState{}, errors.New("未能终止飞书授权，请重试取消")
	}
	state := managedfeishu.DefaultSetupState()
	err := (remoteSetupStore{service}).Save(state)
	if err == nil && service.managedFeishuSupervisor != nil {
		service.managedFeishuSupervisor.SetConfigured(false)
	}
	return state, err
}

func feishuSetupConfiguresBridge(stage string) bool {
	return stage == managedfeishu.SetupReady
}

func safeSetupError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > 1000 {
		message = message[:1000]
	}
	message = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, message)
	return strings.TrimSpace(message)
}

func permissionsReady(result map[string]any) bool {
	permissions, _ := result["permissions"].(map[string]any)
	verified, _ := permissions["verified"].(bool)
	identities, _ := permissions["identities"].(map[string]any)
	user, _ := identities["user"].(map[string]any)
	ready, _ := user["ready"].(bool)
	missing, _ := user["missing"].([]any)
	return verified && ready && len(missing) == 0
}

func (service *Service) StartFeishuAuth(ctx context.Context) (map[string]any, error) {
	if service.managedFeishuSupervisor == nil {
		return nil, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result map[string]any
	err := service.managedFeishuSupervisor.Call(ctx, "bridge/auth/start", map[string]any{"kind": "user", "scope": "required"}, &result)
	return result, err
}

func (service *Service) FinishFeishuAuth(ctx context.Context) error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu is unavailable")
	}
	var result map[string]any
	return service.managedFeishuSupervisor.Call(ctx, "bridge/auth/finish", map[string]any{}, &result)
}

func (service *Service) ensureCurrentFeishuUser(ctx context.Context) error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu is unavailable")
	}
	var result map[string]any
	return service.managedFeishuSupervisor.Call(ctx, "bridge/auth/ensureCurrentUser", map[string]any{}, &result)
}

func (service *Service) FeishuPermissions(ctx context.Context) (map[string]any, error) {
	if service.managedFeishuSupervisor == nil {
		return nil, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result map[string]any
	err := service.managedFeishuSupervisor.Call(ctx, "bridge/permissions/read", map[string]any{}, &result)
	return result, err
}

func (service *Service) FeishuSettingsOverview(ctx context.Context) (domain.FeishuSettingsOverview, error) {
	settings, err := service.FeishuSettings()
	if err != nil {
		return domain.FeishuSettingsOverview{}, err
	}
	snapshot := service.readFeishu(ctx, time.Now())
	permissions, permissionErr := service.FeishuPermissions(ctx)
	overview := domain.FeishuSettingsOverview{
		State:       snapshot.Availability,
		Summary:     feishuOverviewSummary(snapshot),
		Profile:     settings.Profile,
		Health:      domain.FeishuSettingsHealth{Core: "running", Bridge: feishuServiceHealth(snapshot), Inbound: feishuInboundHealth(settings.Profile, snapshot), Detail: snapshot.Message},
		Permissions: feishuPermissionOverview(permissions, permissionErr),
		Features:    feishuFeatureOverview(settings),
		Targets:     snapshot.TargetAliases,
	}
	return overview, nil
}

func (service *Service) UpdateFeishuFeature(ctx context.Context, request FeishuFeatureUpdateRequest) (domain.FeishuSettingsOverview, error) {
	settings, err := service.FeishuSettings()
	if err != nil {
		return domain.FeishuSettingsOverview{}, err
	}
	mode := strings.TrimSpace(request.Mode)
	switch request.Feature {
	case "groupMessaging":
		if mode != "off" && mode != "enabled" {
			return domain.FeishuSettingsOverview{}, errors.New("群聊消息处理只支持关闭或启用")
		}
		settings.Group.Enabled = mode == "enabled"
	case "peopleDirectory":
		if mode != "off" && mode != "enabled" {
			return domain.FeishuSettingsOverview{}, errors.New("人员查询只支持关闭或启用")
		}
		settings.Directory.Enabled = mode == "enabled"
	case "groupDirectory":
		if mode != "off" && mode != "enabled" {
			return domain.FeishuSettingsOverview{}, errors.New("群组查询只支持关闭或启用")
		}
		settings.GroupDirectory.Enabled = mode == "enabled"
	case "docbox", "actionbox":
		if mode != "off" && mode != "dry_run" && mode != "live" {
			return domain.FeishuSettingsOverview{}, errors.New("写入能力只支持关闭、演练或真实执行")
		}
		if mode == "live" && !request.ConfirmRealWrite {
			return domain.FeishuSettingsOverview{}, errors.New("允许真实执行前需要明确确认")
		}
		value := managedfeishu.DryRunSwitch{Enabled: mode != "off", DryRun: mode != "live"}
		if request.Feature == "docbox" {
			settings.Docbox = value
		} else {
			settings.Actionbox = value
		}
	default:
		return domain.FeishuSettingsOverview{}, errors.New("不支持的飞书高级功能")
	}
	if err := service.saveAndRestartFeishuSettings(ctx, settings); err != nil {
		return domain.FeishuSettingsOverview{}, err
	}
	return service.FeishuSettingsOverview(ctx)
}

func feishuFeatureOverview(settings managedfeishu.Settings) []domain.FeishuFeatureOverview {
	switchState := func(enabled bool) string {
		if enabled {
			return "enabled"
		}
		return "off"
	}
	writeState := func(value managedfeishu.DryRunSwitch) string {
		if !value.Enabled {
			return "off"
		}
		if value.DryRun {
			return "dry_run"
		}
		return "live"
	}
	return []domain.FeishuFeatureOverview{
		{ID: "groupMessaging", Title: "群聊消息处理", Description: "在群聊中接收并处理消息", State: switchState(settings.Group.Enabled)},
		{ID: "peopleDirectory", Title: "人员查询", Description: "按姓名查询已授权人员", State: switchState(settings.Directory.Enabled)},
		{ID: "groupDirectory", Title: "群组查询", Description: "按名称查询已授权群组", State: switchState(settings.GroupDirectory.Enabled)},
		{ID: "docbox", Title: "文档与知识库", Description: "通过受控队列处理文档写入", State: writeState(settings.Docbox), Writable: true},
		{ID: "actionbox", Title: "自动化与队列", Description: "通过受控队列执行自动化操作", State: writeState(settings.Actionbox), Writable: true},
	}
}

func feishuPermissionOverview(value map[string]any, callErr error) domain.FeishuPermissionOverview {
	if callErr != nil {
		return domain.FeishuPermissionOverview{Application: "unavailable", User: "unavailable", Missing: []string{}}
	}
	data, _ := json.Marshal(value)
	var report struct {
		Permissions struct {
			Verified   bool `json:"verified"`
			Identities struct {
				User struct {
					Ready       bool `json:"ready"`
					Application *struct {
						Missing []string `json:"missing"`
					} `json:"application"`
					OAuth struct {
						Missing []string `json:"missing"`
					} `json:"oauth"`
				} `json:"user"`
			} `json:"identities"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &report) != nil {
		return domain.FeishuPermissionOverview{Application: "unavailable", User: "unavailable", Missing: []string{}}
	}
	missing := append([]string{}, report.Permissions.Identities.User.OAuth.Missing...)
	if app := report.Permissions.Identities.User.Application; app != nil {
		missing = append(missing, app.Missing...)
	}
	missing = uniqueStrings(missing)
	appState, userState := "verified", "verified"
	if len(missing) > 0 {
		appState, userState = "missing", "missing"
	}
	if !report.Permissions.Identities.User.Ready || !report.Permissions.Verified {
		userState = "missing"
	}
	return domain.FeishuPermissionOverview{Application: appState, User: userState, Missing: feishuMissingCapabilities(missing)}
}

func feishuMissingCapabilities(scopes []string) []string {
	groups := map[string]string{"doc": "文档与知识库", "docs": "文档与知识库", "wiki": "文档与知识库", "calendar": "日历", "task": "任务", "contact": "人员与群组", "im": "沟通与任务", "sheets": "表格", "base": "多维表格", "vc": "会议", "minutes": "会议纪要"}
	result := []string{}
	for _, scope := range scopes {
		if name, ok := groups[strings.Split(scope, ":")[0]]; ok {
			result = append(result, name)
		}
	}
	return uniqueStrings(result)
}

func feishuServiceHealth(snapshot domain.FeishuSnapshot) string {
	if snapshot.ProcessRunning && snapshot.Availability == "ready" {
		return "running"
	}
	return snapshot.Availability
}
func feishuInboundHealth(profile string, snapshot domain.FeishuSnapshot) string {
	if profile == managedfeishu.ProfileManualOnly {
		return "manual_only"
	}
	if snapshot.InboundConnection {
		return "connected"
	}
	return "unavailable"
}
func feishuOverviewSummary(snapshot domain.FeishuSnapshot) string {
	if snapshot.Availability == "ready" {
		return "单聊收发、卡片回调与 Codex 任务控制可用"
	}
	if snapshot.Message != "" {
		return snapshot.Message
	}
	return "飞书服务需要处理"
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
			seen[value] = true
		}
	}
	sort.Strings(result)
	return result
}

func (service *Service) SetFeishuProfile(ctx context.Context, profile string) (map[string]any, error) {
	if service.managedFeishuSupervisor == nil {
		return nil, errors.New("KSFAssistant Feishu is unavailable")
	}
	var result map[string]any
	err := service.managedFeishuSupervisor.Call(ctx, "bridge/profile/set", map[string]any{"profile": profile}, &result)
	if err == nil {
		err = service.restartFeishuSupervisor(ctx)
	}
	service.clearFeishuCache()
	return result, err
}

func (service *Service) ControlFeishuService(ctx context.Context, action string) (map[string]any, error) {
	var err error
	if action == "start" {
		err = service.startFeishuSupervisor()
	} else if action == "restart" {
		err = service.restartFeishuSupervisor(ctx)
	} else {
		err = errors.New("不支持的飞书服务动作")
	}
	service.clearFeishuCache()
	return service.feishuSupervisorStatus(), err
}

func (service *Service) clearFeishuCache() {
	service.mu.Lock()
	service.lastFeishuAt = time.Time{}
	service.mu.Unlock()
}

func (service *Service) TokenHistory(_ context.Context, request TokenHistoryRequest) ([]domain.DailyUsageBucket, error) {
	dayCount := normalizedHistoryDayCount(request.DayCount)
	now := time.Now()
	service.mu.Lock()
	if service.lastTokenHistoryDays == dayCount && (request.RepriceOnly || now.Sub(service.lastTokenHistoryAt) < 10*time.Second) && len(service.lastTokenHistory) > 0 {
		cached := append([]domain.DailyUsageBucket(nil), service.lastTokenHistory...)
		service.mu.Unlock()
		return cached, nil
	}
	service.mu.Unlock()

	values, err := (tokens.Reader{Roots: tokens.DefaultRoots(service.home)}).ReadHistory(now, dayCount)
	if err != nil {
		return nil, fmt.Errorf("本机 Token 历史暂不可用：%w", err)
	}
	service.mu.Lock()
	service.lastTokenHistoryAt = now
	service.lastTokenHistoryDays = dayCount
	service.lastTokenHistory = append([]domain.DailyUsageBucket(nil), values...)
	service.mu.Unlock()
	return values, nil
}

func (service *Service) TokenHistoryComparison(ctx context.Context, request TokenHistoryRequest) (domain.TokenHistoryComparison, error) {
	dayCount := normalizedHistoryDayCount(request.DayCount)
	localDays, err := service.TokenHistory(ctx, TokenHistoryRequest{DayCount: dayCount, RepriceOnly: request.RepriceOnly})
	if err != nil {
		return domain.TokenHistoryComparison{}, err
	}

	serverDays, serverErr := service.readServerTokenHistory(ctx, request.RepriceOnly)
	days, plan, summary, fallback := applyPricingToHistory(compareTokenHistory(localDays, serverDays), request.PricingSelection)
	result := domain.TokenHistoryComparison{Days: days, SelectedPlan: plan, LocalCostSummary: summary, PricingFallback: fallback}
	if serverErr != nil {
		result.ServerError = "服务器 Token 历史刷新失败，正在显示本机数据。"
		if len(serverDays) > 0 {
			result.ServerError = "服务器 Token 历史刷新失败，正在显示上次同步数据。"
		}
	}
	return result, nil
}

func applyPricingToHistory(days []domain.TokenHistoryComparisonDay, selection domain.PricingSelection) ([]domain.TokenHistoryComparisonDay, domain.PricingPlan, domain.TokenCostEstimate, bool) {
	plan, fallback := pricing.Resolve(selection)
	result := append([]domain.TokenHistoryComparisonDay(nil), days...)
	for index := range result {
		estimate := pricing.Estimate(plan, result[index].LocalTokens, result[index].LocalBreakdown)
		result[index].LocalCost = &estimate
	}
	return result, plan, pricing.Aggregate(plan, result), fallback
}

func (service *Service) readServerTokenHistory(ctx context.Context, cachedOnly bool) ([]domain.DailyUsageBucket, error) {
	now := time.Now()
	service.mu.Lock()
	if !service.lastServerHistoryAt.IsZero() && now.Sub(service.lastServerHistoryAt) < 30*time.Second {
		cached := append([]domain.DailyUsageBucket(nil), service.lastServerHistory...)
		service.mu.Unlock()
		return cached, nil
	}
	cached := append([]domain.DailyUsageBucket(nil), service.lastServerHistory...)
	if len(cached) == 0 {
		cached = append(cached, service.lastUsage.DailyUsageBuckets...)
	}
	service.mu.Unlock()
	if cachedOnly && len(cached) > 0 {
		return cached, nil
	}

	if service.codex == nil {
		return cached, errors.New("Codex App Server is unavailable")
	}
	response, err := service.codex.FetchTokenUsage(ctx)
	if err != nil {
		return cached, err
	}
	values := normalizedDays(response.DailyUsageBuckets, 90)
	service.mu.Lock()
	service.lastServerHistoryAt = now
	service.lastServerHistory = append([]domain.DailyUsageBucket(nil), values...)
	service.lastUsage.TokenSummary = &response.Summary
	service.lastUsage.DailyUsageBuckets = append([]domain.DailyUsageBucket(nil), values...)
	service.lastUsage.TokenUpdatedAt = &now
	service.lastUsage.TokenError = ""
	service.mu.Unlock()
	return values, nil
}

func (service *Service) PrepareProjectLaunch(ctx context.Context, request PrepareLaunchRequest) (domain.ProjectLaunchAction, error) {
	root, err := canonicalDirectory(request.KSFRoot)
	if err != nil {
		return domain.ProjectLaunchAction{}, errors.New("KSF 根目录无效，无法启动项目")
	}
	catalog, err := service.ksf.Catalog(ctx, root)
	if err != nil {
		return domain.ProjectLaunchAction{}, err
	}
	project, ok := projectByID(catalog.Projects, request.ProjectID)
	if !ok {
		return domain.ProjectLaunchAction{}, errors.New("项目不存在或尚未同步")
	}
	action, ok := resolveProjectLaunchAction(project)
	if !ok {
		return domain.ProjectLaunchAction{}, errors.New(projectLaunchUnavailableMessage())
	}
	return action, nil
}

func (service *Service) Close() {
	service.approvals().broker.Close()
	if service.localGateway != nil {
		_ = service.localGateway.Close()
	}
	if service.integrationRuntime != nil {
		service.integrationRuntime.Close()
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_ = service.stopFeishuSupervisor(stopContext)
	if service.codex != nil {
		_ = service.codex.Close()
	}
	if service.desktop != nil {
		service.desktop.Close()
	}
}

func (service *Service) readCodex(ctx context.Context, now time.Time, active bool) (domain.UsageSnapshot, []domain.CodexThread) {
	service.mu.Lock()
	usage := service.lastUsage
	threads := service.lastThreads
	needRate := service.lastRateAttempt.IsZero() || now.Sub(service.lastRateAttempt) >= rateRefreshInterval
	needToken := service.lastTokenAttempt.IsZero() || now.Sub(service.lastTokenAttempt) >= tokenRefreshInterval
	localInterval := idleLocalTokenInterval
	if active {
		localInterval = activeLocalTokenInterval
	}
	needLocal := service.lastLocalTokenAttempt.IsZero() || now.Sub(service.lastLocalTokenAttempt) >= localInterval
	needThreads := service.lastThreadAttempt.IsZero() || now.Sub(service.lastThreadAttempt) >= threadRefreshInterval
	if needRate {
		service.lastRateAttempt = now
	}
	if needToken {
		service.lastTokenAttempt = now
	}
	if needLocal {
		service.lastLocalTokenAttempt = now
	}
	if needThreads {
		service.lastThreadAttempt = now
	}
	service.mu.Unlock()
	if service.codex == nil {
		usage.Status = "codexMissing"
		usage.RateError = "未找到 Codex。"
		if needLocal {
			service.mergeLocalTokens(&usage, now)
		}
		service.mu.Lock()
		service.lastUsage = usage
		service.mu.Unlock()
		return usage, nil
	}
	var rateResponse domain.RateLimitsResponse
	var tokenResponse domain.TokenUsageResponse
	var rateErr, tokenErr, threadErr error
	var wait sync.WaitGroup
	if needRate {
		wait.Add(1)
		go func() { defer wait.Done(); rateResponse, rateErr = service.codex.FetchRateLimits(ctx) }()
	}
	if needToken {
		wait.Add(1)
		go func() { defer wait.Done(); tokenResponse, tokenErr = service.codex.FetchTokenUsage(ctx) }()
	}
	if needThreads {
		wait.Add(1)
		go func() { defer wait.Done(); threads, threadErr = service.codex.FetchThreads(ctx) }()
	}
	wait.Wait()
	if needRate && rateErr == nil {
		usage.Buckets = domain.NormalizeRateLimits(rateResponse)
		usage.RateUpdatedAt = &now
		usage.RateError = ""
		usage.Status = "available"
		if general := domain.GeneralBucket(usage.Buckets); general == nil || domain.HeadlineRemaining(*general) == nil {
			usage.Status = "unsupportedProtocol"
		}
	} else if needRate {
		usage.RateError = recoveryMessage(rateErr)
		if domain.GeneralBucket(usage.Buckets) != nil {
			usage.Status = "stale"
		} else {
			usage.Status = "offline"
		}
	}
	if needToken && tokenErr == nil {
		usage.TokenSummary = &tokenResponse.Summary
		usage.DailyUsageBuckets = normalizedDays(tokenResponse.DailyUsageBuckets, 14)
		usage.TokenUpdatedAt = &now
		usage.TokenError = ""
	} else if needToken {
		usage.TokenError = "Token 活动暂不可用，额度信息不受影响。"
	}
	if needLocal {
		service.mergeLocalTokens(&usage, now)
	}
	service.mu.Lock()
	service.lastUsage = usage
	if needThreads && threadErr == nil {
		service.lastThreads = threads
	} else if threadErr != nil {
		threads = service.lastThreads
	}
	service.mu.Unlock()
	return usage, threads
}

func (service *Service) mergeLocalTokens(usage *domain.UsageSnapshot, now time.Time) {
	reader := tokens.Reader{Roots: tokens.DefaultRoots(service.home)}
	if values, err := reader.ReadHistory(now, 2); err == nil && len(values) == 2 {
		usage.LocalPreviousDailyUsage = &values[0]
		usage.LocalDailyUsage = &values[1]
		usage.LocalTokenUpdatedAt = &now
	}
}

func (service *Service) readFeishu(ctx context.Context, now time.Time) domain.FeishuSnapshot {
	if service.managedFeishuSupervisor == nil {
		return normalizedFeishuSnapshot(domain.FeishuSnapshot{Availability: "unavailable", Message: "KSFAssistant Feishu is unavailable", RuntimeKind: "go", ProcessState: managedfeishu.StateDegraded})
	}
	service.mu.Lock()
	cached := service.lastFeishu
	refresh := cached.Revision == 0 || service.lastFeishuAt.IsZero()
	service.mu.Unlock()
	if refresh {
		snapshot, err := service.fetchFeishuSnapshot(ctx)
		if err != nil {
			cached = domain.FeishuSnapshot{Availability: "unavailable", Message: err.Error(), RuntimeKind: "go"}
		} else {
			cached = snapshot
		}
	}
	service.applyFeishuSupervisorStatus(&cached)
	return service.composeIntegrationSnapshot(cached)
}

func (service *Service) mergeFeishuLink(link domain.FeishuTaskLink) {
	service.mu.Lock()
	defer service.mu.Unlock()
	links := service.lastFeishu.Links
	replaced := false
	for index := range links {
		if links[index].TaskKey == link.TaskKey {
			links[index] = link
			replaced = true
			break
		}
	}
	if !replaced {
		links = append(links, link)
	}
	service.lastFeishu.Links = links
	service.lastFeishuAt = time.Now()
}

// JSON clients treat these collections as part of a stable array contract.
// A nil Go slice would otherwise be encoded as null and make strict desktop
// decoders reject the entire dashboard even though usage and project data are
// otherwise available.
func normalizedFeishuSnapshot(snapshot domain.FeishuSnapshot) domain.FeishuSnapshot {
	if snapshot.TargetAliases == nil {
		snapshot.TargetAliases = []string{}
	}
	if snapshot.ReadinessBlockers == nil {
		snapshot.ReadinessBlockers = []string{}
	}
	if snapshot.Links == nil {
		snapshot.Links = []domain.FeishuTaskLink{}
	}
	return snapshot
}

func (service *Service) startFeishuSupervisor() error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu is unavailable")
	}
	if err := service.managedFeishuSupervisor.Start(); err != nil {
		return err
	}
	return nil
}

func (service *Service) restartFeishuSupervisor(ctx context.Context) error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu is unavailable")
	}
	if err := service.managedFeishuSupervisor.Restart(ctx); err != nil {
		return err
	}
	return nil
}

func (service *Service) initializeManagedBridge(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var initialized feishuprotocol.InitializeResult
	request := service.bridgeInitializeRequest()
	if err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodBridgeInitialize, request, &initialized); err != nil {
		return err
	}
	if initialized.Protocol != feishuprotocol.Protocol {
		return errors.New("KSFAssistant Feishu private IPC protocol mismatch")
	}
	return nil
}

func (service *Service) stopFeishuSupervisor(ctx context.Context) error {
	if service.managedFeishuSupervisor == nil {
		return nil
	}
	return service.managedFeishuSupervisor.Stop(ctx)
}

func (service *Service) feishuSupervisorStatus() map[string]any {
	if service.managedFeishuSupervisor == nil {
		return map[string]any{"state": managedfeishu.StateDegraded, "configured": false, "pid": 0, "restartCount": 0, "lastError": "KSFAssistant Feishu is unavailable"}
	}
	status := service.managedFeishuSupervisor.Status()
	return map[string]any{
		"state": status.State, "configured": status.Configured, "pid": status.PID,
		"restartCount": status.RestartCount, "lastError": status.LastError,
		"lastDiagnosticAt": status.LastDiagnosticAt, "lastDiagnosticCode": status.LastDiagnosticCode,
		"lastDiagnosticSummary": status.LastDiagnosticSummary,
	}
}

func (service *Service) applyFeishuSupervisorStatus(snapshot *domain.FeishuSnapshot) {
	status := service.feishuSupervisorStatus()
	snapshot.ProcessState, _ = status["state"].(string)
	snapshot.Configured, _ = status["configured"].(bool)
	snapshot.ProcessPID, _ = status["pid"].(int)
	snapshot.RestartCount, _ = status["restartCount"].(int)
	snapshot.LastError, _ = status["lastError"].(string)
	snapshot.LastDiagnosticAt, _ = status["lastDiagnosticAt"].(string)
	snapshot.LastDiagnosticCode, _ = status["lastDiagnosticCode"].(string)
	snapshot.LastDiagnosticSummary, _ = status["lastDiagnosticSummary"].(string)
	snapshot.ProcessRunning = snapshot.ProcessState == managedfeishu.StateRunning || snapshot.ProcessState == managedfeishu.StateIdleUnconfigured || snapshot.ProcessState == managedfeishu.StateStarting
}

func (service *Service) readProjects(ctx context.Context, request DashboardRequest, threads []domain.CodexThread, observations []domain.TaskObservation, now time.Time) domain.ProjectDashboardSnapshot {
	result := domain.ProjectDashboardSnapshot{Availability: "unavailable", Projects: []domain.ProjectDashboardItem{}, Catalog: []domain.Project{}, ObservedAt: now}
	if strings.TrimSpace(request.KSFRoot) == "" {
		result.Message = "请在设置中选择 KSF 根目录。"
		return result
	}
	root, err := canonicalDirectory(request.KSFRoot)
	if err != nil {
		result.Message = "KSF 目录不存在，请在设置中重新选择。"
		return result
	}
	stamp := threadSetStamp(threads)
	service.mu.Lock()
	source, cached := service.projectSources[root]
	service.mu.Unlock()
	if !cached || now.Sub(source.refreshedAt) >= projectRefreshInterval || source.threadStamp != stamp {
		enabledAt, enableErr := service.ksf.Enable(ctx, root)
		catalog, catalogErr := service.ksf.Catalog(ctx, root)
		if catalogErr != nil {
			if !cached {
				result.Message = catalogErr.Error()
				return result
			}
			source.message = "项目目录刷新失败，正在显示上次成功数据。"
		} else {
			ids := make([]string, 0, len(threads))
			for _, thread := range threads {
				ids = append(ids, thread.ID)
			}
			projections, projectionErr := service.ksf.Projections(ctx, root, ids)
			if projectionErr != nil {
				projections = map[string]domain.TaskProjection{}
			}
			trackingAt := enabledAt
			if enableErr != nil || trackingAt.IsZero() {
				service.mu.Lock()
				trackingAt = service.trackingAt[root]
				if trackingAt.IsZero() {
					trackingAt = now
					service.trackingAt[root] = trackingAt
				}
				service.mu.Unlock()
			}
			projectIDs := make([]string, 0, len(catalog.Projects))
			for _, project := range catalog.Projects {
				projectIDs = append(projectIDs, project.ID)
			}
			source = projectSourceCache{
				catalog:       catalog.Projects,
				projections:   projections,
				usage:         tokens.ReadProjectUsage(projectIDs, threads, projections, trackingAt, now),
				launchActions: resolveProjectLaunchActions(catalog.Projects),
				threadStamp:   stamp,
				refreshedAt:   now,
			}
			if projectionErr != nil {
				source.message = "项目任务投影暂不可用；目录信息仍可使用。"
			}
			service.mu.Lock()
			service.projectSources[root] = source
			service.mu.Unlock()
		}
	}
	service.desktop.ReconcileCandidates(localTaskCandidates(source.catalog, threads, source.projections))
	pinned := map[string]bool{}
	for _, id := range request.PinnedProjectIDs {
		if id != "" {
			pinned[id] = true
		}
	}
	result.Availability = "available"
	result.Catalog = source.catalog
	result.Projects = domain.BuildProjectDashboard(source.catalog, threads, source.projections, observations, pinned, source.usage, source.launchActions, now)
	result.Message = source.message
	return result
}

func threadSetStamp(threads []domain.CodexThread) string {
	ids := make([]string, 0, len(threads))
	for _, thread := range threads {
		ids = append(ids, thread.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x00")
}

func taskBootstrap(project domain.Project, ksfRoot, purpose string) (string, string, error) {
	if strings.TrimSpace(project.Name) == "" || hasControl(project.Name) {
		return "", "", fmt.Errorf("项目名称无效，无法准备新任务")
	}
	root, err := canonicalDirectory(ksfRoot)
	if err != nil {
		return "", "", fmt.Errorf("项目路径无效，无法准备新任务")
	}
	card, err := canonicalFile(project.CardPath)
	if err != nil || hasControl(card) || !pathInside(card, root) {
		return "", "", fmt.Errorf("项目路径无效，无法准备新任务")
	}
	suffix := " · 新任务"
	prompt := fmt.Sprintf("这是 KSF 项目「%s」的新任务。\n项目记忆卡：%s\n\n请按 KSF 规范加载该项目的基础上下文。本轮只做上下文准备：不要开始具体工作，不要修改文件，不要生成实施方案。完成后简短说明已就绪，并等待用户下一步指令。", project.Name, card)
	if purpose == "archiveProject" {
		suffix = " · 归档项目"
		prompt = fmt.Sprintf("这是 KSF 项目「%s」的归档任务。\n项目记忆卡：%s\n\n请按 KSF 规范加载项目上下文并归档当前项目。先核对归档前置条件、未完成事项和需要保留的项目事实；若存在必须由用户确认的判断或授权，先说明并等待确认。无阻塞时完成归档，并报告归档位置、保留内容和结果。不要绕过 KSF 的移动、归档与长期写入边界。", project.Name, card)
	}
	runes := []rune(project.Name)
	available := 64 - len([]rune(suffix))
	if len(runes) > available {
		runes = runes[:available]
	}
	return string(runes) + suffix, prompt, nil
}

func projectByID(projects []domain.Project, id string) (domain.Project, bool) {
	if strings.TrimSpace(id) == "" || hasControl(id) {
		return domain.Project{}, false
	}
	for _, project := range projects {
		if project.ID == id {
			return project, true
		}
	}
	return domain.Project{}, false
}

func normalizedHistoryDayCount(value int) int {
	if value <= 0 {
		return 30
	}
	if value > 90 {
		return 90
	}
	return value
}

func canonicalDirectory(value string) (string, error) {
	if strings.TrimSpace(value) == "" || hasControl(value) {
		return "", errors.New("invalid directory")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("not a directory")
	}
	return filepath.Clean(resolved), nil
}

func canonicalFile(value string) (string, error) {
	if strings.TrimSpace(value) == "" || hasControl(value) {
		return "", errors.New("invalid file")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	return filepath.Clean(resolved), nil
}

func pathInside(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func normalizedDays(values []domain.DailyUsageBucket, count int) []domain.DailyUsageBucket {
	values = append([]domain.DailyUsageBucket(nil), values...)
	sort.SliceStable(values, func(left, right int) bool {
		return values[left].StartDate < values[right].StartDate
	})
	if len(values) > count {
		values = values[len(values)-count:]
	}
	return values
}

func compareTokenHistory(localDays, serverDays []domain.DailyUsageBucket) []domain.TokenHistoryComparisonDay {
	serverByDate := make(map[string]int64, len(serverDays))
	for _, day := range serverDays {
		if day.StartDate != "" {
			serverByDate[day.StartDate] = day.Tokens
		}
	}
	result := make([]domain.TokenHistoryComparisonDay, 0, len(localDays))
	for _, local := range localDays {
		day := domain.TokenHistoryComparisonDay{
			StartDate:      local.StartDate,
			LocalTokens:    local.Tokens,
			LocalBreakdown: local.Breakdown,
		}
		if value, ok := serverByDate[local.StartDate]; ok {
			serverTokens := value
			day.ServerTokens = &serverTokens
		}
		result = append(result, day)
	}
	return result
}

func recoveryMessage(err error) string {
	value := strings.ToLower(err.Error())
	if strings.Contains(value, "authentication") || strings.Contains(value, "sign in") || strings.Contains(value, "login") {
		return "请先在 Codex 中使用 ChatGPT 登录，然后刷新。"
	}
	return "请检查 Codex 与网络，应用会自动重试。"
}

func hasControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func localTaskCandidates(projects []domain.Project, threads []domain.CodexThread, projections map[string]domain.TaskProjection) []string {
	known := map[string]bool{}
	roots := map[string]bool{}
	for _, project := range projects {
		known[project.ID] = true
		for _, mapping := range project.EngineeringMappings {
			roots[strings.ToLower(filepath.Clean(mapping.RootPath))] = true
		}
	}
	result := []string{}
	for _, thread := range threads {
		if thread.ParentThreadID != nil || (thread.AgentNickname != nil && *thread.AgentNickname != "") || thread.Path == nil {
			continue
		}
		matched := roots[strings.ToLower(filepath.Clean(thread.CWD))]
		if projection, ok := projections[thread.ID]; ok {
			if binding := projection.CurrentBinding(); binding != nil && known[binding.ProjectCard] {
				matched = true
			}
		}
		if matched {
			result = append(result, thread.ID)
		}
	}
	return result
}
