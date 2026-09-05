package feishu

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskLinkStorePreservesNodeV2Fields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "task-links-v1.json")
	data := `{"protocol":"codex-feishu-task-link-v1","schemaVersion":2,"updatedAt":"2026-09-04T00:00:00Z","links":[{"id":"LINK-1","taskKey":"abc","threadId":"thread-1","title":"existing","targetAlias":"me","linkState":"active","turnState":"plan_ready","turnOwner":"desktop","actionRequired":"none","createdAt":"2026-09-04T00:00:00Z","updatedAt":"2026-09-04T00:00:00Z","expiresAt":"2026-09-05T00:00:00Z","pendingPlanRevision":"0123456789abcdef0123","inputCapture":{"chatId":"c","operatorId":"u","messageId":"m","expiresAt":"2026-09-04T01:00:00Z"}}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewTaskLinkStore(root)
	file, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	file.Links[0].Title = "updated"
	if err := store.Save(file); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(stored, &raw); err != nil {
		t.Fatal(err)
	}
	link := raw["links"].([]any)[0].(map[string]any)
	if link["pendingPlanRevision"] != "0123456789abcdef0123" || link["inputCapture"] == nil {
		t.Fatalf("Node fields were lost: %#v", link)
	}
}

func TestPublicLinksDeduplicatesLegacyTaskKeys(t *testing.T) {
	now := time.Now().UTC()
	links := []TaskLink{
		{TaskKey: "same", LinkState: "released", UpdatedAt: now.Add(time.Minute)},
		{TaskKey: "same", LinkState: "active", ExpiresAt: now.Add(time.Hour), UpdatedAt: now},
	}
	public := PublicLinks(links)
	if len(public) != 1 || public[0].LinkState != "active" {
		t.Fatalf("unexpected public links: %#v", public)
	}
}

func TestPublicTaskLinkUsesOnlyCanonicalStateFields(t *testing.T) {
	now := time.Now().UTC()
	links := PublicLinks([]TaskLink{{TaskKey: "canonical", LinkState: "active", TurnState: "idle", TurnOwner: "none", ExpiresAt: now.Add(time.Hour), UpdatedAt: now}})
	encoded, err := json.Marshal(links[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["state"]; exists {
		t.Fatalf("legacy duplicate state leaked into the Go domain projection: %s", encoded)
	}
}

func TestFindAnyByTaskKeyReturnsLatestReleasedLink(t *testing.T) {
	root := t.TempDir()
	store := NewTaskLinkStore(root)
	now := time.Now().UTC()
	file := TaskLinkFile{Protocol: TaskLinkProtocol, SchemaVersion: TaskLinkSchema, Links: []TaskLink{
		{TaskKey: "same", LinkState: "released", UpdatedAt: now.Add(-time.Minute)},
		{TaskKey: "same", LinkState: "released", UpdatedAt: now},
	}}
	if err := store.Save(file); err != nil {
		t.Fatal(err)
	}
	link, found, err := store.FindAnyByTaskKey("same")
	if err != nil || !found {
		t.Fatalf("FindAnyByTaskKey() = found %v, err %v", found, err)
	}
	if !link.UpdatedAt.Equal(now) {
		t.Fatalf("returned stale link: %s", link.UpdatedAt)
	}
}

func TestReleaseUsesSharedTransitionAndMarksCardSyncPending(t *testing.T) {
	store := NewTaskLinkStore(t.TempDir())
	link, err := store.Upsert("thread-release", "Release me", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	link, err = store.Update(link.TaskKey, func(value *TaskLink) {
		value.RootMessageID = "message-card"
		value.TurnState = "completed"
		value.TurnOwner = "bridge"
	})
	if err != nil {
		t.Fatal(err)
	}
	link, err = store.Release(link.TaskKey)
	if err != nil {
		t.Fatal(err)
	}
	if link.LinkState != "released" || link.TurnState != "idle" || link.TurnOwner != "none" || link.Phase != "已断开" {
		t.Fatalf("unexpected release transition: %#v", link)
	}
	if !TaskLinkCardSyncPending(link) {
		t.Fatal("linked card was not marked for durable synchronization")
	}
}

func TestReconnectUsesNewCardDeliveryIdentity(t *testing.T) {
	store := NewTaskLinkStore(t.TempDir())
	first, err := store.Upsert("thread-reconnect", "Reconnect me", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	firstKey, err := TaskLinkCardIdempotencyKey(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Release(first.TaskKey); err != nil {
		t.Fatal(err)
	}

	second, err := store.Upsert("thread-reconnect", "Reconnect me", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := TaskLinkCardIdempotencyKey(second)
	if err != nil {
		t.Fatal(err)
	}

	if first.TaskKey != second.TaskKey {
		t.Fatalf("task identity changed across reconnect: %q != %q", first.TaskKey, second.TaskKey)
	}
	if first.ID == second.ID {
		t.Fatalf("connection record identity was reused: %q", first.ID)
	}
	if firstKey == secondKey {
		t.Fatalf("card delivery identity was reused: %q", firstKey)
	}
	if second.RootMessageID != "" {
		t.Fatalf("new connection inherited the previous card: %q", second.RootMessageID)
	}
}

func TestTaskLinkCardIdempotencyKeyIsStableForRetry(t *testing.T) {
	link := TaskLink{ID: "LINK-1234", TaskKey: "stable-task"}
	first, err := TaskLinkCardIdempotencyKey(link)
	if err != nil {
		t.Fatal(err)
	}
	second, err := TaskLinkCardIdempotencyKey(link)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("retry identity changed: %q != %q", first, second)
	}
	if _, err := TaskLinkCardIdempotencyKey(TaskLink{}); err == nil {
		t.Fatal("missing connection record id was accepted")
	}
}

func TestFindByIDDoesNotCollapseReconnectHistory(t *testing.T) {
	store := NewTaskLinkStore(t.TempDir())
	first, err := store.Upsert("thread-by-id", "Task", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Release(first.TaskKey); err != nil {
		t.Fatal(err)
	}
	second, err := store.Upsert("thread-by-id", "Task", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	found, ok, err := store.FindByID(first.ID)
	if err != nil || !ok || found.ID != first.ID || found.ID == second.ID {
		t.Fatalf("historical connection was not resolved exactly: %#v %v %v", found, ok, err)
	}
}

func TestTaskLinkCleanupKeepsActiveAndProtectedHistory(t *testing.T) {
	root := t.TempDir()
	store := NewTaskLinkStore(root)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	protected := TaskLink{ID: "protected", TaskKey: "protected", LinkState: "released", UpdatedAt: now.Add(-10 * 24 * time.Hour)}
	protected.SetExtraValue("cardSyncPending", true)
	file := TaskLinkFile{Protocol: TaskLinkProtocol, SchemaVersion: TaskLinkSchema, Links: []TaskLink{
		{ID: "active", TaskKey: "active", LinkState: "active", UpdatedAt: now.Add(-40 * 24 * time.Hour), ExpiresAt: now.Add(time.Hour)},
		{ID: "old", TaskKey: "old", LinkState: "released", UpdatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "recent", TaskKey: "recent", LinkState: "released", UpdatedAt: now.Add(-6 * 24 * time.Hour)},
		protected,
	}}
	if err := store.Save(file); err != nil {
		t.Fatal(err)
	}
	report, err := store.CleanupAt(now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 1 || report.Protected != 1 {
		t.Fatalf("unexpected cleanup report: %#v", report)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, link := range stored.Links {
		ids[link.ID] = true
	}
	if !ids["active"] || !ids["recent"] || !ids["protected"] || ids["old"] {
		t.Fatalf("unexpected retained links: %#v", ids)
	}
}

func TestTaskLinkCleanupCapsTerminalHistoryAndAbandonsStaleProtection(t *testing.T) {
	store := NewTaskLinkStore(t.TempDir())
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	links := make([]TaskLink, 0, 503)
	for index := 0; index < 502; index++ {
		links = append(links, TaskLink{ID: fmt.Sprintf("terminal-%03d", index), TaskKey: fmt.Sprintf("task-%03d", index), LinkState: "released", UpdatedAt: now.Add(-time.Duration(502-index) * time.Minute)})
	}
	staleProtected := TaskLink{ID: "stale-protected", TaskKey: "stale-protected", LinkState: "released", UpdatedAt: now.Add(-31 * 24 * time.Hour)}
	staleProtected.SetExtraString("pendingCleanupDir", "/private/attachment")
	links = append(links, staleProtected)
	if err := store.Save(TaskLinkFile{Protocol: TaskLinkProtocol, SchemaVersion: TaskLinkSchema, Links: links}); err != nil {
		t.Fatal(err)
	}
	report, err := store.CleanupAt(now)
	if err != nil {
		t.Fatal(err)
	}
	if report.AbandonedProtected != 1 {
		t.Fatalf("stale protection was not abandoned: %#v", report)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Links) != 500 {
		t.Fatalf("terminal history was not capped: %d", len(stored.Links))
	}
	if stored.Links[0].ID != "terminal-002" {
		t.Fatalf("oldest removable records were not pruned first: %s", stored.Links[0].ID)
	}
}
