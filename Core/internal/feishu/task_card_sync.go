package feishu

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type TaskLinkCardPatcher interface {
	PatchCard(context.Context, string, string) error
}

func TaskLinkCardMessageID(link TaskLink) string {
	if value := strings.TrimSpace(link.RootMessageID); value != "" {
		return value
	}
	for index := len(link.MessageIDs) - 1; index >= 0; index-- {
		if value := strings.TrimSpace(link.MessageIDs[index]); value != "" {
			return value
		}
	}
	return ""
}

func TaskLinkCardSyncPending(link TaskLink) bool {
	var pending bool
	return link.ExtraValue("cardSyncPending", &pending) && pending
}

// SyncTaskLinkCard patches the bot-owned task card and only then clears the
// durable pending marker. The retry window covers short token/network races;
// the daemon reconciler handles failures that outlive this command.
func SyncTaskLinkCard(ctx context.Context, store TaskLinkStore, patcher TaskLinkCardPatcher, link TaskLink, messageID string) error {
	if patcher == nil {
		return errors.New("task card patcher is unavailable")
	}
	if strings.TrimSpace(messageID) == "" {
		messageID = TaskLinkCardMessageID(link)
	}
	if messageID == "" {
		return nil
	}
	card, err := TaskLinkCardJSON(link)
	if err != nil {
		return err
	}
	delays := []time.Duration{0, 200 * time.Millisecond, 600 * time.Millisecond}
	var lastErr error
	for _, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		if err := patcher.PatchCard(ctx, messageID, card); err != nil {
			lastErr = err
			continue
		}
		if link.ID != "" {
			_, err = store.UpdateByID(link.ID, func(value *TaskLink) {
				value.SetExtraValue("cardSyncPending", nil)
			})
		}
		return err
	}
	return fmt.Errorf("sync task link card: %w", lastErr)
}
