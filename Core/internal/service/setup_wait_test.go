package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"ksfassistant/core/internal/domain"
)

func TestDesktopSetupWaitPassesBoundedDeadlineToFetch(t *testing.T) {
	started := time.Now()
	_, err := waitForSetupSnapshot(context.Background(), "ready", 20*time.Millisecond, func(ctx context.Context) (domain.FeishuSnapshot, error) {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Sub(started) > 100*time.Millisecond {
			t.Fatal("fetch escaped wait deadline")
		}
		<-ctx.Done()
		return domain.FeishuSnapshot{Availability: "ready"}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late ready snapshot accepted: %v", err)
	}
}

func TestDesktopSetupWaitRequiresAuthoritativeReadyNotDryRunOrDegraded(t *testing.T) {
	for _, availability := range []string{"dryRun", "degraded"} {
		_, err := waitForSetupSnapshot(context.Background(), "ready", 10*time.Millisecond, func(context.Context) (domain.FeishuSnapshot, error) {
			return domain.FeishuSnapshot{Availability: availability, ProcessRunning: true}, nil
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s bypassed readiness: %v", availability, err)
		}
	}
	result, err := waitForSetupSnapshot(context.Background(), "ready", time.Second, func(context.Context) (domain.FeishuSnapshot, error) {
		return domain.FeishuSnapshot{Availability: "ready"}, nil
	})
	if err != nil || result.Availability != "ready" {
		t.Fatalf("authoritative ready rejected: %+v %v", result, err)
	}
}

func TestDesktopSetupWaitPreservesShorterParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := waitForSetupSnapshot(ctx, "ready", 45*time.Second, func(ctx context.Context) (domain.FeishuSnapshot, error) {
		<-ctx.Done()
		return domain.FeishuSnapshot{}, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parent deadline ignored: %v", err)
	}
}
