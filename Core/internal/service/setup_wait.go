package service

import (
	"context"
	"time"

	"ksfassistant/core/internal/domain"
)

func waitForSetupSnapshot(parent context.Context, expected string, timeout time.Duration, fetch func(context.Context) (domain.FeishuSnapshot, error)) (domain.FeishuSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return domain.FeishuSnapshot{}, err
		}
		snapshot, err := fetch(ctx)
		if ctx.Err() != nil {
			return domain.FeishuSnapshot{}, ctx.Err()
		}
		if err == nil && snapshot.Availability == expected {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return domain.FeishuSnapshot{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
