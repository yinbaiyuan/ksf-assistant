package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codexusagebar/core/internal/bridge"
	"codexusagebar/core/internal/codex"
	"codexusagebar/core/internal/desktop"
	"codexusagebar/core/internal/domain"
	managedfeishu "codexusagebar/core/internal/feishu"
	"codexusagebar/core/internal/pricing"
	"codexusagebar/core/internal/tokens"
)

const Version = "0.10.0-preview.1"

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
	home                    string
	codex                   *codex.Client
	ksf                     bridge.KSFClient
	feishu                  bridge.FeishuClient
	desktop                 *desktop.ActivityClient
	mu                      sync.Mutex
	lastUsage               domain.UsageSnapshot
	lastThreads             []domain.CodexThread
	lastRateAttempt         time.Time
	lastTokenAttempt        time.Time
	lastLocalTokenAttempt   time.Time
	lastThreadAttempt       time.Time
	projectSources          map[string]projectSourceCache
	feishuRoot              string
	feishuDataRoot          string
	legacyFeishuSupervisor  *bridge.FeishuSupervisor
	managedFeishuSupervisor *managedfeishu.Supervisor
	lastFeishuRoot          string
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
	feishuRoot := os.Getenv("CODEX_USAGE_BAR_FEISHU_SERVICE_ROOT")
	feishuNode := os.Getenv("CODEX_USAGE_BAR_NODE")
	dataRoot := os.Getenv("FEISHU_BRIDGE_DATA_DIR")
	if strings.TrimSpace(dataRoot) == "" {
		dataRoot = filepath.Join(home, ".config", "feishu-bridge")
	}
	var managedSupervisor *managedfeishu.Supervisor
	if executable := strings.TrimSpace(os.Getenv("CODEX_USAGE_BAR_FEISHU_BRIDGE")); executable != "" {
		environment := []string{"FEISHU_BRIDGE_DATA_DIR=" + dataRoot}
		if larkCLI := strings.TrimSpace(os.Getenv("CODEX_USAGE_BAR_LARK_CLI")); larkCLI != "" {
			environment = append(environment, "LARK_CLI_BIN="+larkCLI)
		}
		managedSupervisor = managedfeishu.NewSupervisor(managedfeishu.SupervisorOptions{
			Executable:  executable,
			Directory:   filepath.Dir(executable),
			Environment: environment,
		})
		if setup, err := managedfeishu.NewSetupStore(dataRoot).Load(); err == nil {
			managedSupervisor.SetConfigured(feishuSetupConfiguresBridge(setup.Stage))
		}
	}
	return &Service{
		home:                    home,
		feishu:                  bridge.FeishuClient{Node: feishuNode},
		feishuRoot:              feishuRoot,
		feishuDataRoot:          dataRoot,
		legacyFeishuSupervisor:  bridge.NewFeishuSupervisor(feishuRoot, feishuNode),
		managedFeishuSupervisor: managedSupervisor,
		desktop:                 desktop.New(desktop.DefaultEndpoint(home)),
		trackingAt:              map[string]time.Time{},
		projectSources:          map[string]projectSourceCache{},
		lastUsage:               domain.UsageSnapshot{Buckets: []domain.RateLimitBucket{}, DailyUsageBuckets: []domain.DailyUsageBucket{}, Status: "loading"},
	}
}

func (service *Service) Initialize(ctx context.Context) map[string]any {
	executable, err := codex.LocateExecutable(service.home)
	if err == nil {
		service.codex = &codex.Client{Executable: executable, Timeout: 15 * time.Second}
		_ = service.codex.Start(ctx)
	}
	_ = service.desktop.Start(ctx)
	_ = service.startFeishuSupervisor()
	return map[string]any{
		"protocol": domain.Protocol,
		"version":  Version,
		"platform": runtime.GOOS,
		"capabilities": map[string]bool{
			"usage": true, "localTokens": true, "tokenHistory": true, "tokenHistoryComparison": true, "tokenCostEstimate": true, "projects": true, "taskActivity": true,
			"taskCreation": true, "projectLaunch": true, "feishuTaskLinks": true, "feishuServiceManagement": true,
		},
	}
}

func (service *Service) Dashboard(ctx context.Context, request DashboardRequest) domain.DashboardSnapshot {
	now := time.Now()
	activity := service.desktop.Snapshot(now)
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
	feishu := domain.FeishuSnapshot{Availability: "notConfigured", TargetAliases: []string{}, Links: []domain.FeishuTaskLink{}}
	if strings.TrimSpace(service.feishuRoot) != "" {
		feishu = service.readFeishu(ctx, service.feishuRoot, now)
	}
	return domain.DashboardSnapshot{Protocol: domain.Protocol, CoreVersion: Version, Platform: runtime.GOOS, ObservedAt: now, Usage: usage, Activity: activity, Projects: projects, Feishu: feishu}
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
	return service.feishu.CreateTaskLink(ctx, service.feishuRoot, request.ThreadID, request.Title, request.ProjectName, request.TargetAlias)
}

func (service *Service) ReleaseTaskLink(ctx context.Context, request TaskLinkRequest) (domain.FeishuTaskLink, error) {
	return service.feishu.ReleaseTaskLink(ctx, service.feishuRoot, request.ThreadID)
}

func (service *Service) InterruptTaskLink(ctx context.Context, request TaskLinkRequest) (domain.FeishuTaskLink, error) {
	return service.feishu.InterruptTaskLink(ctx, service.feishuRoot, request.ThreadID)
}

func (service *Service) SendFeishuTest(ctx context.Context, target string) error {
	return service.feishu.SendTest(ctx, service.feishuRoot, target)
}

func (service *Service) FeishuProfile(ctx context.Context) (map[string]any, error) {
	return service.feishu.Profile(ctx, service.feishuRoot)
}

func (service *Service) ConfigureFeishu(ctx context.Context, appID, appSecret string) error {
	return service.feishu.ConfigureExisting(ctx, service.feishuRoot, appID, appSecret)
}

func (service *Service) FeishuSettings() (managedfeishu.Settings, error) {
	return managedfeishu.NewSettingsStore(service.feishuDataRoot).Load()
}

func (service *Service) UpdateFeishuSettings(settings managedfeishu.Settings) (managedfeishu.Settings, error) {
	store := managedfeishu.NewSettingsStore(service.feishuDataRoot)
	previous, err := store.Load()
	if err != nil {
		return managedfeishu.Settings{}, err
	}
	if err := store.Save(settings); err != nil {
		return managedfeishu.Settings{}, err
	}
	if strings.TrimSpace(service.feishuRoot) != "" {
		if _, err := service.SetFeishuProfile(context.Background(), settings.Profile); err != nil {
			if rollbackErr := store.Save(previous); rollbackErr != nil {
				return managedfeishu.Settings{}, fmt.Errorf("更新飞书配置失败，且无法恢复原配置：%v；恢复失败：%w", err, rollbackErr)
			}
			return managedfeishu.Settings{}, err
		}
	}
	return store.Load()
}

func (service *Service) FeishuSetup() (managedfeishu.SetupState, error) {
	return managedfeishu.NewSetupStore(service.feishuDataRoot).Load()
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
		result, err = service.feishu.StartConfig(ctx, service.feishuRoot)
		state.Stage = managedfeishu.SetupAppPending
		if result != nil {
			state.VerificationURL, _ = result["verificationUrl"].(string)
		}
	default:
		return nil, errors.New("不支持的飞书配置方式")
	}
	store := managedfeishu.NewSetupStore(service.feishuDataRoot)
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
	store := managedfeishu.NewSetupStore(service.feishuDataRoot)
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	var result map[string]any
	switch state.Stage {
	case managedfeishu.SetupAppPending, managedfeishu.SetupAppConfigured:
		result, err = service.StartFeishuAuth(ctx)
		if err == nil {
			state.Stage = managedfeishu.SetupAuthorizationPending
			state.VerificationURL, _ = result["verificationUrl"].(string)
			state.UserCode, _ = result["userCode"].(string)
		}
	case managedfeishu.SetupAuthorizationPending:
		err = service.FinishFeishuAuth(ctx)
		if err == nil {
			state.Stage = managedfeishu.SetupPlatformPending
			state.VerificationURL = ""
			state.UserCode = ""
			result = map[string]any{"status": "authenticated"}
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
	result["setup"] = state
	return result, nil
}

func (service *Service) VerifyFeishuSetup(ctx context.Context) (map[string]any, error) {
	permissions, err := service.FeishuPermissions(ctx)
	store := managedfeishu.NewSetupStore(service.feishuDataRoot)
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
	if permissionsReady(permissions) {
		state.Stage = managedfeishu.SetupReady
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

func (service *Service) CancelFeishuSetup() (managedfeishu.SetupState, error) {
	state := managedfeishu.DefaultSetupState()
	err := managedfeishu.NewSetupStore(service.feishuDataRoot).Save(state)
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
	return service.feishu.StartUserAuth(ctx, service.feishuRoot)
}

func (service *Service) FinishFeishuAuth(ctx context.Context) error {
	return service.feishu.FinishUserAuth(ctx, service.feishuRoot)
}

func (service *Service) FeishuPermissions(ctx context.Context) (map[string]any, error) {
	return service.feishu.Permissions(ctx, service.feishuRoot)
}

func (service *Service) SetFeishuProfile(ctx context.Context, profile string) (map[string]any, error) {
	result, err := service.feishu.SetProfile(ctx, service.feishuRoot, profile)
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
	stopContext, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_ = service.stopFeishuSupervisor(stopContext)
	if service.codex != nil {
		_ = service.codex.Close()
	}
	service.desktop.Close()
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

func (service *Service) readFeishu(ctx context.Context, root string, now time.Time) domain.FeishuSnapshot {
	service.mu.Lock()
	if service.lastFeishuRoot == root && !service.lastFeishuAt.IsZero() && now.Sub(service.lastFeishuAt) < feishuRefreshFloor {
		cached := service.lastFeishu
		service.mu.Unlock()
		return cached
	}
	service.mu.Unlock()
	snapshot, err := service.feishu.Inspect(ctx, root)
	if err != nil {
		snapshot = domain.FeishuSnapshot{Availability: "unavailable", Message: err.Error(), TargetAliases: []string{}, Links: []domain.FeishuTaskLink{}}
	}
	service.applyFeishuSupervisorStatus(&snapshot)
	service.mu.Lock()
	service.lastFeishuRoot = root
	service.lastFeishuAt = now
	service.lastFeishu = snapshot
	service.mu.Unlock()
	return snapshot
}

func (service *Service) startFeishuSupervisor() error {
	if service.managedFeishuSupervisor != nil {
		return service.managedFeishuSupervisor.Start()
	}
	return service.legacyFeishuSupervisor.Start()
}

func (service *Service) restartFeishuSupervisor(ctx context.Context) error {
	if service.managedFeishuSupervisor != nil {
		return service.managedFeishuSupervisor.Restart(ctx)
	}
	return service.legacyFeishuSupervisor.Restart()
}

func (service *Service) stopFeishuSupervisor(ctx context.Context) error {
	if service.managedFeishuSupervisor != nil {
		return service.managedFeishuSupervisor.Stop(ctx)
	}
	return service.legacyFeishuSupervisor.Stop(ctx)
}

func (service *Service) feishuSupervisorStatus() map[string]any {
	if service.managedFeishuSupervisor != nil {
		status := service.managedFeishuSupervisor.Status()
		return map[string]any{
			"state": status.State, "configured": status.Configured, "pid": status.PID,
			"restartCount": status.RestartCount, "lastError": status.LastError,
		}
	}
	legacy := service.legacyFeishuSupervisor.Status()
	running, _ := legacy["running"].(bool)
	state := managedfeishu.StateStopped
	if running {
		state = managedfeishu.StateRunning
	}
	return map[string]any{
		"state": state, "configured": strings.TrimSpace(service.feishuRoot) != "",
		"pid": legacy["pid"], "restartCount": 0, "lastError": "",
	}
}

func (service *Service) applyFeishuSupervisorStatus(snapshot *domain.FeishuSnapshot) {
	status := service.feishuSupervisorStatus()
	snapshot.ProcessState, _ = status["state"].(string)
	snapshot.Configured, _ = status["configured"].(bool)
	snapshot.ProcessPID, _ = status["pid"].(int)
	snapshot.RestartCount, _ = status["restartCount"].(int)
	snapshot.LastError, _ = status["lastError"].(string)
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
