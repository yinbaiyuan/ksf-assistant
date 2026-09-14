package integration

import (
	"context"
	"errors"
	"testing"
)

type archiveTestCore struct {
	fakeCorePort
	ids   []string
	err   error
	calls int
	stops int
}

func (c *archiveTestCore) ArchivedThreadIDs(context.Context) ([]string, error) {
	c.calls++
	return c.ids, c.err
}
func (c *archiveTestCore) InterruptTurn(context.Context, string, string, string, string) error {
	c.stops++
	return nil
}

func TestMaintenanceReleasesExplicitlyArchivedLinks(t *testing.T) {
	c := &archiveTestCore{ids: []string{"archived"}}
	r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, c)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	old, _ := r.links.Upsert("archived", "old", "", "me")
	live, _ := r.links.Upsert("live", "live", "", "me")
	r.runMaintenanceCycle(context.Background())
	old, _, _ = r.links.FindByID(old.ID)
	live, _, _ = r.links.FindByID(live.ID)
	if old.LinkState != "released" || live.LinkState != "active" {
		t.Fatalf("archive=%s live=%s", old.LinkState, live.LinkState)
	}
	if c.stops != 0 {
		t.Fatal("archiving must not stop Codex")
	}
	c.ids = nil
	r.runMaintenanceCycle(context.Background())
	old, _, _ = r.links.FindByID(old.ID)
	if old.LinkState != "released" {
		t.Fatal("unarchive reconnected without user intent")
	}
}

func TestArchiveReadFailureNeverReleasesLinks(t *testing.T) {
	c := &archiveTestCore{ids: []string{"thread"}, err: errors.New("offline")}
	r, _ := NewRuntime(t.TempDir(), &fakeFeishuPort{}, c)
	defer r.Close()
	l, _ := r.links.Upsert("thread", "title", "", "me")
	r.runMaintenanceCycle(context.Background())
	l, _, _ = r.links.FindByID(l.ID)
	if l.LinkState != "active" {
		t.Fatal("failed archive query released link")
	}
}

func TestFreshThreadListReconcilesMissingConnections(t *testing.T) {
	for _, tc := range []struct {
		name      string
		live      []string
		archived  []string
		err       error
		wantCalls int
		wantState string
	}{
		{"present", []string{"thread"}, nil, nil, 0, "active"},
		{"archived", nil, []string{"thread"}, nil, 1, "released"},
		{"missing alone", nil, nil, nil, 1, "active"},
		{"failed catalog", nil, []string{"thread"}, errors.New("offline"), 1, "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &archiveTestCore{ids: tc.archived, err: tc.err}
			r, err := NewRuntime(t.TempDir(), &fakeFeishuPort{}, c)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			link, _ := r.links.Upsert("thread", "title", "", "me")
			r.ReconcileFreshThreadList(context.Background(), tc.live)
			link, _, _ = r.links.FindByID(link.ID)
			if c.calls != tc.wantCalls || link.LinkState != tc.wantState {
				t.Fatalf("calls=%d state=%s", c.calls, link.LinkState)
			}
		})
	}
}
