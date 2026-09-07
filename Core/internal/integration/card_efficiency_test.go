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
	_ = SyncTaskLinkCard(context.Background(), s, p, l, "")
	if p.calls != 1 {
		t.Fatalf("permanent failure retried %d times", p.calls)
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
