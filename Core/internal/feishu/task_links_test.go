package feishu

import (
	"encoding/json"
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
