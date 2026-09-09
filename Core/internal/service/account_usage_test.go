package service

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ksfassistant/core/internal/codex"
	"ksfassistant/core/internal/domain"
)

type accountFixture struct {
	UsedPercent int  `json:"usedPercent"`
	Tokens      int  `json:"tokens"`
	Fail        bool `json:"fail"`
	FailTokens  bool `json:"failTokens"`
}

func init() {
	path := os.Getenv("KSFA_ACCOUNT_FIXTURE")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	var account accountFixture
	if err != nil || json.Unmarshal(data, &account) != nil {
		os.Exit(2)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.ID == nil {
			continue
		}
		result := any(map[string]any{})
		switch request.Method {
		case "initialize":
		case "thread/list", "project/list":
			result = map[string]any{"data": []any{}}
		case "account/rateLimits/read":
			result = map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": account.UsedPercent}}}
		case "account/usage/read":
			result = map[string]any{"summary": map[string]any{"lifetimeTokens": account.Tokens}, "dailyUsageBuckets": []any{map[string]any{"startDate": "2026-09-09", "tokens": account.Tokens}}}
		default:
			os.Exit(3)
		}
		response := map[string]any{"id": *request.ID, "result": result}
		if (account.Fail && (request.Method == "account/rateLimits/read" || request.Method == "account/usage/read")) ||
			(account.FailTokens && request.Method == "account/usage/read") {
			response = map[string]any{"id": *request.ID, "error": map[string]any{"code": -32000, "message": "authentication required"}}
		}
		if json.NewEncoder(os.Stdout).Encode(response) != nil {
			os.Exit(4)
		}
	}
	os.Exit(0)
}

func newAccountFixture(test *testing.T) (*Service, func(accountFixture)) {
	test.Helper()
	home := test.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0o700); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(home, "account-fixture.json")
	test.Setenv("KSFA_ACCOUNT_FIXTURE", path)
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	client := &codex.Client{Executable: executable, Timeout: time.Second}
	test.Cleanup(func() { _ = client.Close() })
	return &Service{home: home, codex: client}, func(value accountFixture) {
		test.Helper()
		data, _ := json.Marshal(value)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			test.Fatal(err)
		}
	}
}

func TestAccountRefreshDoesNotReuseTaskConnectionAuthentication(test *testing.T) {
	service, login := newAccountFixture(test)
	now := time.Date(2026, 9, 9, 12, 22, 18, 0, time.UTC)
	login(accountFixture{UsedPercent: 20, Tokens: 300})
	old, _ := service.readCodex(context.Background(), now, false, false)
	if domain.HeadlineRemaining(*domain.GeneralBucket(old.Buckets)) == nil {
		test.Fatal("fixture did not load account quota")
	}
	login(accountFixture{UsedPercent: 0, Tokens: 1})
	current, _ := service.readCodex(context.Background(), now.Add(6*time.Minute), false, false)
	bucket := domain.GeneralBucket(current.Buckets)
	if bucket == nil || *domain.HeadlineRemaining(*bucket) != 100 {
		test.Fatalf("quota retained the old process account: %+v", current)
	}
	if current.TokenSummary == nil || current.TokenSummary.LifetimeTokens == nil || *current.TokenSummary.LifetimeTokens != 1 {
		test.Fatalf("server token activity mixed accounts: %+v", current.TokenSummary)
	}
	if current.LocalDailyUsage == nil || current.LocalDailyUsage.Tokens != 0 {
		test.Fatal("account switch discarded local usage")
	}
	oldConnection, err := service.codex.FetchRateLimits(context.Background())
	if err != nil || oldConnection.RateLimits.Primary.UsedPercent != 20 {
		test.Fatal("account refresh replaced the task connection")
	}
}

func TestForcedAccountRefreshBypassesIntervalAndHistoryReadsFreshAccount(test *testing.T) {
	service, login := newAccountFixture(test)
	now := time.Date(2026, 9, 9, 12, 22, 18, 0, time.UTC)
	login(accountFixture{UsedPercent: 20, Tokens: 300})
	service.readCodex(context.Background(), now, false, false)
	login(accountFixture{UsedPercent: 0, Tokens: 1})
	current, _ := service.readCodex(context.Background(), now.Add(time.Second), false, true)
	if bucket := domain.GeneralBucket(current.Buckets); bucket == nil || *domain.HeadlineRemaining(*bucket) != 100 {
		test.Fatal("forced refresh reused the previous account within the interval")
	}
	login(accountFixture{UsedPercent: 1, Tokens: 2})
	days, err := service.readServerTokenHistory(context.Background(), false)
	if err != nil || len(days) != 1 || days[0].Tokens != 2 {
		test.Fatalf("history reused the previous account: %+v, %v", days, err)
	}
	login(accountFixture{Fail: true})
	days, err = service.readServerTokenHistory(context.Background(), false)
	if err == nil || len(days) != 0 || len(service.lastUsage.Buckets) != 0 {
		test.Fatal("failed history refresh retained unverified account data")
	}
}

func TestAccountFailureDoesNotRetainUnverifiedQuotaOrServerHistory(test *testing.T) {
	service, login := newAccountFixture(test)
	now := time.Date(2026, 9, 9, 12, 22, 18, 0, time.UTC)
	login(accountFixture{UsedPercent: 20, Tokens: 300})
	service.readCodex(context.Background(), now, false, false)
	service.lastServerHistory = []domain.DailyUsageBucket{{StartDate: "2026-09-09", Tokens: 300}}
	service.lastServerHistoryAt = now
	login(accountFixture{Fail: true})
	_ = service.codex.Close()
	service.codex = &codex.Client{Executable: service.codex.Executable, Timeout: time.Second}
	test.Cleanup(func() { _ = service.codex.Close() })
	current, _ := service.readCodex(context.Background(), now.Add(31*time.Minute), false, false)
	if len(current.Buckets) != 0 || current.RateUpdatedAt != nil || current.TokenSummary != nil || len(current.DailyUsageBuckets) != 0 || len(service.lastServerHistory) != 0 {
		test.Fatalf("failed account refresh retained prior-account data: %+v", current)
	}
	if current.LocalDailyUsage == nil {
		test.Fatal("account failure discarded local usage")
	}
}

func TestNewAccountQuotaDoesNotRetainOldActivityWhenUsageEndpointFails(test *testing.T) {
	service, login := newAccountFixture(test)
	now := time.Date(2026, 9, 9, 12, 22, 18, 0, time.UTC)
	login(accountFixture{UsedPercent: 20, Tokens: 300})
	service.readCodex(context.Background(), now, false, false)
	login(accountFixture{UsedPercent: 0, FailTokens: true})
	current, _ := service.readCodex(context.Background(), now.Add(time.Second), false, true)
	bucket := domain.GeneralBucket(current.Buckets)
	if bucket == nil || *domain.HeadlineRemaining(*bucket) != 100 || current.Status != "available" {
		test.Fatal("a failed usage endpoint hid the new account's available quota")
	}
	if current.TokenSummary != nil || len(current.DailyUsageBuckets) != 0 || len(service.lastServerHistory) != 0 || current.TokenError == "" {
		test.Fatal("the new account inherited the old account's token activity")
	}
}
