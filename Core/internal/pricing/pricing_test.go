package pricing

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"ksfassistant/core/internal/domain"
)

func TestCatalogContainsEightOfficialPlansAndDefault(t *testing.T) {
	catalog := Catalog(nil)
	if catalog.DefaultPlanID != "openai:gpt-6-astra" {
		t.Fatalf("unexpected default plan: %q", catalog.DefaultPlanID)
	}
	if len(catalog.Plans) != 8 {
		t.Fatalf("expected 8 built-in plans, got %d", len(catalog.Plans))
	}
	plan, ok := PlanByID(catalog.Plans, "openai:gpt-5.6-sol")
	if !ok || plan.RegularInputMicroUSDPerMillion != 4_000_000 || plan.CachedInputMicroUSDPerMillion != 400_000 || plan.OutputMicroUSDPerMillion != 20_000_000 {
		t.Fatalf("unexpected GPT-5.6 Sol pricing: %#v", plan)
	}
	if !plan.BuiltIn || plan.SourceURL == "" || plan.VerifiedAt == "" {
		t.Fatalf("built-in source metadata is required: %#v", plan)
	}
}

func TestAllBuiltInRatesMatchVersionedCatalog(t *testing.T) {
	expected := map[string][3]int64{
		"openai:gpt-6-astra":                  {10_000_000, 1_000_000, 50_000_000},
		"openai:gpt-5.6-sol":                  {4_000_000, 400_000, 20_000_000},
		"openai:gpt-5.6-terra":                {2_000_000, 200_000, 12_000_000},
		"openai:gpt-5.6-luna":                 {200_000, 20_000, 1_200_000},
		"deepseek:deepseek-v4-flash:off-peak": {220_000, 7_000, 660_000},
		"deepseek:deepseek-v4-flash:peak":     {440_000, 14_000, 1_320_000},
		"deepseek:deepseek-v4-pro:off-peak":   {660_000, 22_000, 1_980_000},
		"deepseek:deepseek-v4-pro:peak":       {1_320_000, 44_000, 3_960_000},
	}
	for id, rates := range expected {
		plan, ok := PlanByID(Catalog(nil).Plans, id)
		if !ok || [3]int64{plan.RegularInputMicroUSDPerMillion, plan.CachedInputMicroUSDPerMillion, plan.OutputMicroUSDPerMillion} != rates {
			t.Fatalf("unexpected rates for %s: %#v", id, plan)
		}
	}
}

func TestCatalogCapsCustomPlansAndRejectsDuplicateIDsAndLongLabels(t *testing.T) {
	plans := make([]domain.PricingPlan, 0, MaxCustomPlans+3)
	for index := 0; index < MaxCustomPlans+1; index++ {
		plans = append(plans, domain.PricingPlan{ID: fmt.Sprintf("custom:%d", index), Provider: "P", Model: "M"})
	}
	plans = append(plans,
		domain.PricingPlan{ID: "custom:0", Provider: "P", Model: "duplicate"},
		domain.PricingPlan{ID: "custom:long", Provider: strings.Repeat("p", 61), Model: "M"},
	)
	catalog := Catalog(plans)
	if len(catalog.Plans) != 8+MaxCustomPlans || len(catalog.RejectedCustomPlanIDs) != 3 {
		t.Fatalf("unexpected custom catalog bounds: %#v", catalog)
	}
}

func TestCatalogAcceptsValidCustomPlanAndRejectsUnsafePlans(t *testing.T) {
	valid := domain.PricingPlan{
		ID: "custom:team-model", Provider: "Team API", Model: "Model A", Variant: "标准",
		RegularInputMicroUSDPerMillion: 1_250_000,
		CachedInputMicroUSDPerMillion:  125_000,
		OutputMicroUSDPerMillion:       8_000_000,
	}
	catalog := Catalog([]domain.PricingPlan{
		valid,
		{ID: DefaultPlanID, Provider: "Fake", Model: "Collision"},
		{ID: "custom:invalid-price", Provider: "Fake", Model: "Bad", RegularInputMicroUSDPerMillion: MaxRateMicroUSDPerMillion + 1},
	})
	if _, ok := PlanByID(catalog.Plans, valid.ID); !ok {
		t.Fatalf("valid custom plan missing: %#v", catalog)
	}
	if len(catalog.RejectedCustomPlanIDs) != 2 {
		t.Fatalf("expected two rejected plans, got %#v", catalog.RejectedCustomPlanIDs)
	}
}

func TestResolveFallsBackToDefaultPlan(t *testing.T) {
	plan, fallback := Resolve(domain.PricingSelection{PlanID: "missing"})
	if plan.ID != DefaultPlanID || !fallback {
		t.Fatalf("expected default fallback, got %#v fallback=%t", plan, fallback)
	}
}

func TestEstimateUsesFixedPointBreakdown(t *testing.T) {
	plan, _ := Resolve(domain.PricingSelection{PlanID: "openai:gpt-5.6-sol"})
	estimate := Estimate(plan, 3_000_000, &domain.TokenUsageBreakdown{
		RegularInputTokens: 1_000_000,
		CachedInputTokens:  1_000_000,
		OutputTokens:       1_000_000,
	})
	if estimate.Status != "complete" || estimate.TotalMicroUSD != 24_400_000 {
		t.Fatalf("unexpected estimate: %#v", estimate)
	}
	if estimate.RegularInputMicroUSD != 4_000_000 || estimate.CachedInputMicroUSD != 400_000 || estimate.OutputMicroUSD != 20_000_000 {
		t.Fatalf("unexpected component costs: %#v", estimate)
	}
}

func TestDefaultAstraEstimateAndSource(t *testing.T) {
	plan, fallback := Resolve(domain.PricingSelection{})
	if fallback || plan.ID != "openai:gpt-6-astra" || plan.SourceURL != "https://developers.openai.com/api/docs/pricing" || plan.VerifiedAt != "2026-09-08" {
		t.Fatalf("unexpected default pricing: %#v", plan)
	}
	estimate := Estimate(plan, 3_000_000, &domain.TokenUsageBreakdown{RegularInputTokens: 1_000_000, CachedInputTokens: 1_000_000, OutputTokens: 1_000_000})
	if estimate.Status != "complete" || estimate.TotalMicroUSD != 61_000_000 {
		t.Fatalf("expected $61 for one million tokens in each category: %#v", estimate)
	}
}

func TestEstimateDoesNotGuessMissingBreakdownOrOverflow(t *testing.T) {
	plan, _ := Resolve(domain.PricingSelection{PlanID: DefaultPlanID})
	missing := Estimate(plan, 42, nil)
	if missing.Status != "unavailable" || missing.UncoveredTokens != 42 {
		t.Fatalf("missing breakdown must remain unavailable: %#v", missing)
	}
	overflow := Estimate(plan, math.MaxInt64, &domain.TokenUsageBreakdown{OutputTokens: math.MaxInt64})
	if overflow.Status != "unavailable" || overflow.UncoveredTokens != math.MaxInt64 {
		t.Fatalf("overflow must fail closed: %#v", overflow)
	}
}

func TestAggregateReportsKnownSubtotalAndIncompleteDays(t *testing.T) {
	plan, _ := Resolve(domain.PricingSelection{PlanID: "openai:gpt-5.6-luna"})
	estimate := Aggregate(plan, []domain.TokenHistoryComparisonDay{
		{StartDate: "2026-09-01", LocalTokens: 1_000_000, LocalBreakdown: &domain.TokenUsageBreakdown{RegularInputTokens: 1_000_000}},
		{StartDate: "2026-09-02", LocalTokens: 100},
		{StartDate: "2026-09-03", LocalTokens: 0},
	})
	if estimate.Status != "partial" || estimate.TotalMicroUSD != 200_000 || estimate.UncoveredTokens != 100 || estimate.IncompleteDayCount != 1 {
		t.Fatalf("unexpected partial aggregate: %#v", estimate)
	}
}
