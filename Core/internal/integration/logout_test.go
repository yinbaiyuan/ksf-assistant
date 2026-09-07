package integration

import (
	"context"
	"errors"
	"ksfassistant/core/internal/capabilitypolicy"
	"strings"
	"testing"
	"time"
)

type logoutFeishuPort struct {
	*fakeFeishuPort
	patch func(string, string) error
}

func (p *logoutFeishuPort) PatchCard(_ context.Context, id, card string) error {
	return p.patch(id, card)
}

func TestLogoutDisconnectsEveryCardBeforeRevokingAndNeverStopsCodex(t *testing.T) {
	root := t.TempDir()
	core := &fakeCorePort{}
	patches := 0
	port := &logoutFeishuPort{fakeFeishuPort: &fakeFeishuPort{}}
	r, err := NewRuntime(root, port, core)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, id := range []string{"one", "two"} {
		link, err := r.links.Upsert(id, id, "project", "me")
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.links.UpdateByID(link.ID, func(l *TaskLink) { l.RootMessageID = "card-" + id; l.TurnState = "running" })
		if err != nil {
			t.Fatal(err)
		}
	}
	port.patch = func(id, card string) error {
		patches++
		if capabilitypolicy.CheckSession(root) != nil {
			t.Fatal("authorization revoked before PATCH")
		}
		if !strings.Contains(card, "连接已解除") || strings.Contains(card, "task_link_release") {
			t.Fatal("card still actionable")
		}
		file, _ := r.links.Load()
		for _, l := range file.Links {
			if l.LinkState != "released" {
				t.Fatal("all links must close before first PATCH")
			}
		}
		if _, err := r.CreateTaskLink(context.Background(), CreateTaskLinkRequest{ThreadID: "racing"}); !errors.Is(err, ErrLogoutInProgress) {
			t.Fatal("new connection admitted during logout", err)
		}
		return nil
	}
	revoked := false
	err = r.DisconnectForLogout(context.Background(), func() error {
		if patches != 2 {
			t.Fatal("revoked too early")
		}
		revoked = true
		return capabilitypolicy.SignOut(root)
	})
	if err != nil || !revoked || core.calls.Load() != 0 {
		t.Fatal(err, revoked, core.calls.Load())
	}
	if r.CanCreateTaskLink() {
		t.Fatal("signed out runtime still ready")
	}
}

func TestLogoutCardFailureStillRevokesAndNeverReplaysAfterRestart(t *testing.T) {
	root := t.TempDir()
	port := &fakeFeishuPort{patchErr: errors.New("outcome_unknown")}
	r, err := NewRuntime(root, port, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	link, _ := r.links.Upsert("one", "One", "Project", "me")
	r.links.UpdateByID(link.ID, func(l *TaskLink) { l.RootMessageID = "card" })
	revoked := false
	if err := r.DisconnectForLogout(context.Background(), func() error { revoked = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Fatal("failed PATCH prevented logout")
	}
	saved, _, _ := r.links.FindByID(link.ID)
	var pending bool
	if saved.LinkState != "released" || !saved.ExtraValue("logoutDisconnectPending", &pending) || pending {
		t.Fatal("disconnect progress lost")
	}
	var syncState CardSyncState
	saved.ExtraValue("cardSync", &syncState)
	if syncState.State != "needs_review" {
		t.Fatal("unknown result must stop automatic retry")
	}
	r.Close()
	r, err = NewRuntime(root, port, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// An unresolved write must not be reset or replayed by a second logout.
	count := port.calls.Load()
	if err := r.DisconnectForLogout(context.Background(), func() error { revoked = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !revoked || port.calls.Load() != count {
		t.Fatal("unknown PATCH automatically replayed")
	}
	// Simulate a separately verified card repair. Logout now just finishes revocation.
	r.links.UpdateByID(link.ID, func(l *TaskLink) { l.SetExtraValue("logoutDisconnectPending", false) })
	if err := r.DisconnectForLogout(context.Background(), func() error { revoked = true; return nil }); err != nil || !revoked {
		t.Fatal(err)
	}
}

func TestLogoutWaitHonorsCancellationAndDoesNotRevokeInflightOperation(t *testing.T) {
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	done, err := r.admitOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	revoked := false
	err = r.DisconnectForLogout(ctx, func() error { revoked = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || revoked || r.disconnecting.Load() {
		t.Fatal("cancelled logout leaked admission or revoked", err)
	}
}
