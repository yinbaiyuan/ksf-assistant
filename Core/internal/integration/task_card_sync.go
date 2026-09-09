package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"ksfassistant/core/internal/privatestore"
	"ksfassistant/core/internal/retrypolicy"
	"strings"
	"sync/atomic"
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
	if !link.ExtraValue("cardSyncPending", &pending) || !pending {
		return false
	}
	var state CardSyncState
	link.ExtraValue("cardSync", &state)
	if state.State == "needs_review" {
		fingerprint, err := taskLinkCardFingerprint(link, "")
		return err == nil && supersedesUncertainCardTarget(state, fingerprint)
	}
	if time.Now().Before(state.NextAttemptAt) {
		return false
	}
	return true
}

// SyncTaskLinkCard patches the bot-owned task card and only then clears the
// durable pending marker. The retry window covers short token/network races;
// the daemon reconciler handles failures that outlive this command.
func SyncTaskLinkCard(ctx context.Context, store TaskLinkStore, patcher TaskLinkCardPatcher, link TaskLink, messageID string) error {
	if patcher == nil {
		return errors.New("task card patcher is unavailable")
	}
	return privatestore.WithFileLock(store.path+"."+cardDigest(link.ID)+".sync.lock", func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if link.ID != "" {
			current, found, err := store.FindByID(link.ID)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("task link not found")
			}
			link = current
		}
		if messageID == "" {
			messageID = TaskLinkCardMessageID(link)
		}
		if messageID == "" {
			if effectiveTaskLinkState(link, time.Now()) != "active" {
				return saveCardSync(store, link.ID, CardSyncState{State: "synced", LinkState: effectiveTaskLinkState(link, time.Now()), ErrorCode: "no_card_created"}, false)
			}
			return nil
		}
		var state CardSyncState
		link.ExtraValue("cardSync", &state)
		card, err := TaskLinkCardJSON(link)
		if err != nil {
			return err
		}
		fingerprint := cardDigest(messageID + card)
		if state.State == "needs_review" {
			if !supersedesUncertainCardTarget(state, fingerprint) {
				cardSyncSkipped.Add(1)
				return errors.New("card_sync_needs_review")
			}
			state.State = "pending"
			state.Attempts = 0
			state.ErrorCode = ""
			state.FirstFailureAt = time.Time{}
			state.NextAttemptAt = time.Time{}
		}
		if time.Now().Before(state.NextAttemptAt) {
			cardSyncSkipped.Add(1)
			return errors.New("card_sync_backoff")
		}
		if state.SyncedVersion == fingerprint {
			cardSyncSkipped.Add(1)
			return saveCardSync(store, link.ID, state, false)
		}
		state.TargetVersion = fingerprint
		if state.FirstFailureAt.IsZero() == false && retrypolicy.Exhausted(state.Attempts, state.FirstFailureAt, time.Now()) {
			state.State = "needs_review"
			state.ErrorCode = "retry_budget_exhausted"
			return saveCardSync(store, link.ID, state, false)
		}
		state.State = "pending"
		if err := saveCardSync(store, link.ID, state, true); err != nil {
			return err
		}
		cardSyncAttempts.Add(1)
		err = patcher.PatchCard(ctx, messageID, card)
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		if err != nil {
			code, permanent := retrypolicy.Class(err)
			state.ErrorCode = code
			state.Attempts++
			if state.FirstFailureAt.IsZero() {
				state.FirstFailureAt = time.Now().UTC()
			}
			state.State = "waiting_retry"
			delay := retrypolicy.Delay(state.Attempts, link.ID)
			var retry interface{ RetryDelay() time.Duration }
			if errors.As(err, &retry) && retry.RetryDelay() > delay {
				delay = retry.RetryDelay()
			}
			state.NextAttemptAt = time.Now().UTC().Add(delay)
			if permanent || retrypolicy.Exhausted(state.Attempts, state.FirstFailureAt, time.Now()) {
				state.State = "needs_review"
				state.NextAttemptAt = time.Time{}
			}
			if saveErr := saveCardSync(store, link.ID, state, state.State == "waiting_retry"); saveErr != nil {
				return saveErr
			}
			return err
		}
		cardSyncUpdates.Add(1)
		state.State = "synced"
		state.LinkState = effectiveTaskLinkState(link, time.Now())
		state.SyncedVersion = fingerprint
		state.Attempts = 0
		state.ErrorCode = ""
		state.FirstFailureAt = time.Time{}
		state.NextAttemptAt = time.Time{}
		if link.ID == "" {
			return nil
		}
		_, err = store.UpdateByID(link.ID, func(value *TaskLink) {
			currentCard, e := TaskLinkCardJSON(*value)
			pending := e != nil || cardDigest(TaskLinkCardMessageID(*value)+currentCard) != fingerprint
			if pending {
				state.State = "pending"
			}
			value.SetExtraValue("cardSync", state)
			value.SetExtraValue("cardSyncPending", pending)
		})
		return err
	})
}

type CardSyncState struct {
	LinkState      string    `json:"linkState,omitempty"`
	State          string    `json:"state"`
	TargetVersion  string    `json:"targetVersion,omitempty"`
	SyncedVersion  string    `json:"syncedVersion,omitempty"`
	Attempts       int       `json:"attempts"`
	ErrorCode      string    `json:"errorCode,omitempty"`
	FirstFailureAt time.Time `json:"firstFailureAt,omitempty"`
	NextAttemptAt  time.Time `json:"nextAttemptAt,omitempty"`
}

var cardSyncAttempts, cardSyncUpdates, cardSyncSkipped atomic.Uint64

func CardSyncDiagnostics() map[string]uint64 {
	return map[string]uint64{"attempts": cardSyncAttempts.Load(), "updates": cardSyncUpdates.Load(), "skipped": cardSyncSkipped.Load()}
}
func cardDigest(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func taskLinkCardFingerprint(link TaskLink, messageID string) (string, error) {
	if messageID == "" {
		messageID = TaskLinkCardMessageID(link)
	}
	if messageID == "" {
		return "", nil
	}
	card, err := TaskLinkCardJSON(link)
	if err != nil {
		return "", err
	}
	return cardDigest(messageID + card), nil
}
func supersedesUncertainCardTarget(state CardSyncState, fingerprint string) bool {
	return state.ErrorCode == "outcome_unknown" && state.TargetVersion != "" && fingerprint != "" && fingerprint != state.TargetVersion
}
func saveCardSync(store TaskLinkStore, id string, state CardSyncState, pending bool) error {
	if id == "" {
		return nil
	}
	_, err := store.UpdateByID(id, func(l *TaskLink) { l.SetExtraValue("cardSync", state); l.SetExtraValue("cardSyncPending", pending) })
	return err
}
func RetryTaskLinkCard(ctx context.Context, store TaskLinkStore, patcher TaskLinkCardPatcher, id string) error {
	// The normal patch path verifies local binding and current permissions before
	// its first network effect. Reset the budget only for this explicit action.
	l, found, err := store.FindByID(id)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("task link not found")
	}
	err = privatestore.WithFileLock(store.path+"."+cardDigest(id)+".sync.lock", func() error { return saveCardSync(store, id, CardSyncState{State: "pending"}, true) })
	if err != nil {
		return err
	}
	return SyncTaskLinkCard(ctx, store, patcher, l, "")
}
