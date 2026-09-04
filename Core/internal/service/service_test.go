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

	"codexusagebar/core/internal/domain"
	managedfeishu "codexusagebar/core/internal/feishu"
)

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

func TestNormalizedHistoryDayCountUsesBoundedDefault(t *testing.T) {
	values := map[int]int{-1: 30, 0: 30, 1: 1, 30: 30, 90: 90, 91: 90}
	for input, expected := range values {
		if actual := normalizedHistoryDayCount(input); actual != expected {
			t.Fatalf("normalizedHistoryDayCount(%d) = %d, expected %d", input, actual, expected)
		}
	}
}

func TestPermissionsReadyRequiresVerifiedUserWithNoMissingScopes(t *testing.T) {
	ready := map[string]any{"permissions": map[string]any{
		"verified": true,
		"identities": map[string]any{"user": map[string]any{
			"ready": true, "missing": []any{},
		}},
	}}
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

func TestOnlyReadyFeishuSetupConfiguresManagedBridge(t *testing.T) {
	for _, stage := range []string{
		managedfeishu.SetupNotStarted,
		managedfeishu.SetupAppPending,
		managedfeishu.SetupAppConfigured,
		managedfeishu.SetupAuthorizationPending,
		managedfeishu.SetupPlatformPending,
		managedfeishu.SetupVerifying,
		managedfeishu.SetupFailed,
	} {
		if feishuSetupConfiguresBridge(stage) {
			t.Fatalf("unfinished stage %q configured the bridge", stage)
		}
	}
	if !feishuSetupConfiguresBridge(managedfeishu.SetupReady) {
		t.Fatal("ready setup did not configure the bridge")
	}
}

func TestFeishuSetupSettingsKeepRealWritesDisabledUntilConfirmation(t *testing.T) {
	settings := managedfeishu.DefaultSettings()
	prepared := prepareFeishuDryRunSettings(settings)
	if prepared.Profile != managedfeishu.ProfilePrimary {
		t.Fatalf("onboarding must enable the primary inbound profile: %q", prepared.Profile)
	}
	if !prepared.Outbound.Enabled || !prepared.Outbound.DryRun {
		t.Fatalf("verification must prepare dry-run only: %#v", prepared.Outbound)
	}
	if feishuSetupCanBecomeReady(prepared) {
		t.Fatal("dry-run setup must not be marked ready")
	}

	activated := activateFeishuSettings(prepared)
	if !activated.Outbound.Enabled || activated.Outbound.DryRun {
		t.Fatalf("explicit confirmation did not activate real outbound: %#v", activated.Outbound)
	}
	if !feishuSetupCanBecomeReady(activated) {
		t.Fatal("confirmed outbound setup should be eligible for ready")
	}
}

func TestFeishuSettingsOverviewSeparatesEnabledFromWriteModes(t *testing.T) {
	settings := managedfeishu.DefaultSettings()
	settings.Group.Enabled = true
	settings.Directory.Enabled = true
	settings.Docbox = managedfeishu.DryRunSwitch{Enabled: true, DryRun: true}
	settings.Actionbox = managedfeishu.DryRunSwitch{Enabled: true, DryRun: false}
	features := feishuFeatureOverview(settings)
	states := map[string]string{}
	for _, feature := range features {
		states[feature.ID] = feature.State
	}
	if states["groupMessaging"] != "enabled" || states["peopleDirectory"] != "enabled" || states["groupDirectory"] != "off" || states["docbox"] != "dry_run" || states["actionbox"] != "live" {
		t.Fatalf("unexpected feature projection: %#v", states)
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

func TestFeishuActivationWaitsForReadyBeforeSending(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "func (service *Service) ActivateFeishuSetup")
	if start < 0 {
		t.Fatal("activation implementation not found")
	}
	end := strings.Index(body[start:], "func (service *Service) prepareFeishuDryRun")
	if end < 0 {
		t.Fatal("activation implementation not found")
	}
	body = body[start : start+end]
	wait := strings.Index(body, `waitForFeishuAvailability(ctx, "ready"`)
	send := strings.Index(body, "service.SendFeishuTest(ctx, targetAlias)")
	if wait < 0 || send <= wait {
		t.Fatal("activation must wait for the managed bridge before its real test send")
	}
}

func TestConfigureCodexProcessEnvironmentDiscoversUserInstallWithoutPATH(t *testing.T) {
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
	t.Setenv("CODEX_BIN", "")
	resolved, err := configureCodexProcessEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != executable || os.Getenv("CODEX_BIN") != executable {
		t.Fatalf("Codex path was not injected: resolved=%q env=%q", resolved, os.Getenv("CODEX_BIN"))
	}
}

func TestCodexAssistantSupportRootUsesTheCurrentUserConfigurationDirectory(t *testing.T) {
	home := t.TempDir()
	configured := filepath.Join(home, "configured-support")
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", configured)
	} else if runtime.GOOS == "darwin" {
		t.Setenv("HOME", home)
	} else {
		t.Setenv("XDG_CONFIG_HOME", configured)
	}
	root := codexAssistantSupportRoot(home)
	if !filepath.IsAbs(root) || filepath.Base(root) != "CodexUsageBar" {
		t.Fatalf("unexpected CodexAssistant support root: %q", root)
	}
}
