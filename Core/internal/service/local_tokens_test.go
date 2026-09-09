package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ksfassistant/core/internal/domain"
	"ksfassistant/core/internal/pricing"
)

func TestLocalTokensRefreshAtMidnightWithoutActiveTasks(test *testing.T) {
	home := test.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		test.Fatal(err)
	}
	content := `{"timestamp":"2026-09-08T15:59:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20,"total_tokens":120}}}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "yesterday.jsonl"), []byte(content), 0o600); err != nil {
		test.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 0, 0, 1, 0, time.FixedZone("UTC+8", 8*60*60))
	service := &Service{home: home}
	previous, _ := service.readCodex(context.Background(), now.Add(-2*time.Second), false, false)
	if previous.LocalDailyUsage == nil || previous.LocalDailyUsage.Tokens != 120 {
		test.Fatalf("invalid yesterday baseline: %+v", previous)
	}
	usage, _ := service.readCodex(context.Background(), now, false, false)
	if usage.LocalDailyUsage == nil || usage.LocalDailyUsage.StartDate != "2026-09-09" || usage.LocalDailyUsage.Tokens != 0 || usage.LocalDailyUsage.Breakdown == nil {
		test.Fatalf("today must be a dated zero bucket, got %+v", usage.LocalDailyUsage)
	}
	if usage.LocalPreviousDailyUsage == nil || usage.LocalPreviousDailyUsage.StartDate != "2026-09-08" || usage.LocalPreviousDailyUsage.Tokens != 120 {
		test.Fatalf("yesterday must retain its own usage, got %+v", usage.LocalPreviousDailyUsage)
	}
	plan, _ := pricing.Resolve(domain.PricingSelection{})
	cost := pricing.Estimate(plan, usage.LocalDailyUsage.Tokens, usage.LocalDailyUsage.Breakdown)
	if cost.Status != "complete" || cost.TotalMicroUSD != 0 {
		test.Fatalf("zero usage must cost zero, got %+v", cost)
	}
}

func TestLocalTokensMissingSourceDoesNotReusePreviousDay(test *testing.T) {
	now := time.Date(2026, 9, 9, 11, 38, 56, 0, time.UTC)
	usage := domain.UsageSnapshot{
		LocalDailyUsage:         &domain.DailyUsageBucket{StartDate: "2026-09-08", Tokens: 120},
		LocalPreviousDailyUsage: &domain.DailyUsageBucket{StartDate: "2026-09-07", Tokens: 100},
		LocalDailyCost:          &domain.TokenCostEstimate{Status: "complete", TotalMicroUSD: 157110000},
		LocalTokenUpdatedAt:     &now,
	}
	service := &Service{home: test.TempDir()}
	service.mergeLocalTokens(&usage, now)
	if usage.LocalDailyUsage != nil || usage.LocalPreviousDailyUsage != nil || usage.LocalDailyCost != nil || usage.LocalTokenUpdatedAt != nil {
		test.Fatalf("unreadable local source must not return old usage or a fabricated zero: %+v", usage)
	}
}

func TestLocalTokensEmptyDayThenFirstUsageWithoutAccount(test *testing.T) {
	home := test.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		test.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 11, 38, 56, 0, time.FixedZone("UTC+8", 8*60*60))
	service := &Service{home: home}
	empty, _ := service.readCodex(context.Background(), now, false, false)
	if empty.LocalDailyUsage == nil || empty.LocalDailyUsage.StartDate != "2026-09-09" || empty.LocalDailyUsage.Tokens != 0 || empty.LocalDailyUsage.Breakdown == nil {
		test.Fatalf("an existing empty source is zero, not unavailable: %+v", empty)
	}
	content := `{"timestamp":"2026-09-09T03:39:00Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":10,"output_tokens":5,"total_tokens":35}}}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "today.jsonl"), []byte(content), 0o600); err != nil {
		test.Fatal(err)
	}
	active, _ := service.readCodex(context.Background(), now.Add(11*time.Second), true, false)
	if active.LocalDailyUsage == nil || active.LocalDailyUsage.Tokens != 35 || active.LocalDailyUsage.Breakdown == nil || active.LocalDailyUsage.Breakdown.RegularInputTokens != 20 {
		test.Fatalf("first activity must replace the zero bucket: %+v", active)
	}
}
