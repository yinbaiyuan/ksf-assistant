package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codexusagebar/core/internal/domain"
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
