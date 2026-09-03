package domain

import "time"

const Protocol = "codex-usage-core-v1"

type RateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins,omitempty"`
	ResetsAt           *int64 `json:"resetsAt,omitempty"`
}

func (window RateLimitWindow) RemainingPercent() int {
	remaining := 100 - window.UsedPercent
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}

type CreditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance,omitempty"`
}

type SpendControlLimitSnapshot struct {
	Limit            string `json:"limit"`
	RemainingPercent int    `json:"remainingPercent"`
	ResetsAt         int64  `json:"resetsAt"`
	Used             string `json:"used"`
}

type RateLimitBucket struct {
	LimitID              *string                    `json:"limitId,omitempty"`
	LimitName            *string                    `json:"limitName,omitempty"`
	Primary              *RateLimitWindow           `json:"primary,omitempty"`
	Secondary            *RateLimitWindow           `json:"secondary,omitempty"`
	Credits              *CreditsSnapshot           `json:"credits,omitempty"`
	IndividualLimit      *SpendControlLimitSnapshot `json:"individualLimit,omitempty"`
	PlanType             *string                    `json:"planType,omitempty"`
	RateLimitReachedType *string                    `json:"rateLimitReachedType,omitempty"`
}

type RateLimitsResponse struct {
	RateLimits          RateLimitBucket            `json:"rateLimits"`
	RateLimitsByLimitID map[string]RateLimitBucket `json:"rateLimitsByLimitId,omitempty"`
}

type TokenUsageSummary struct {
	LifetimeTokens        *int64 `json:"lifetimeTokens,omitempty"`
	PeakDailyTokens       *int64 `json:"peakDailyTokens,omitempty"`
	LongestRunningTurnSec *int64 `json:"longestRunningTurnSec,omitempty"`
	CurrentStreakDays     *int64 `json:"currentStreakDays,omitempty"`
	LongestStreakDays     *int64 `json:"longestStreakDays,omitempty"`
}

type TokenUsageBreakdown struct {
	RegularInputTokens int64 `json:"regularInputTokens"`
	CachedInputTokens  int64 `json:"cachedInputTokens"`
	OutputTokens       int64 `json:"outputTokens"`
}

type PricingPlan struct {
	ID                             string `json:"id"`
	Provider                       string `json:"provider"`
	Model                          string `json:"model"`
	Variant                        string `json:"variant,omitempty"`
	DisplayName                    string `json:"displayName"`
	RegularInputMicroUSDPerMillion int64  `json:"regularInputMicroUsdPerMillion"`
	CachedInputMicroUSDPerMillion  int64  `json:"cachedInputMicroUsdPerMillion"`
	OutputMicroUSDPerMillion       int64  `json:"outputMicroUsdPerMillion"`
	BuiltIn                        bool   `json:"builtIn"`
	SourceURL                      string `json:"sourceUrl,omitempty"`
	VerifiedAt                     string `json:"verifiedAt,omitempty"`
}

type PricingSelection struct {
	PlanID      string        `json:"planId,omitempty"`
	CustomPlans []PricingPlan `json:"customPlans,omitempty"`
}

type PricingCatalog struct {
	DefaultPlanID         string        `json:"defaultPlanId"`
	Plans                 []PricingPlan `json:"plans"`
	RejectedCustomPlanIDs []string      `json:"rejectedCustomPlanIds,omitempty"`
}

type TokenCostEstimate struct {
	PlanID               string `json:"planId"`
	Currency             string `json:"currency"`
	Status               string `json:"status"`
	RegularInputMicroUSD int64  `json:"regularInputMicroUsd"`
	CachedInputMicroUSD  int64  `json:"cachedInputMicroUsd"`
	OutputMicroUSD       int64  `json:"outputMicroUsd"`
	TotalMicroUSD        int64  `json:"totalMicroUsd"`
	UncoveredTokens      int64  `json:"uncoveredTokens,omitempty"`
	IncompleteDayCount   int    `json:"incompleteDayCount,omitempty"`
}

func (breakdown TokenUsageBreakdown) TotalTokens() int64 {
	return breakdown.RegularInputTokens + breakdown.CachedInputTokens + breakdown.OutputTokens
}

type DailyUsageBucket struct {
	StartDate string               `json:"startDate"`
	Tokens    int64                `json:"tokens"`
	Breakdown *TokenUsageBreakdown `json:"breakdown,omitempty"`
}

type TokenHistoryComparisonDay struct {
	StartDate      string               `json:"startDate"`
	ServerTokens   *int64               `json:"serverTokens,omitempty"`
	LocalTokens    int64                `json:"localTokens"`
	LocalBreakdown *TokenUsageBreakdown `json:"localBreakdown,omitempty"`
	LocalCost      *TokenCostEstimate   `json:"localCost,omitempty"`
}

type TokenHistoryComparison struct {
	Days             []TokenHistoryComparisonDay `json:"days"`
	ServerError      string                      `json:"serverError,omitempty"`
	SelectedPlan     PricingPlan                 `json:"selectedPlan"`
	LocalCostSummary TokenCostEstimate           `json:"localCostSummary"`
	PricingFallback  bool                        `json:"pricingFallback,omitempty"`
}

type TokenUsageResponse struct {
	Summary           TokenUsageSummary  `json:"summary"`
	DailyUsageBuckets []DailyUsageBucket `json:"dailyUsageBuckets,omitempty"`
}

type UsageSnapshot struct {
	Buckets                 []RateLimitBucket  `json:"buckets"`
	TokenSummary            *TokenUsageSummary `json:"tokenSummary,omitempty"`
	DailyUsageBuckets       []DailyUsageBucket `json:"dailyUsageBuckets"`
	LocalDailyUsage         *DailyUsageBucket  `json:"localDailyUsage,omitempty"`
	LocalPreviousDailyUsage *DailyUsageBucket  `json:"localPreviousDailyUsage,omitempty"`
	LocalDailyCost          *TokenCostEstimate `json:"localDailyCost,omitempty"`
	RateUpdatedAt           *time.Time         `json:"rateUpdatedAt,omitempty"`
	TokenUpdatedAt          *time.Time         `json:"tokenUpdatedAt,omitempty"`
	LocalTokenUpdatedAt     *time.Time         `json:"localTokenUpdatedAt,omitempty"`
	Status                  string             `json:"status"`
	RateError               string             `json:"rateError,omitempty"`
	TokenError              string             `json:"tokenError,omitempty"`
}

type CodexThread struct {
	ID             string           `json:"id"`
	Name           *string          `json:"name,omitempty"`
	CWD            string           `json:"cwd"`
	ParentThreadID *string          `json:"parentThreadId,omitempty"`
	AgentNickname  *string          `json:"agentNickname,omitempty"`
	CreatedAt      int64            `json:"createdAt"`
	UpdatedAt      int64            `json:"updatedAt"`
	Path           *string          `json:"path,omitempty"`
	Source         any              `json:"source,omitempty"`
	Status         ThreadStatus     `json:"status"`
	Turns          []map[string]any `json:"turns,omitempty"`
}

type ThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

type TaskObservation struct {
	ID                           string   `json:"id"`
	HostID                       string   `json:"hostId"`
	AgentNickname                *string  `json:"agentNickname,omitempty"`
	SourceKind                   *string  `json:"sourceKind,omitempty"`
	RuntimeStatus                string   `json:"runtimeStatus"`
	ActiveFlags                  []string `json:"activeFlags"`
	PendingRequestMethods        []string `json:"pendingRequestMethods"`
	HasPendingPlanImplementation bool     `json:"hasPendingPlanImplementation"`
}

type TaskActivitySnapshot struct {
	RunningCount int               `json:"runningCount"`
	WaitingCount int               `json:"waitingCount"`
	ObservedAt   time.Time         `json:"observedAt"`
	Availability string            `json:"availability"`
	Observations []TaskObservation `json:"observations"`
}

type EngineeringMapping struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	RootPath        string `json:"rootPath"`
	FactEntrypoints string `json:"factEntrypoints"`
	Purpose         string `json:"purpose"`
}

type Project struct {
	ID                  string               `json:"id"`
	Name                string               `json:"name"`
	Status              string               `json:"status"`
	Summary             string               `json:"summary"`
	MemoryMode          string               `json:"memoryMode"`
	CardPath            string               `json:"cardPath"`
	ProjectDirectory    string               `json:"projectDirectory"`
	Phase               *string              `json:"phase,omitempty"`
	Focus               *string              `json:"focus,omitempty"`
	EngineeringMappings []EngineeringMapping `json:"engineeringMappings"`
}

type ProjectCatalog struct {
	Protocol    string    `json:"protocol"`
	GeneratedAt string    `json:"generatedAt"`
	KSFRoot     string    `json:"ksfRoot"`
	Projects    []Project `json:"projects"`
}

type RouteCategory struct {
	CategoryID       *string `json:"category_id,omitempty"`
	Name             *string `json:"name,omitempty"`
	ValidationStatus *string `json:"validation_status,omitempty"`
}

type RouteJob struct {
	JobID            *string `json:"job_id,omitempty"`
	Name             *string `json:"name,omitempty"`
	Role             *string `json:"role,omitempty"`
	ValidationStatus *string `json:"validation_status,omitempty"`
}

type RouteAbility struct {
	AbilityID        *string `json:"ability_id,omitempty"`
	Name             *string `json:"name,omitempty"`
	JobID            *string `json:"job_id,omitempty"`
	ValidationStatus *string `json:"validation_status,omitempty"`
}

type DispatchableSkill struct {
	SkillID    *string `json:"skill_id,omitempty"`
	SkillStage *string `json:"skill_stage,omitempty"`
	AbilityID  *string `json:"ability_id,omitempty"`
}

type RouteSummary struct {
	Category           *RouteCategory      `json:"category,omitempty"`
	Jobs               []RouteJob          `json:"jobs"`
	Abilities          []RouteAbility      `json:"abilities"`
	DispatchableSkills []DispatchableSkill `json:"dispatchableSkills"`
	ReceiptSHA256      *string             `json:"receiptSHA256,omitempty"`
}

type TaskBinding struct {
	ProjectCard string        `json:"projectCard"`
	BoundAt     string        `json:"boundAt"`
	ObservedAt  *string       `json:"observedAt,omitempty"`
	Route       *RouteSummary `json:"route,omitempty"`
}

type TaskProjection struct {
	Protocol  string        `json:"protocol"`
	ThreadKey string        `json:"threadKey"`
	Bindings  []TaskBinding `json:"bindings"`
	UpdatedAt string        `json:"updatedAt"`
}

func (projection TaskProjection) CurrentBinding() *TaskBinding {
	if len(projection.Bindings) == 0 {
		return nil
	}
	return &projection.Bindings[len(projection.Bindings)-1]
}

type ResolvedTaskProjection struct {
	ThreadID   string         `json:"threadId"`
	Projection TaskProjection `json:"projection"`
}

type ProjectionResolution struct {
	Protocol    string                   `json:"protocol"`
	Projections []ResolvedTaskProjection `json:"projections"`
}

type ProjectUsageSummary struct {
	CumulativeTokens     int64     `json:"cumulativeTokens"`
	TodayTokens          int64     `json:"todayTokens"`
	TrackingStartedAt    time.Time `json:"trackingStartedAt"`
	IsComplete           bool      `json:"isComplete"`
	UncountedThreadCount int       `json:"uncountedThreadCount"`
}

type ProjectTask struct {
	ID             string        `json:"id"`
	ThreadID       string        `json:"threadId"`
	TaskKey        string        `json:"taskKey"`
	HostID         string        `json:"hostId"`
	Name           *string       `json:"name,omitempty"`
	Classification string        `json:"classification"`
	WaitingReason  *string       `json:"waitingReason,omitempty"`
	Route          *RouteSummary `json:"route,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
	ProjectID      string        `json:"projectId"`
}

type ProjectLaunchAction struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	ScriptPath       string `json:"scriptPath"`
	WorkingDirectory string `json:"workingDirectory"`
	Kind             string `json:"kind"`
}

type ProjectDashboardItem struct {
	ID                     string               `json:"id"`
	Kind                   string               `json:"kind"`
	Project                *Project             `json:"project,omitempty"`
	IsPinned               bool                 `json:"isPinned"`
	Tasks                  []ProjectTask        `json:"tasks"`
	LatestActivity         *time.Time           `json:"latestActivity,omitempty"`
	Usage                  *ProjectUsageSummary `json:"usage,omitempty"`
	LaunchAction           *ProjectLaunchAction `json:"launchAction,omitempty"`
	PreferredEngineeringID *string              `json:"preferredEngineeringId,omitempty"`
}

type ProjectDashboardSnapshot struct {
	Availability string                 `json:"availability"`
	Projects     []ProjectDashboardItem `json:"projects"`
	Catalog      []Project              `json:"catalog"`
	ObservedAt   time.Time              `json:"observedAt"`
	Message      string                 `json:"message,omitempty"`
}

type FeishuControls struct {
	CanSend            bool `json:"canSend"`
	CanSteer           bool `json:"canSteer"`
	CanInterrupt       bool `json:"canInterrupt"`
	CanAnswer          bool `json:"canAnswer"`
	CanRelease         bool `json:"canRelease"`
	AcceptsAttachments bool `json:"acceptsAttachments"`
}

type FeishuTaskLink struct {
	TaskKey           string         `json:"taskKey"`
	Title             string         `json:"title"`
	ProjectName       string         `json:"projectName"`
	TargetAlias       string         `json:"targetAlias"`
	LinkState         string         `json:"linkState"`
	TurnState         string         `json:"turnState"`
	TurnOwner         string         `json:"turnOwner"`
	ActionRequired    string         `json:"actionRequired"`
	Controls          FeishuControls `json:"controls"`
	State             string         `json:"state"`
	CreatedAt         string         `json:"createdAt"`
	UpdatedAt         string         `json:"updatedAt"`
	ExpiresAt         string         `json:"expiresAt"`
	RemainingSeconds  int            `json:"remainingSeconds"`
	HasPendingMessage bool           `json:"hasPendingMessage"`
	DetailAvailable   bool           `json:"detailAvailable"`
	Phase             string         `json:"phase"`
	DetailSummary     string         `json:"detailSummary"`
}

type FeishuSnapshot struct {
	Availability            string           `json:"availability"`
	Message                 string           `json:"message,omitempty"`
	TargetAliases           []string         `json:"targetAliases"`
	TaskLinkProtocolVersion int              `json:"taskLinkProtocolVersion"`
	TaskLinkReady           bool             `json:"taskLinkReady"`
	ReadinessBlockers       []string         `json:"readinessBlockers"`
	Links                   []FeishuTaskLink `json:"links"`
}

type DashboardSnapshot struct {
	Protocol    string                   `json:"protocol"`
	CoreVersion string                   `json:"coreVersion"`
	Platform    string                   `json:"platform"`
	ObservedAt  time.Time                `json:"observedAt"`
	Usage       UsageSnapshot            `json:"usage"`
	Activity    TaskActivitySnapshot     `json:"activity"`
	Projects    ProjectDashboardSnapshot `json:"projects"`
	Feishu      FeishuSnapshot           `json:"feishu"`
}
