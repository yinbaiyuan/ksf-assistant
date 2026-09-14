package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type countFailPatch struct{ calls int }

func (p *countFailPatch) PatchCard(context.Context, string, string) error {
	p.calls++
	return errors.New("unbound Feishu message")
}
func TestPermanentCardFailureStopsBackgroundRetries(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("thread", "title", "", "me")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) { l.RootMessageID = "card"; l.SetExtraValue("cardSyncPending", true) })
	p := &countFailPatch{}
	_ = SyncTaskLinkCard(context.Background(), s, p, l, "")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) {
		l.Detail = "new text"
		l.SetExtraValue("cardSyncPending", true)
	})
	_ = SyncTaskLinkCard(context.Background(), s, p, l, "")
	if p.calls != 1 {
		t.Fatalf("permanent failure retried after content changed: %d", p.calls)
	}
}

func TestCardBackoffSurvivesNewContentAndSuccessSkipsUnchanged(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("thread", "title", "", "me")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) { l.RootMessageID = "card" })
	calls := 0
	fail := true
	p := updatingCardPatcher(func(context.Context, string, string) error {
		calls++
		if fail {
			return errors.New("network timeout")
		}
		return nil
	})
	_ = SyncTaskLinkCard(context.Background(), s, p, l, "")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) { l.Detail = "new text"; l.SetExtraValue("cardSyncPending", true) })
	_ = SyncTaskLinkCard(context.Background(), s, p, l, "")
	if calls != 1 {
		t.Fatal("new content bypassed backoff")
	}
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) {
		var st CardSyncState
		l.ExtraValue("cardSync", &st)
		st.NextAttemptAt = time.Time{}
		l.SetExtraValue("cardSync", st)
	})
	fail = false
	if err := SyncTaskLinkCard(context.Background(), s, p, l, ""); err != nil {
		t.Fatal(err)
	}
	if err := SyncTaskLinkCard(context.Background(), s, p, l, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("unchanged card was patched")
	}
}

func TestNewCardVersionSupersedesUncertainOutcome(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("thread", "title", "", "me")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) {
		l.RootMessageID = "card"
		l.Detail = "old text"
		l.SetExtraValue("cardSyncPending", true)
	})
	calls := 0
	p := updatingCardPatcher(func(context.Context, string, string) error {
		calls++
		if calls == 1 {
			return errors.New("outcome_unknown")
		}
		return nil
	})
	if err := SyncTaskLinkCard(context.Background(), s, p, l, ""); err == nil {
		t.Fatal("uncertain card update unexpectedly succeeded")
	}
	l, _, _ = s.FindByID(l.ID)
	var uncertain CardSyncState
	l.ExtraValue("cardSync", &uncertain)
	if uncertain.State != "waiting_retry" {
		t.Fatalf("uncertain replacement was not scheduled: %#v", uncertain)
	}
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) {
		l.Detail = "new text"
		l.SetExtraValue("cardSyncPending", true)
		var st CardSyncState
		l.ExtraValue("cardSync", &st)
		st.NextAttemptAt = time.Time{}
		l.SetExtraValue("cardSync", st)
	})
	if !TaskLinkCardSyncPending(l) {
		t.Fatal("new card version did not supersede the uncertain version")
	}
	if err := SyncTaskLinkCard(context.Background(), s, p, l, ""); err != nil {
		t.Fatal(err)
	}
	l, _, _ = s.FindByID(l.ID)
	var synced CardSyncState
	l.ExtraValue("cardSync", &synced)
	if calls != 2 || synced.State != "synced" || TaskLinkCardSyncPending(l) {
		t.Fatalf("new card version did not converge: calls=%d sync=%#v", calls, synced)
	}
}

func TestUnknownCardReplacementRetriesWithoutNewContent(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("thread", "title", "", "me")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) { l.RootMessageID = "card" })
	calls := 0
	p := updatingCardPatcher(func(context.Context, string, string) error {
		calls++
		if calls == 1 {
			return errors.New("outcome_unknown")
		}
		return nil
	})
	_ = SyncTaskLinkCard(context.Background(), s, p, l, "")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) {
		var st CardSyncState
		l.ExtraValue("cardSync", &st)
		st.NextAttemptAt = time.Time{}
		l.SetExtraValue("cardSync", st)
	})
	if !TaskLinkCardSyncPending(l) {
		t.Fatal("unchanged content cannot recover")
	}
	if err := SyncTaskLinkCard(context.Background(), s, p, l, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestLegacyUnknownCardCanRecoverButBudgetIsPreserved(t *testing.T) {
	l := TaskLink{}
	l.SetExtraValue("cardSync", CardSyncState{State: "needs_review", ErrorCode: "outcome_unknown", Attempts: 1, FirstFailureAt: time.Now()})
	l.SetExtraValue("cardSyncPending", false)
	if !TaskLinkCardSyncPending(l) {
		t.Fatal("legacy quarantine cannot recover")
	}
	l.SetExtraValue("cardSync", CardSyncState{State: "needs_review", ErrorCode: "outcome_unknown", Attempts: 20, FirstFailureAt: time.Now()})
	if TaskLinkCardSyncPending(l) {
		t.Fatal("retry budget reset by migration")
	}
}

func TestNewTurnDoesNotInheritPreviousProgress(t *testing.T) {
	l := TaskLink{}
	l.SetExtraString("progressTurnId", "old")
	l.SetExtraValue("progressSegments", []taskProgressSegment{{ID: "old-answer", Text: "old"}})
	got := desktopProjectionProgressSegments(l, desktopTaskProjection{TurnID: "new", TurnState: "running", ProgressSegments: []taskProgressSegment{{ID: "new-answer", Text: "new"}}})
	if len(got) != 1 || got[0].Text != "new" {
		t.Fatal("previous turn leaked into new card", got)
	}
}

func TestConcurrentSyncReloadsLatestAndNeverWritesStaleCardLast(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("thread", "title", "", "me")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) { l.RootMessageID = "card"; l.Detail = "old text" })
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 2)
	var cards []string
	p := updatingCardPatcher(func(_ context.Context, _ string, card string) error {
		cards = append(cards, card)
		if len(cards) == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	go func() { done <- SyncTaskLinkCard(context.Background(), s, p, l, "") }()
	<-entered
	_, err := s.UpdateByID(l.ID, func(l *TaskLink) { l.Detail = "latest text"; l.SetExtraValue("cardSyncPending", true) })
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- SyncTaskLinkCard(context.Background(), s, p, l, "") }()
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if len(cards) != 2 || !strings.Contains(cards[1], "latest text") {
		t.Fatal("stale card won")
	}
}

type retryAfterPatcher struct{}

func (retryAfterPatcher) PatchCard(context.Context, string, string) error { return delayedFailure{} }

type delayedFailure struct{}

func (delayedFailure) Error() string             { return "rate limited" }
func (delayedFailure) RetryDelay() time.Duration { return 10 * time.Minute }
func TestCardHonorsLongerServerRetryDelay(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("thread", "title", "", "me")
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) { l.RootMessageID = "card" })
	_ = SyncTaskLinkCard(context.Background(), s, retryAfterPatcher{}, l, "")
	l, _, _ = s.FindByID(l.ID)
	var st CardSyncState
	l.ExtraValue("cardSync", &st)
	if time.Until(st.NextAttemptAt) < 9*time.Minute {
		t.Fatal("server delay shortened")
	}
}

func TestReleasedLinkWithoutDeliveredCardDoesNotStayPending(t *testing.T) {
	s := NewTaskLinkStore(t.TempDir())
	l, _ := s.Upsert("never-sent", "title", "", "me")
	l, _ = s.ReleaseByID(l.ID)
	l, _ = s.UpdateByID(l.ID, func(l *TaskLink) { l.SetExtraValue("cardSyncPending", true) })
	p := &countFailPatch{}
	if err := SyncTaskLinkCard(context.Background(), s, p, l, ""); err != nil {
		t.Fatal(err)
	}
	l, _, _ = s.FindByID(l.ID)
	if TaskLinkCardSyncPending(l) || p.calls != 0 {
		t.Fatal("undelivered released link still scheduled")
	}
}
