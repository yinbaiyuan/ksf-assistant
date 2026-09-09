package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/corebridge"
	"ksfassistant/core/internal/desktop"
	"ksfassistant/core/internal/domain"
	managedfeishu "ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/privateipc"
)

func TestPrivateBridgeCapabilitiesDegradeIndependently(t *testing.T) {
	root := t.TempDir()
	service := &Service{
		hostContextStore: integration.NewHostContextStore(root),
		desktop:          desktop.New(""),
	}
	capabilities := service.privateCapabilities()
	if capabilities.Protocol != corebridge.Protocol {
		t.Fatalf("unexpected protocol: %s", capabilities.Protocol)
	}
	if capabilities.Capabilities["codexAppServer"].State != "unavailable" || capabilities.Capabilities["ksfContext"].State != "unavailable" {
		t.Fatalf("capabilities did not degrade independently: %#v", capabilities)
	}
}

func TestCorePrivateRPCRejectsUnknownAndUnexpectedParams(t *testing.T) {
	service, _ := newServiceBridgeFixture(t, "normal")
	ctx := managedfeishu.WithEpoch(context.Background(), service.managedFeishuSupervisor.Generation())
	tests := []struct {
		method string
		params json.RawMessage
	}{
		{method: feishuprotocol.SnapshotPush, params: json.RawMessage(`{"revision":2,"unexpected":true}`)},
		{method: feishuprotocol.EventDeliver, params: json.RawMessage(`{"id":"event","kind":"message","payload":{},"unexpected":true}`)},
	}
	for _, test := range tests {
		_, err := service.HandlePrivateRPC(ctx, test.method, test.params)
		var rpcErr *privateipc.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
			t.Fatalf("expected -32602, got %T %v", err, err)
		}
	}
}

func TestPrivateBridgeSnapshotRejectsStaleRevision(t *testing.T) {
	service, _ := newServiceBridgeFixture(t, "normal")
	ctx := managedfeishu.WithEpoch(context.Background(), service.managedFeishuSupervisor.Generation())
	newer, _ := json.Marshal(feishuprotocol.Snapshot{Revision: 9, Availability: "ready", ProcessState: "running"})
	older, _ := json.Marshal(feishuprotocol.Snapshot{Revision: 8, Availability: "unavailable", ProcessState: "stopped"})
	if _, err := service.HandlePrivateRPC(ctx, feishuprotocol.SnapshotPush, newer); err != nil {
		t.Fatal(err)
	}
	if _, err := service.HandlePrivateRPC(ctx, feishuprotocol.SnapshotPush, older); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.lastFeishu.Revision != 9 || service.lastFeishu.Availability != "ready" {
		t.Fatalf("stale snapshot replaced revision 9: %#v", service.lastFeishu)
	}
}

func TestControlWorkingDirectoryReadsNestedAuthoritativeState(t *testing.T) {
	state := map[string]any{"thread": map[string]any{"workspace": map[string]any{"cwd": "/private/project"}}}
	if value := recursiveControlString(state, "cwd", "workingDirectory", "workspaceRoot"); value != "/private/project" {
		t.Fatalf("working directory = %q", value)
	}
}

func TestTaskBootstrapRequiresProjectCardInsideKSFRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "10项目", "示例", "项目记忆卡.md")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("# 示例"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-a", Name: "示例项目", CardPath: inside}

	name, prompt, err := taskBootstrap(project, root, "contextPreparation")
	if err != nil {
		t.Fatal(err)
	}
	if name != "示例项目 · 新任务" || !strings.Contains(prompt, inside) {
		t.Fatalf("unexpected bootstrap: %q %q", name, prompt)
	}

	outside := filepath.Join(t.TempDir(), "项目记忆卡.md")
	if err := os.WriteFile(outside, []byte("# 越界"), 0o600); err != nil {
		t.Fatal(err)
	}
	project.CardPath = outside
	if _, _, err := taskBootstrap(project, root, "contextPreparation"); err == nil {
		t.Fatal("expected an out-of-root project card to be rejected")
	}
}

func TestProjectByIDUsesCatalogAuthority(t *testing.T) {
	projects := []domain.Project{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	project, ok := projectByID(projects, "b")
	if !ok || project.Name != "B" {
		t.Fatalf("unexpected project: %#v %v", project, ok)
	}
	if _, ok := projectByID(projects, "missing"); ok {
		t.Fatal("unknown project should not resolve")
	}
}

func TestWorkspaceTaskNameRequiresVisibleSafeName(t *testing.T) {
	name, err := workspaceTaskName(" 测试 ")
	if err != nil || name != "测试 · 新任务" {
		t.Fatalf("unexpected workspace task name: %q %v", name, err)
	}
	for _, value := range []string{"", "bad\nname"} {
		if _, err := workspaceTaskName(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestNormalizedHistoryDayCountUsesBoundedDefault(t *testing.T) {
	values := map[int]int{-1: 30, 0: 30, 1: 1, 30: 30, 90: 90, 91: 90}
	for input, expected := range values {
		if actual := normalizedHistoryDayCount(input); actual != expected {
			t.Fatalf("normalizedHistoryDayCount(%d) = %d, expected %d", input, actual, expected)
		}
	}
}

func TestPermissionsReadyRequiresVerifiedUserWithNoMissingScopes(t *testing.T) {
	ready := completeSetupPermissions()
	if !permissionsReady(ready) {
		t.Fatal("expected complete permissions to be ready")
	}
	ready["permissions"].(map[string]any)["verified"] = false
	if permissionsReady(ready) {
		t.Fatal("unverified permissions must not be ready")
	}
}

func TestCompareTokenHistoryAlignsServerAndLocalDays(t *testing.T) {
	breakdown := &domain.TokenUsageBreakdown{RegularInputTokens: 20, CachedInputTokens: 60, OutputTokens: 20}
	days := compareTokenHistory(
		[]domain.DailyUsageBucket{
			{StartDate: "2026-09-01", Tokens: 100, Breakdown: breakdown},
			{StartDate: "2026-09-02", Tokens: 40},
			{StartDate: "2026-09-03", Tokens: 10},
		},
		[]domain.DailyUsageBucket{
			{StartDate: "2026-09-02", Tokens: 200},
			{StartDate: "2026-09-01", Tokens: 500},
		},
	)

	if len(days) != 3 || days[0].StartDate != "2026-09-01" || days[0].ServerTokens == nil || *days[0].ServerTokens != 500 {
		t.Fatalf("unexpected first comparison day: %#v", days)
	}
	if days[0].LocalTokens != 100 || days[0].LocalBreakdown != breakdown {
		t.Fatalf("local usage was not preserved: %#v", days[0])
	}
	if days[1].ServerTokens == nil || *days[1].ServerTokens != 200 {
		t.Fatalf("server day was not aligned by date: %#v", days[1])
	}
	if days[2].ServerTokens != nil {
		t.Fatalf("a missing server day must remain unavailable: %#v", days[2])
	}
}

func TestApplyPricingToHistoryAddsDailyAndSummaryCosts(t *testing.T) {
	days := []domain.TokenHistoryComparisonDay{
		{
			StartDate: "2026-09-01", LocalTokens: 1_000_000,
			LocalBreakdown: &domain.TokenUsageBreakdown{RegularInputTokens: 1_000_000},
		},
		{StartDate: "2026-09-02", LocalTokens: 25},
	}
	priced, plan, summary, fallback := applyPricingToHistory(days, domain.PricingSelection{PlanID: "openai:gpt-5.6-luna"})
	if fallback || plan.ID != "openai:gpt-5.6-luna" {
		t.Fatalf("unexpected selected plan: %#v fallback=%t", plan, fallback)
	}
	if priced[0].LocalCost == nil || priced[0].LocalCost.TotalMicroUSD != 200_000 {
		t.Fatalf("daily cost missing: %#v", priced[0])
	}
	if priced[1].LocalCost == nil || priced[1].LocalCost.Status != "unavailable" {
		t.Fatalf("missing composition must remain unavailable: %#v", priced[1])
	}
	if summary.Status != "partial" || summary.TotalMicroUSD != 200_000 || summary.IncompleteDayCount != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestRepriceOnlyUsesLoadedHistoryWithoutScanningOrRefreshingServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", t.TempDir())
	t.Setenv("KSF_ASSISTANT_FEISHU_BRIDGE", "")
	service := New()
	service.lastTokenHistoryDays = 30
	service.lastTokenHistory = []domain.DailyUsageBucket{{
		StartDate: "2026-09-03", Tokens: 1_000_000,
		Breakdown: &domain.TokenUsageBreakdown{RegularInputTokens: 1_000_000},
	}}
	service.lastServerHistory = []domain.DailyUsageBucket{{StartDate: "2026-09-03", Tokens: 2_000_000}}

	comparison, err := service.TokenHistoryComparison(context.Background(), TokenHistoryRequest{
		DayCount: 30, RepriceOnly: true,
		PricingSelection: domain.PricingSelection{PlanID: "openai:gpt-5.6-luna"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Days) != 1 || comparison.Days[0].LocalCost == nil || comparison.Days[0].LocalCost.TotalMicroUSD != 200_000 {
		t.Fatalf("cached history was not repriced: %#v", comparison)
	}
	if comparison.Days[0].ServerTokens == nil || *comparison.Days[0].ServerTokens != 2_000_000 {
		t.Fatalf("cached server history was not reused: %#v", comparison.Days[0])
	}
}

func TestNormalizedDaysSortsBeforeApplyingLimit(t *testing.T) {
	days := normalizedDays([]domain.DailyUsageBucket{
		{StartDate: "2026-09-03", Tokens: 3},
		{StartDate: "2026-09-01", Tokens: 1},
		{StartDate: "2026-09-02", Tokens: 2},
	}, 2)
	if len(days) != 2 || days[0].StartDate != "2026-09-02" || days[1].StartDate != "2026-09-03" {
		t.Fatalf("unexpected normalized days: %#v", days)
	}
}

func TestDashboardRecognizesNativeFeishuRuntimeWithoutLegacyServiceRoot(t *testing.T) {
	nativeOnly := &Service{managedFeishuSupervisor: &managedfeishu.Supervisor{}}
	if !nativeOnly.hasFeishuRuntime() {
		t.Fatal("native managed runtime was incorrectly treated as unconfigured")
	}
	if (&Service{}).hasFeishuRuntime() {
		t.Fatal("missing Feishu runtime was treated as configured")
	}
}

func TestFeishuActivationSettingsChangeOnlyOutbound(t *testing.T) {
	settings := managedfeishu.DefaultSettings()
	settings.Profile = "manual-only"
	settings.Actionbox = managedfeishu.DryRunSwitch{Enabled: true, DryRun: false}
	activated := activateFeishuSettings(settings)
	if !activated.Outbound.Enabled || activated.Outbound.DryRun {
		t.Fatalf("explicit confirmation did not activate real outbound: %#v", activated.Outbound)
	}
	activated.Outbound = settings.Outbound
	if activated != settings {
		t.Fatal("activation changed unrelated policy")
	}
}

func TestFeishuSettingsOverviewSeparatesEnabledFromWriteModes(t *testing.T) {
	if len(feishuFeatureOverview(managedfeishu.DefaultSettings())) != 0 {
		t.Fatal("retired product capabilities exposed")
	}
}

func TestFeishuMissingCapabilitiesAreSanitizedAndGrouped(t *testing.T) {
	got := feishuMissingCapabilities([]string{"docs:document.content:read", "contact:user:search", "docs:document:create", "unknown:scope"})
	if strings.Join(got, ",") != "人员与群组,文档与知识库" {
		t.Fatalf("unexpected capability projection: %#v", got)
	}
}

func TestFeishuPermissionFailureKeepsMissingAsEmptyArray(t *testing.T) {
	value := feishuPermissionOverview(nil, errors.New("permission check failed"))
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"missing":[]`) {
		t.Fatalf("permission failure must remain decodable by desktop hosts: %s", data)
	}
}

func TestNormalizedFeishuSnapshotKeepsCollectionsAsArrays(t *testing.T) {
	value := normalizedFeishuSnapshot(domain.FeishuSnapshot{Availability: "notConfigured"})
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, field := range []string{`"targetAliases":[]`, `"readinessBlockers":[]`, `"links":[]`} {
		if !strings.Contains(text, field) {
			t.Fatalf("desktop collection contract %s was not preserved: %s", field, text)
		}
	}
}

func TestFeishuActivationNeverSendsOrRollsBack(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "func (service *Service) ActivateFeishuSetup")
	if start < 0 {
		t.Fatal("activation implementation not found")
	}
	end := strings.Index(body[start:], "func (service *Service) saveAndRestartFeishuSettings")
	if end < 0 {
		t.Fatal("activation implementation not found")
	}
	body = body[start : start+end]
	wait := strings.Index(body, `waitForFeishuAvailability(ctx, "ready"`)
	if wait >= 0 || !strings.Contains(body, "configuration_action_retired") || strings.Contains(body, "SendFeishuTest") || strings.Contains(body, "rollback") || strings.Contains(body, "permissionsReady") {
		t.Fatal("activation must only enable outbound and observe runtime without user-scope gating or a test send")
	}
}

func TestConfigureCodexProcessEnvironmentHonorsExplicitInstallWithoutPATH(t *testing.T) {
	home := t.TempDir()
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}
	executable := filepath.Join(home, ".local", "bin", name)
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	t.Setenv("CODEX_BIN", executable)
	resolved, err := configureCodexProcessEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != executable || os.Getenv("CODEX_BIN") != executable {
		t.Fatalf("Codex path was not injected: resolved=%q env=%q", resolved, os.Getenv("CODEX_BIN"))
	}
}

func TestKSFAssistantSupportRootUsesTheCurrentUserConfigurationDirectory(t *testing.T) {
	home := t.TempDir()
	configured := filepath.Join(home, "configured-support")
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", configured)
	} else if runtime.GOOS == "darwin" {
		t.Setenv("HOME", home)
	} else {
		t.Setenv("XDG_CONFIG_HOME", configured)
	}
	root := ksfAssistantSupportRoot(home)
	if !filepath.IsAbs(root) || filepath.Base(root) != "KSFAssistant" {
		t.Fatalf("unexpected KSFAssistant support root: %q", root)
	}
}

func TestPreparedTaskSubmissionIsBoundAndConsumedOnce(t *testing.T) {
	request := SubmitTaskRequest{ThreadID: "draft", HostID: "local", CWD: "/project", Prompt: "exact input"}
	service := &Service{preparedTasks: map[string]preparedTask{"draft": {cwd: request.CWD, prompt: request.Prompt, expiresAt: time.Now().Add(time.Minute)}}}
	mismatch := request
	mismatch.Prompt = "different input"
	if err := service.consumePreparedTask(mismatch); err == nil {
		t.Fatal("accepted changed input")
	}
	mismatch = request
	mismatch.HostID = "remote"
	if err := service.consumePreparedTask(mismatch); err == nil {
		t.Fatal("accepted another host")
	}
	if err := service.consumePreparedTask(request); err != nil {
		t.Fatal(err)
	}
	if err := service.consumePreparedTask(request); err == nil {
		t.Fatal("allowed duplicate dispatch")
	}
	service.preparedTasks["draft"] = preparedTask{cwd: request.CWD, prompt: request.Prompt, expiresAt: time.Now().Add(-time.Second)}
	if err := service.consumePreparedTask(request); err == nil {
		t.Fatal("accepted expired draft")
	}
}
