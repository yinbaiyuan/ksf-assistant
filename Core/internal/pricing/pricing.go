package pricing

import (
	"math/big"
	"regexp"
	"strings"

	"codexusagebar/core/internal/domain"
)

const (
	DefaultPlanID                   = "openai:gpt-5.6-sol"
	MaxCustomPlans                  = 20
	MaxRateMicroUSDPerMillion int64 = 1_000_000_000
	priceDenominator                = 1_000_000
)

var customIDPattern = regexp.MustCompile(`^custom:[A-Za-z0-9._:-]{1,72}$`)

func builtInPlans() []domain.PricingPlan {
	const (
		openAIURL   = "https://developers.openai.com/api/docs/models/compare"
		deepSeekURL = "https://api-docs.deepseek.com/quick_start/pricing/"
		verifiedAt  = "2026-09-03"
	)
	return []domain.PricingPlan{
		builtIn("openai:gpt-5.6-sol", "OpenAI API", "GPT-5.6 Sol", "", 4_000_000, 400_000, 20_000_000, openAIURL, verifiedAt),
		builtIn("openai:gpt-5.6-terra", "OpenAI API", "GPT-5.6 Terra", "", 2_000_000, 200_000, 12_000_000, openAIURL, verifiedAt),
		builtIn("openai:gpt-5.6-luna", "OpenAI API", "GPT-5.6 Luna", "", 200_000, 20_000, 1_200_000, openAIURL, verifiedAt),
		builtIn("deepseek:deepseek-v4-flash:off-peak", "DeepSeek API", "V4 Flash", "谷时", 220_000, 7_000, 660_000, deepSeekURL, verifiedAt),
		builtIn("deepseek:deepseek-v4-flash:peak", "DeepSeek API", "V4 Flash", "峰时", 440_000, 14_000, 1_320_000, deepSeekURL, verifiedAt),
		builtIn("deepseek:deepseek-v4-pro:off-peak", "DeepSeek API", "V4 Pro", "谷时", 660_000, 22_000, 1_980_000, deepSeekURL, verifiedAt),
		builtIn("deepseek:deepseek-v4-pro:peak", "DeepSeek API", "V4 Pro", "峰时", 1_320_000, 44_000, 3_960_000, deepSeekURL, verifiedAt),
	}
}

func builtIn(id, provider, model, variant string, regular, cached, output int64, sourceURL, verifiedAt string) domain.PricingPlan {
	plan := domain.PricingPlan{
		ID: id, Provider: provider, Model: model, Variant: variant,
		RegularInputMicroUSDPerMillion: regular,
		CachedInputMicroUSDPerMillion:  cached,
		OutputMicroUSDPerMillion:       output,
		BuiltIn:                        true, SourceURL: sourceURL, VerifiedAt: verifiedAt,
	}
	plan.DisplayName = displayName(plan)
	return plan
}

func Catalog(custom []domain.PricingPlan) domain.PricingCatalog {
	plans := builtInPlans()
	seen := make(map[string]bool, len(plans)+len(custom))
	for _, plan := range plans {
		seen[plan.ID] = true
	}
	rejected := []string{}
	accepted := 0
	for _, candidate := range custom {
		id := strings.TrimSpace(candidate.ID)
		if accepted >= MaxCustomPlans || seen[id] || !validCustom(candidate) {
			rejected = append(rejected, rejectedID(id))
			continue
		}
		candidate.ID = id
		candidate.Provider = strings.TrimSpace(candidate.Provider)
		candidate.Model = strings.TrimSpace(candidate.Model)
		candidate.Variant = strings.TrimSpace(candidate.Variant)
		candidate.DisplayName = displayName(candidate)
		candidate.BuiltIn = false
		candidate.SourceURL = ""
		candidate.VerifiedAt = ""
		plans = append(plans, candidate)
		seen[id] = true
		accepted++
	}
	return domain.PricingCatalog{DefaultPlanID: DefaultPlanID, Plans: plans, RejectedCustomPlanIDs: rejected}
}

func Resolve(selection domain.PricingSelection) (domain.PricingPlan, bool) {
	catalog := Catalog(selection.CustomPlans)
	requested := strings.TrimSpace(selection.PlanID)
	if requested != "" {
		if plan, ok := PlanByID(catalog.Plans, requested); ok {
			return plan, false
		}
	}
	plan, _ := PlanByID(catalog.Plans, DefaultPlanID)
	return plan, requested != "" && requested != DefaultPlanID
}

func PlanByID(plans []domain.PricingPlan, id string) (domain.PricingPlan, bool) {
	for _, plan := range plans {
		if plan.ID == id {
			return plan, true
		}
	}
	return domain.PricingPlan{}, false
}

func Estimate(plan domain.PricingPlan, totalTokens int64, breakdown *domain.TokenUsageBreakdown) domain.TokenCostEstimate {
	result := domain.TokenCostEstimate{PlanID: plan.ID, Currency: "USD", Status: "complete"}
	if totalTokens == 0 {
		return result
	}
	if totalTokens < 0 || breakdown == nil || breakdown.RegularInputTokens < 0 || breakdown.CachedInputTokens < 0 || breakdown.OutputTokens < 0 || breakdown.TotalTokens() != totalTokens {
		result.Status = "unavailable"
		result.UncoveredTokens = maxInt64(totalTokens, 0)
		return result
	}
	regular, okRegular := componentCost(breakdown.RegularInputTokens, plan.RegularInputMicroUSDPerMillion)
	cached, okCached := componentCost(breakdown.CachedInputTokens, plan.CachedInputMicroUSDPerMillion)
	output, okOutput := componentCost(breakdown.OutputTokens, plan.OutputMicroUSDPerMillion)
	total, okTotal := checkedSum(regular, cached, output)
	if !okRegular || !okCached || !okOutput || !okTotal {
		result.Status = "unavailable"
		result.UncoveredTokens = totalTokens
		return result
	}
	result.RegularInputMicroUSD = regular
	result.CachedInputMicroUSD = cached
	result.OutputMicroUSD = output
	result.TotalMicroUSD = total
	return result
}

func Aggregate(plan domain.PricingPlan, days []domain.TokenHistoryComparisonDay) domain.TokenCostEstimate {
	result := domain.TokenCostEstimate{PlanID: plan.ID, Currency: "USD", Status: "complete"}
	var coveredTokens int64
	for _, day := range days {
		estimate := Estimate(plan, day.LocalTokens, day.LocalBreakdown)
		if estimate.Status != "complete" {
			result.UncoveredTokens = saturatingAdd(result.UncoveredTokens, estimate.UncoveredTokens)
			result.IncompleteDayCount++
			continue
		}
		coveredTokens = saturatingAdd(coveredTokens, day.LocalTokens)
		result.RegularInputMicroUSD = saturatingAdd(result.RegularInputMicroUSD, estimate.RegularInputMicroUSD)
		result.CachedInputMicroUSD = saturatingAdd(result.CachedInputMicroUSD, estimate.CachedInputMicroUSD)
		result.OutputMicroUSD = saturatingAdd(result.OutputMicroUSD, estimate.OutputMicroUSD)
		result.TotalMicroUSD = saturatingAdd(result.TotalMicroUSD, estimate.TotalMicroUSD)
	}
	if result.IncompleteDayCount > 0 {
		result.Status = "partial"
		if coveredTokens == 0 && result.UncoveredTokens > 0 {
			result.Status = "unavailable"
		}
	}
	return result
}

func validCustom(plan domain.PricingPlan) bool {
	return customIDPattern.MatchString(strings.TrimSpace(plan.ID)) &&
		validLabel(plan.Provider, 60, true) && validLabel(plan.Model, 60, true) && validLabel(plan.Variant, 40, false) &&
		validRate(plan.RegularInputMicroUSDPerMillion) && validRate(plan.CachedInputMicroUSDPerMillion) && validRate(plan.OutputMicroUSDPerMillion)
}

func validLabel(value string, maximum int, required bool) bool {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return false
	}
	if len([]rune(value)) > maximum {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func validRate(value int64) bool { return value >= 0 && value <= MaxRateMicroUSDPerMillion }

func displayName(plan domain.PricingPlan) string {
	parts := []string{strings.TrimSpace(plan.Provider), strings.TrimSpace(plan.Model)}
	if variant := strings.TrimSpace(plan.Variant); variant != "" {
		parts = append(parts, variant)
	}
	return strings.Join(parts, " · ")
}

func rejectedID(id string) string {
	if id == "" {
		return "(missing)"
	}
	return id
}

func componentCost(tokens, rate int64) (int64, bool) {
	if tokens < 0 || !validRate(rate) {
		return 0, false
	}
	value := new(big.Int).Mul(big.NewInt(tokens), big.NewInt(rate))
	value.Add(value, big.NewInt(priceDenominator/2))
	value.Quo(value, big.NewInt(priceDenominator))
	return value.Int64(), value.IsInt64()
}

func checkedSum(values ...int64) (int64, bool) {
	value := big.NewInt(0)
	for _, item := range values {
		value.Add(value, big.NewInt(item))
	}
	return value.Int64(), value.IsInt64()
}

func saturatingAdd(left, right int64) int64 {
	value, ok := checkedSum(left, right)
	if !ok {
		return int64(^uint64(0) >> 1)
	}
	return value
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
