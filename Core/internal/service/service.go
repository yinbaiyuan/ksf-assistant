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
	"ksfassistant/core/internal/pricing"
	"ksfassistant/core/internal/productversion"
	"ksfassistant/core/internal/tokens"
)

const Version = productversion.Version

const (
	rateRefreshInterval       = 5 * time.Minute
	activeLocalTokenInterval  = 10 * time.Second
	idleLocalTokenInterval    = 30 * time.Minute
	threadRefreshInterval     = 10 * time.Second
	activityDiscoveryInterval = 3 * time.Second
	projectRefreshInterval    = 15 * time.Second
	feishuRefreshFloor        = 2500 * time.Millisecond
)

type DashboardRequest struct {
	KSFRoot             string                  `json:"ksfRoot"`
	PinnedProjectIDs    []string                `json:"pinnedProjectIds"`
	PinnedWorkspaceIDs  []string                `json:"pinnedWorkspaceIds"`
	PricingSelection    domain.PricingSelection `json:"pricingSelection,omitempty"`
	ForceAccountRefresh bool                    `json:"forceAccountRefresh,omitempty"`
}

type InitializeRequest struct {
	Integrations IntegrationContextRequest `json:"integrations"`
}

type IntegrationContextRequest struct {
	KSFRoot string `json:"ksfRoot"`
}

type FeishuFeatureUpdateRequest struct {
	ExpectedSettings *managedfeishu.Settings `json:"-"`
	Feature          string                  `json:"feature"`
	Mode             string                  `json:"mode"`
	ConfirmRealWrite bool                    `json:"confirmRealWrite"`
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

type CreateWorkspaceTaskRequest struct {
	WorkspaceID string `json:"workspaceId"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	KSFRoot     string `json:"ksfRoot,omitempty"`
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

type preparedTask struct {
	cwd, prompt string
	expiresAt   time.Time
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
	configuration           configurationRuntime
	integrationRuntime      *integration.Runtime
	feishuGeneration        uint64
	home                    string
	codex                   *codex.Client
	ksf                     bridge.KSFClient
	desktop                 *desktop.ActivityClient
	mu                      sync.Mutex
	accountReadMu           sync.Mutex
	lastUsage               domain.UsageSnapshot
	lastThreads             []domain.CodexThread
	lastActivityThreads     []domain.CodexThread
	lastCodexProjects       []domain.CodexProject
	lastRateAttempt         time.Time
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
	preparedTasks           map[string]preparedTask
	workspaceUsage          workspaceUsageCache
	activityDiscoveryCancel context.CancelFunc
	activityDiscoveryDone   chan struct{}
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

type workspaceUsageCache struct {
	values      map[string]domain.ProjectUsageSummary
	stamp       string
	refreshedAt time.Time
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
	// Reconcile durable configuration flow receipts before any supervised
	// Feishu process can accept new work. This also upgrades records produced by
	// older builds even when the user has not opened the Feishu settings page.
	service.persistLegacyConfigurationFlowOutcomes()
	executable, err := configureCodexProcessEnvironment(service.home)
	if err == nil {
		service.codex = &codex.Client{Executable: executable, Timeout: 15 * time.Second}
		_ = service.codex.Start(ctx)
	}
	_ = service.desktop.Start(ctx)
	service.startActivityDiscovery()
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
			"taskCreation": true, "workspaceTaskCreation": true, "projectLaunch": true, "feishuTaskLinks": true, "feishuServiceManagement": true, "feishuCapabilityGovernance": true, "userWriteApproval": true,
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
	activeHint := activity.RunningCount > 0 || activity.WaitingCount > 0
	usage, threads, codexProjects := service.readCodexState(ctx, now, activeHint, request.ForceAccountRefresh)
	service.mu.Lock()
	activityThreads := append([]domain.CodexThread(nil), service.lastActivityThreads...)
	service.mu.Unlock()
	threads = mergeCodexThreads(activityThreads, threads)
	// Account and token reads can be slow. Do not return activity captured
	// before those reads; a turn may have started or completed meanwhile.
	now = time.Now()
	activity = service.desktop.Snapshot(now)
	desktopActivity := activity
	threads = service.applyThreadLaunchScopes(threads)
	plan, _ := pricing.Resolve(request.PricingSelection)
	if usage.LocalDailyUsage != nil {
		estimate := pricing.Estimate(plan, usage.LocalDailyUsage.Tokens, usage.LocalDailyUsage.Breakdown)
		usage.LocalDailyCost = &estimate
	}
	observations := activity.Observations
	if service.codex != nil {
		observations = mergeActivityObservations(observations, codex.Observations(threads))
	}
	activity = domain.SummarizeActivity(observations, now)
	if desktopActivity.Availability != "available" && service.codex == nil {
		activity.Availability = desktopActivity.Availability
	}
	feishu := normalizedFeishuSnapshot(domain.FeishuSnapshot{Availability: "notConfigured"})
	if service.hasFeishuRuntime() {
		feishu = service.readFeishu(ctx, now)
	}
	connected := map[string]bool{}
	for _, link := range feishu.Links {
		if link.LinkState == "active" {
			connected[link.TaskKey] = true
		}
	}
	projects := service.readProjects(ctx, request, threads, observations, now, connected)
	service.enrichTaskRuntime(ctx, request.KSFRoot, &projects, desktopActivity, now)
	workspaces := domain.BuildCodexWorkspaceDashboard(runtime.GOOS, request.KSFRoot, threads, codexProjects, observations, projects, request.PinnedWorkspaceIDs, now, connected)
	workspaceUsage := service.readWorkspaceUsage(workspaces.Workspaces, threads, projects, now)
	for index := range workspaces.Workspaces {
		if usage, ok := workspaceUsage[workspaces.Workspaces[index].ID]; ok {
			workspaces.Workspaces[index].Usage = &usage
		}
	}
	projects = domain.RemoveWorkspaceTasksFromUnassignedProjects(projects, workspaces)
	return domain.DashboardSnapshot{Protocol: domain.Protocol, CoreVersion: Version, Platform: runtime.GOOS, ObservedAt: now, Usage: usage, Activity: activity, Projects: projects, Workspaces: workspaces, Feishu: feishu}
}

func (service *Service) applyThreadLaunchScopes(threads []domain.CodexThread) []domain.CodexThread {
	if service.integrationRuntime == nil || len(threads) == 0 {
		return threads
	}
	file, err := service.integrationRuntime.Store().Load()
	if err != nil {
		return threads
	}
	scopes := map[string]string{}
	for _, link := range file.Links {
		if scope := strings.TrimSpace(link.ExtraString("launchScope")); scope != "" {
			scopes[link.ThreadID] = scope
		}
	}
	if len(scopes) == 0 {
		return threads
	}
	result := append([]domain.CodexThread(nil), threads...)
	for index := range result {
		result[index].LaunchScope = scopes[result[index].ID]
	}
	return result
}

func (service *Service) readWorkspaceUsage(workspaces []domain.CodexWorkspaceItem, threads []domain.CodexThread, projects domain.ProjectDashboardSnapshot, now time.Time) map[string]domain.ProjectUsageSummary {
	stampParts := make([]string, 0, len(workspaces)+len(threads))
	for _, workspace := range workspaces {
		stampParts = append(stampParts, workspace.ID)
	}
	for _, project := range projects.Projects {
		if project.Kind != "project" || project.Project == nil {
			continue
		}
		for _, task := range project.Tasks {
			stampParts = append(stampParts, "ksf:"+task.ThreadID)
		}
	}
	stampParts = append(stampParts, "threads:"+threadSetStamp(threads))
	sort.Strings(stampParts)
	stamp := strings.Join(stampParts, "\x00")

	service.mu.Lock()
	cached := service.workspaceUsage
	service.mu.Unlock()
	if cached.values != nil && cached.stamp == stamp && now.Sub(cached.refreshedAt) < projectRefreshInterval {
		return cached.values
	}
	values := tokens.ReadWorkspaceUsage(runtime.GOOS, workspaces, threads, projects, now)
	service.mu.Lock()
	service.workspaceUsage = workspaceUsageCache{values: values, stamp: stamp, refreshedAt: now}
	service.mu.Unlock()
	return values
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
	service.mu.Lock()
	if service.preparedTasks == nil {
		service.preparedTasks = map[string]preparedTask{}
	}
	for id, task := range service.preparedTasks {
		if !time.Now().Before(task.expiresAt) {
			delete(service.preparedTasks, id)
		}
	}
	service.preparedTasks[threadID] = preparedTask{cwd: root, prompt: prompt, expiresAt: time.Now().Add(10 * time.Minute)}
	service.mu.Unlock()
	return map[string]string{"threadId": threadID, "name": name, "prompt": prompt, "submission": "desktop-prepared-context"}, nil
}

func (service *Service) CreateWorkspaceTask(ctx context.Context, request CreateWorkspaceTaskRequest) (map[string]string, error) {
	if service.codex == nil {
		return nil, errors.New("未找到 Codex，无法新建任务")
	}
	if request.WorkspaceID != domain.CodexWorkspaceID(runtime.GOOS, request.Path) {
		return nil, errors.New("工作区标识与目录不匹配")
	}
	workspace, err := canonicalDirectory(request.Path)
	if err != nil {
		return nil, errors.New("工作区目录不存在，无法新建任务")
	}
	if strings.TrimSpace(request.KSFRoot) != "" {
		if root, rootErr := canonicalDirectory(request.KSFRoot); rootErr == nil && pathInside(workspace, root) {
			return nil, errors.New("KSF 路径不能作为普通 Codex 工作区新建任务")
		}
	}
	name, err := workspaceTaskName(request.Name)
	if err != nil {
		return nil, err
	}
	threadID, err := service.codex.CreateDraftThread(ctx, workspace, name)
	if err != nil {
		return nil, err
	}
	return map[string]string{"threadId": threadID, "name": name, "prompt": "", "submission": "desktop-draft"}, nil
}

func (service *Service) SubmitTask(ctx context.Context, request SubmitTaskRequest) error {
	if err := service.consumePreparedTask(request); err != nil {
		return err
	}
	return service.desktop.StartPreparedTurn(ctx, request.ThreadID, request.HostID, request.CWD, request.Prompt)
}

func (service *Service) consumePreparedTask(request SubmitTaskRequest) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	task, ok := service.preparedTasks[request.ThreadID]
	if !ok || !time.Now().Before(task.expiresAt) {
		return errors.New("任务启动状态已失效或已提交，请在 Codex 中查看并继续")
	}
	if (request.HostID != "" && request.HostID != "local") || request.CWD != task.cwd || request.Prompt != task.prompt {
		return errors.New("任务启动参数与已准备的任务不一致")
	}
	// Reserve the one allowed dispatch before crossing IPC. A lost reply must
	// never cause a second turn, including after a repeated host request.
	delete(service.preparedTasks, request.ThreadID)
	return nil
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
	return errors.New("configuration_confirmation_required")
}
func (service *Service) sendConfigurationTest(ctx context.Context, target, requestID string) error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("test_not_submitted_transport_unavailable")
	}
	var result feishuprotocol.MessageResult
	err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodMessageTest, map[string]any{"targetAlias": target, "requestId": requestID, "confirm": true}, &result)
	if err != nil || result.MessageID == "" {
		var receipt struct {
			Outcome   string `json:"outcome"`
			MessageID string `json:"messageId"`
		}
		if queryErr := service.managedFeishuSupervisor.Call(ctx, "bridge/message/test/result", map[string]any{"requestId": requestID}, &receipt); queryErr == nil {
			if receipt.Outcome == "completed" && receipt.MessageID != "" {
				return nil
			}
			if receipt.Outcome == "failed" {
				return errors.New("test_definitive_failure")
			}
		}
		if err == nil {
			return errors.New("test_receipt_missing")
		}
	}
	return err
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
	if _, err := service.FeishuSettings(); err != nil {
		return nil, err
	}
	return map[string]any{"status": "ok", "eventConsumer": feishuprotocol.ManagedEventConsumerStatus()}, nil
}

func (service *Service) ConfigureFeishu(ctx context.Context, appID, appSecret string) error {
	if service.managedFeishuSupervisor == nil {
		return errors.New("KSFAssistant Feishu is unavailable")
	}
	evidence, err := service.readConfigurationEvidence(ctx)
	if err != nil || evidence.ApplicationState != "missing" {
		return errors.New("官方 CLI 已有配置或配置状态未确认，请检查并沿用当前应用；不会覆盖配置")
	}
	var result map[string]any
	return service.managedFeishuSupervisor.Call(ctx, "bridge/auth/configure", map[string]any{"appId": appID, "appSecret": appSecret, "brand": "feishu", "profile": "default"}, &result)
}

func (service *Service) FeishuSettings() (managedfeishu.Settings, error) {
	return (remoteSettingsStore{service}).Load()
}

func (service *Service) UpdateFeishuSettings(settings managedfeishu.Settings) (managedfeishu.Settings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := service.saveAndRestartFeishuSettings(ctx, settings); err != nil {
		return managedfeishu.Settings{}, err
	}
	return (remoteSettingsStore{service}).Load()
}

func (service *Service) FeishuSetup() (managedfeishu.SetupState, error) {
	return (remoteSetupStore{service}).Load()
}

func (service *Service) BeginFeishuSetup(ctx context.Context, mode, appID, appSecret string) (map[string]any, error) {
	state := managedfeishu.DefaultSetupState()
	state.Mode = mode
	var result map[string]any
	var err error
	restartAfterSave := false
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
		if err == nil {
			state.Stage, err = appConfigurationStage(result)
			restartAfterSave = state.Stage == managedfeishu.SetupAppConfigured
		}
	case "reuse":
		if service.managedFeishuSupervisor == nil {
			return nil, errors.New("KSFAssistant Feishu is unavailable")
		}
		state.Mode = managedfeishu.SetupModeExisting
		err = service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodAuthStart, map[string]any{"kind": "config", "profile": "default", "createNew": false}, &result)
		if err == nil && (result["status"] != "configured" || result["flow"] != "existing-config") {
			err = errors.New("官方 CLI 现有应用配置尚未验证，未变更配置向导")
		}
		state.Stage = managedfeishu.SetupAppConfigured
	default:
		return nil, errors.New("不支持的飞书配置方式")
	}
	store := remoteSetupStore{service}
	if err != nil {
		return nil, err
	}
	if err := store.Save(state); err != nil {
		return nil, err
	}
	if restartAfterSave {
		if err := service.restartFeishuSupervisor(ctx); err != nil {
			return nil, err
		}
		service.clearFeishuCache()
	}
	publicState := state
	if state.Stage == managedfeishu.SetupAppPending {
		publicState.VerificationURL, _ = result["verificationUrl"].(string)
		publicState.UserCode, _ = result["userCode"].(string)
	}
	result["setup"] = publicState
	return result, nil
}

func appConfigurationStage(result map[string]any) (string, error) {
	if result["flow"] == "app-create" {
		switch result["status"] {
		case "pending":
			return managedfeishu.SetupAppPending, nil
		case "completed":
			return managedfeishu.SetupAppConfigured, nil
		}
	}
	return "", errors.New("应用创建会话未返回有效结果，请重新检查；未启动用户授权")
}

func (service *Service) ContinueFeishuSetup(ctx context.Context) (map[string]any, error) {
	store := remoteSetupStore{service}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	var result map[string]any
	restartAfterSave := false
	switch state.Stage {
	case managedfeishu.SetupAppPending:
		state.VerificationURL = ""
		state.UserCode = ""
		state.ReadyToActivate = false
		if service.managedFeishuSupervisor == nil {
			err = errors.New("KSFAssistant Feishu is unavailable")
		} else {
			err = service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodAuthConfigFinish, map[string]any{}, &result)
		}
		if err == nil {
			var stage string
			stage, err = appConfigurationStage(result)
			if err == nil {
				state.Stage = stage
				restartAfterSave = stage == managedfeishu.SetupAppConfigured
			}
		}
	case managedfeishu.SetupAppConfigured:
		var evidence feishuprotocol.ConfigurationEvidence
		evidence, err = service.readConfigurationEvidence(ctx)
		if err != nil {
			break
		}
		if evidence.ApplicationState == "present" && evidence.OperatorState == "present" {
			state.Stage = managedfeishu.SetupPlatformPending
			result = map[string]any{"status": "connected"}
		} else if evidence.ApplicationState != "present" || evidence.OperatorState != "missing" {
			err = errors.New("当前应用或本人绑定状态尚未确认，请先检查；未自动发起授权")
		} else if result, err = service.StartFeishuAuth(ctx); err == nil {
			state.Stage = managedfeishu.SetupAuthorizationPending
		}
	case managedfeishu.SetupAuthorizationPending:
		var auth feishuprotocol.AuthStatus
		auth, err = service.FinishDesktopFeishuAuth(ctx)
		if err == nil && confirmedFeishuUserAuth(&auth) {
			state.Stage = managedfeishu.SetupPlatformPending
			result = map[string]any{"status": "authenticated"}
		} else if err == nil && auth.Status == "pending" {
			result = publicFeishuAuthResult(auth)
		} else if err == nil {
			err = errors.New("用户授权结果尚未确认，请重新检查")
		}
	case managedfeishu.SetupPlatformPending, managedfeishu.SetupFailed:
		return service.VerifyFeishuSetup(ctx)
	case managedfeishu.SetupReady:
		return map[string]any{"status": "ready", "setup": state}, nil
	default:
		return nil, errors.New("请先开始飞书配置")
	}
	state.VerificationURL = ""
	state.UserCode = ""
	if err != nil {
		state.LastError = safeSetupError(err)
		if state.Stage == managedfeishu.SetupAppPending {
			state.LastError = "应用创建尚未完成或会话已失效，请重新检查或重新开始；未启动用户授权"
		}
		_ = store.Save(state)
		return nil, err
	}
	state.LastError = ""
	if err := store.Save(state); err != nil {
		return nil, err
	}
	if restartAfterSave {
		if err := service.restartFeishuSupervisor(ctx); err != nil {
			return nil, err
		}
		service.clearFeishuCache()
	}
	publicState := state
	if state.Stage == managedfeishu.SetupAppPending || state.Stage == managedfeishu.SetupAuthorizationPending {
		publicState.VerificationURL, _ = result["verificationUrl"].(string)
		publicState.UserCode, _ = result["userCode"].(string)
	}
	result["setup"] = publicState
	return result, nil
}

func (service *Service) VerifyFeishuSetup(ctx context.Context) (map[string]any, error) {
	store := remoteSetupStore{service}
	state, loadErr := store.Load()
	if loadErr != nil {
		return nil, loadErr
	}
	if state.Stage == managedfeishu.SetupAppPending {
		return nil, errors.New("请先完成飞书网页中的应用创建或选择")
	}
	evidence, err := service.readConfigurationEvidence(ctx)
	if err != nil {
		return nil, err
	}
	permissions, err := service.FeishuPermissions(ctx)
	if err != nil {
		return nil, err
	}
	status := "incomplete"
	if evidence.ApplicationState == "present" && evidence.BotState == "present" && evidence.ApplicationPermissions == "present" && evidence.OperatorState == "present" && permissionsReady(permissions) {
		status = "verified"
	}
	return map[string]any{"status": status, "setup": state, "permissions": permissions["permissions"]}, nil
}

func (service *Service) ActivateFeishuSetup(ctx context.Context, _ string) (map[string]any, error) {
	return service.activateFeishuSetup(ctx, nil)
}

func (service *Service) ActivateFeishuSetupWithExpected(ctx context.Context, expected managedfeishu.Settings) (map[string]any, error) {
	return service.activateFeishuSetup(ctx, &expected)
}

func (service *Service) activateFeishuSetup(ctx context.Context, expected *managedfeishu.Settings) (map[string]any, error) {
	return nil, errors.New("configuration_action_retired")
}

func (service *Service) saveAndRestartFeishuSettings(ctx context.Context, settings managedfeishu.Settings) error {
	previous, err := (remoteSettingsStore{service}).Load()
	if err != nil {
		return err
	}
	return service.compareAndSwapAndRestartFeishuSettings(ctx, previous, settings)
}

func (service *Service) compareAndSwapAndRestartFeishuSettings(ctx context.Context, previous, settings managedfeishu.Settings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store := remoteSettingsStore{service}
	if err := store.CompareAndSwap(ctx, previous, settings); err != nil {
		if errors.Is(err, ErrFeishuSettingsConflict) {
			return err
		}
		return errors.New("设置保存结果未确认，请检查当前设置；不会自动重试")
	}
	service.clearFeishuCache()
	if err := service.restartFeishuSupervisor(ctx); err != nil {
		return errors.New("设置已保存，但服务重启结果未确认；请检查当前状态，不会自动还原设置")
	}
	return nil
}

func (service *Service) waitForFeishuAvailability(ctx context.Context, expected string, timeout time.Duration) (domain.FeishuSnapshot, error) {
	if service.managedFeishuSupervisor == nil {
		return domain.FeishuSnapshot{}, errors.New("KSFAssistant Feishu is unavailable")
	}
	return waitForSetupSnapshot(ctx, expected, timeout, service.fetchFeishuSnapshot)
}

func activateFeishuSettings(settings managedfeishu.Settings) managedfeishu.Settings {
	settings.Outbound.Enabled = true
	settings.Outbound.DryRun = false
	return settings
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
	var flow *feishuprotocol.ConfigurationFlow
	if err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodConfigurationFlow, map[string]any{}, &flow); err != nil {
		return managedfeishu.SetupState{}, err
	}
	if flow != nil && flow.State == "pending" {
		request := feishuprotocol.ConfigurationCancelRequest{FlowID: flow.ID, Kind: flow.Kind}
		if err := service.managedFeishuSupervisor.Call(ctx, feishuprotocol.MethodConfigurationCancel, request, nil); err != nil {
			return managedfeishu.SetupState{}, errors.New("未能确认指定会话已终止，请检查当前状态")
		}
	}
	return (remoteSetupStore{service}).Load()
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
	report, err := decodeFeishuPermissionReport(result)
	if err != nil || report.Permissions.Verified == nil || !*report.Permissions.Verified || report.Permissions.Identities.Bot.Ready == nil || !*report.Permissions.Identities.Bot.Ready {
		return false
	}
	application, _ := feishuScopeEvidenceState(report.Permissions.Identities.Bot.Application)
	return application == "verified"
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
		Profile:     feishuprotocol.ManagedEventProfile,
		Health:      domain.FeishuSettingsHealth{Core: "running", Bridge: feishuServiceHealth(snapshot), Inbound: feishuInboundHealth(snapshot), Detail: snapshot.Message},
		Permissions: feishuPermissionOverview(permissions, permissionErr),
		Features:    feishuFeatureOverview(settings),
		Targets:     snapshot.TargetAliases,
	}
	return overview, nil
}

func (service *Service) UpdateFeishuFeature(ctx context.Context, request FeishuFeatureUpdateRequest) (domain.FeishuSettingsOverview, error) {
	return domain.FeishuSettingsOverview{}, errors.New("configuration_action_retired")
}

func feishuFeatureOverview(settings managedfeishu.Settings) []domain.FeishuFeatureOverview {
	return []domain.FeishuFeatureOverview{}
}

func feishuPermissionOverview(value map[string]any, callErr error) domain.FeishuPermissionOverview {
	if callErr != nil {
		return domain.FeishuPermissionOverview{Application: "unavailable", User: "unavailable", Missing: []string{}}
	}
	report, err := decodeFeishuPermissionReport(value)
	if err != nil {
		return domain.FeishuPermissionOverview{Application: "unavailable", User: "unavailable", Missing: []string{}}
	}
	appState, appMissing := feishuScopeEvidenceState(report.Permissions.Identities.Bot.Application)
	userState := "unknown"
	if user := report.Permissions.Identities.User; user.Ready != nil {
		if *user.Ready {
			userState = "verified"
		} else {
			userState = "missing"
		}
	}
	return domain.FeishuPermissionOverview{Application: appState, User: userState, Missing: feishuMissingCapabilities(appMissing)}
}

type feishuScopeEvidence struct {
	Complete *bool           `json:"complete"`
	Missing  json.RawMessage `json:"missing"`
}

type feishuPermissionReport struct {
	Permissions struct {
		Verified   *bool `json:"verified"`
		Identities struct {
			Bot struct {
				Ready       *bool                `json:"ready"`
				Application *feishuScopeEvidence `json:"application"`
			} `json:"bot"`
			User struct {
				feishuScopeEvidence
				Ready       *bool                `json:"ready"`
				Application *feishuScopeEvidence `json:"application"`
				OAuth       *feishuScopeEvidence `json:"oauth"`
			} `json:"user"`
		} `json:"identities"`
	} `json:"permissions"`
}

func decodeFeishuPermissionReport(value map[string]any) (feishuPermissionReport, error) {
	var report feishuPermissionReport
	data, err := json.Marshal(value)
	if err != nil {
		return report, err
	}
	err = json.Unmarshal(data, &report)
	return report, err
}

func feishuScopeEvidenceState(evidence *feishuScopeEvidence) (string, []string) {
	if evidence == nil || evidence.Complete == nil || len(evidence.Missing) == 0 {
		return "unknown", nil
	}
	var missing []string
	if json.Unmarshal(evidence.Missing, &missing) != nil || missing == nil {
		return "unknown", nil
	}
	if !*evidence.Complete || len(missing) != 0 {
		return "missing", missing
	}
	return "verified", missing
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
func feishuInboundHealth(snapshot domain.FeishuSnapshot) string {
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
	service.accountReadMu.Lock()
	defer service.accountReadMu.Unlock()
	now := time.Now()
	service.mu.Lock()
	cached := append([]domain.DailyUsageBucket(nil), service.lastServerHistory...)
	if len(cached) == 0 {
		cached = append(cached, service.lastUsage.DailyUsageBuckets...)
	}
	usage := service.lastUsage
	service.mu.Unlock()
	if cachedOnly {
		return cached, nil
	}
	service.refreshAccountUsage(ctx, &usage, now)
	service.mu.Lock()
	service.lastUsage = usage
	values := append([]domain.DailyUsageBucket(nil), service.lastServerHistory...)
	service.mu.Unlock()
	if usage.TokenError != "" {
		return nil, errors.New(usage.TokenError)
	}
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
	service.closeConfiguration()
	if service.integrationRuntime != nil {
		service.integrationRuntime.Close()
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_ = service.stopFeishuSupervisor(stopContext)
	service.stopActivityDiscovery()
	if service.codex != nil {
		_ = service.codex.Close()
	}
	if service.desktop != nil {
		service.desktop.Close()
	}
}

func (service *Service) readCodex(ctx context.Context, now time.Time, active, forceAccountRefresh bool) (domain.UsageSnapshot, []domain.CodexThread) {
	usage, threads, _ := service.readCodexState(ctx, now, active, forceAccountRefresh)
	return usage, threads
}

func (service *Service) readCodexState(ctx context.Context, now time.Time, active, forceAccountRefresh bool) (domain.UsageSnapshot, []domain.CodexThread, []domain.CodexProject) {
	service.accountReadMu.Lock()
	defer service.accountReadMu.Unlock()
	service.mu.Lock()
	usage := service.lastUsage
	threads := service.lastThreads
	codexProjects := service.lastCodexProjects
	needRate := forceAccountRefresh || service.lastRateAttempt.IsZero() || now.Sub(service.lastRateAttempt) >= rateRefreshInterval
	localInterval := idleLocalTokenInterval
	if active {
		localInterval = activeLocalTokenInterval
	}
	needLocal := service.lastLocalTokenAttempt.IsZero() || now.Sub(service.lastLocalTokenAttempt) >= localInterval ||
		service.lastLocalTokenAttempt.In(now.Location()).Format("2006-01-02") != now.Format("2006-01-02")
	needThreads := service.lastThreadAttempt.IsZero() || now.Sub(service.lastThreadAttempt) >= threadRefreshInterval
	if needRate {
		service.lastRateAttempt = now
	}
	if needLocal {
		service.lastLocalTokenAttempt = now
	}
	if needThreads {
		service.lastThreadAttempt = now
	}
	service.mu.Unlock()
	if service.codex == nil {
		service.refreshAccountUsage(ctx, &usage, now)
		if needLocal {
			service.mergeLocalTokens(&usage, now)
		}
		service.mu.Lock()
		service.lastUsage = usage
		service.mu.Unlock()
		return usage, nil, nil
	}
	var threadErr, projectErr error
	var wait sync.WaitGroup
	if needRate {
		wait.Add(1)
		go func() { defer wait.Done(); service.refreshAccountUsage(ctx, &usage, now) }()
	}
	if needThreads {
		wait.Add(2)
		go func() { defer wait.Done(); threads, threadErr = service.codex.FetchThreads(ctx) }()
		go func() { defer wait.Done(); codexProjects, projectErr = service.codex.FetchProjects(ctx) }()
	}
	wait.Wait()
	if needThreads && threadErr == nil && service.integrationRuntime != nil {
		liveIDs := make([]string, 0, len(threads))
		for _, thread := range threads {
			liveIDs = append(liveIDs, thread.ID)
		}
		service.integrationRuntime.ReconcileFreshThreadList(ctx, liveIDs)
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
	if needThreads && projectErr == nil {
		service.lastCodexProjects = codexProjects
	} else if projectErr != nil {
		codexProjects = service.lastCodexProjects
	}
	service.mu.Unlock()
	return usage, threads, codexProjects
}

func (service *Service) refreshAccountUsage(ctx context.Context, usage *domain.UsageSnapshot, now time.Time) {
	usage.Buckets = []domain.RateLimitBucket{}
	usage.TokenSummary = nil
	usage.DailyUsageBuckets = []domain.DailyUsageBucket{}
	usage.RateUpdatedAt = nil
	usage.TokenUpdatedAt = nil
	usage.Status = "offline"
	usage.RateError = "账号额度暂不可用。"
	usage.TokenError = "账号 Token 活动暂不可用，本机统计不受影响。"
	var serverDays []domain.DailyUsageBucket
	if service.codex == nil {
		usage.Status = "codexMissing"
		usage.RateError = "未找到 Codex。"
	} else {
		rates, tokens, rateErr, tokenErr := service.codex.FetchAccountUsage(ctx)
		if rateErr != nil {
			usage.RateError = recoveryMessage(rateErr)
		} else {
			usage.Buckets = domain.NormalizeRateLimits(rates)
			usage.RateUpdatedAt = &now
			usage.RateError = ""
			usage.Status = "available"
			if general := domain.GeneralBucket(usage.Buckets); general == nil || domain.HeadlineRemaining(*general) == nil {
				usage.Status = "unsupportedProtocol"
			}
			if tokenErr == nil {
				usage.TokenSummary = &tokens.Summary
				usage.DailyUsageBuckets = normalizedDays(tokens.DailyUsageBuckets, 14)
				usage.TokenUpdatedAt = &now
				usage.TokenError = ""
				serverDays = normalizedDays(tokens.DailyUsageBuckets, 90)
			}
		}
	}
	service.mu.Lock()
	service.lastRateAttempt = now
	service.lastServerHistory = serverDays
	service.lastServerHistoryAt = time.Time{}
	if usage.TokenError == "" {
		service.lastServerHistoryAt = now
	}
	service.mu.Unlock()
}

func (service *Service) mergeLocalTokens(usage *domain.UsageSnapshot, now time.Time) {
	reader := tokens.Reader{Roots: tokens.DefaultRoots(service.home)}
	usage.LocalDailyUsage = nil
	usage.LocalPreviousDailyUsage = nil
	usage.LocalDailyCost = nil
	usage.LocalTokenUpdatedAt = nil
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

func (service *Service) readProjects(ctx context.Context, request DashboardRequest, threads []domain.CodexThread, observations []domain.TaskObservation, now time.Time, connected ...map[string]bool) domain.ProjectDashboardSnapshot {
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
	service.desktop.ReconcileCandidates(localTaskCandidates(threads))
	pinned := map[string]bool{}
	for _, id := range request.PinnedProjectIDs {
		if id != "" {
			pinned[id] = true
		}
	}
	result.Availability = "available"
	result.Catalog = source.catalog
	result.Projects = domain.BuildProjectDashboard(source.catalog, threads, source.projections, observations, pinned, source.usage, source.launchActions, now, connected...)
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

func workspaceTaskName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || hasControl(value) {
		return "", errors.New("工作区名称无效，无法新建任务")
	}
	return value + " · 新任务", nil
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

func localTaskCandidates(threads []domain.CodexThread) []string {
	result := []string{}
	for _, thread := range threads {
		if thread.ParentThreadID != nil || (thread.AgentNickname != nil && *thread.AgentNickname != "") || thread.Path == nil {
			continue
		}
		result = append(result, thread.ID)
	}
	return result
}
