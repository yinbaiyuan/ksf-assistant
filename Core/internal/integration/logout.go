package integration

import (
	"context"
	"errors"
	"ksfassistant/core/internal/capabilitypolicy"
	"ksfassistant/core/internal/privatestore"
	"strings"
	"time"
)

var ErrLogoutCardsPending = errors.New("logout_cards_pending")
var ErrLogoutInProgress = errors.New("feishu_logout_in_progress")

// Admission spans each control operation. Logout drains existing work before
// closing connections, and retains exclusive admission until identity is revoked.
func (runtime *Runtime) admitOperation(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.disconnecting.Load() {
		return nil, ErrLogoutInProgress
	}
	runtime.admissionMu.RLock()
	if runtime.disconnecting.Load() {
		runtime.admissionMu.RUnlock()
		return nil, ErrLogoutInProgress
	}
	if err := capabilitypolicy.CheckSession(runtime.dataRoot); err != nil {
		runtime.admissionMu.RUnlock()
		return nil, err
	}
	return runtime.admissionMu.RUnlock, nil
}

func (runtime *Runtime) DisconnectForLogout(ctx context.Context, revoke func() error) error {
	if !runtime.disconnecting.CompareAndSwap(false, true) {
		return ErrLogoutCardsPending
	}
	defer runtime.disconnecting.Store(false)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !runtime.admissionMu.TryLock() {
		select {
		case <-ctx.Done():
			return errors.Join(ErrLogoutCardsPending, ctx.Err())
		case <-ticker.C:
		}
	}
	defer runtime.admissionMu.Unlock()
	if ctx.Err() != nil {
		return errors.Join(ErrLogoutCardsPending, ctx.Err())
	}
	var selected []TaskLink
	// Record the whole set before any PATCH; a restart can resume these exact
	// disconnected cards without touching unrelated historical failures.
	err := privatestore.WithFileLock(runtime.links.path+".lock", func() error {
		file, err := runtime.links.Load()
		if err != nil {
			return err
		}
		for i := range file.Links {
			l := &file.Links[i]
			if l.LinkState == "active" {
				releaseTaskLink(l)
				l.SetExtraValue("logoutDisconnectPending", true)
				l.UpdatedAt = time.Now().UTC()
			}
			var pending bool
			if l.ExtraValue("logoutDisconnectPending", &pending) && pending {
				selected = append(selected, *l)
			}
		}
		if len(selected) == 0 {
			return nil
		}
		return runtime.links.saveUnlocked(file)
	})
	if err != nil {
		return errors.Join(ErrLogoutCardsPending, err)
	}
	for _, link := range selected {
		runtime.watchMu.Lock()
		for key, cancel := range runtime.watchers {
			if key == "desktop:"+link.TaskKey || strings.HasPrefix(key, "turn:"+link.TaskKey+":") {
				cancel()
			}
		}
		runtime.watchMu.Unlock()
	}
	// Card appearance is best effort: local control is already disconnected.
	// Bound this phase so a stalled PATCH cannot consume the logout deadline.
	syncCtx, cancelSync := context.WithTimeout(ctx, 5*time.Second)
	defer cancelSync()
	for _, link := range selected {
		syncErr := SyncTaskLinkCard(syncCtx, runtime.links, runtime.messages, link, "")
		_, err := runtime.links.UpdateByID(link.ID, func(l *TaskLink) {
			var state CardSyncState
			l.ExtraValue("cardSync", &state)
			if syncErr != nil || state.State != "synced" {
				state.State = "needs_review"
				if state.ErrorCode == "" {
					state.ErrorCode = "logout_card_update_unconfirmed"
				}
				state.NextAttemptAt = time.Time{}
				l.SetExtraValue("cardSync", state)
				l.SetExtraValue("cardSyncPending", false)
			}
			l.SetExtraValue("logoutDisconnectPending", false)
		})
		// Failure to persist diagnostics must not prevent revocation either.
		if err != nil {
			runtime.setHealth("task card reconciliation failed", err)
		}
	}
	if ctx.Err() != nil {
		return errors.Join(ErrLogoutCardsPending, ctx.Err())
	}
	return revoke()
}
