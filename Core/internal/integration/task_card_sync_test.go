package integration

import (
	"context"
	"strings"
	"testing"
)

type recordingTaskCardPatcher struct {
	messageID string
	card      string
}

func (patcher *recordingTaskCardPatcher) PatchCard(_ context.Context, messageID, card string) error {
	patcher.messageID = messageID
	patcher.card = card
	return nil
}

func TestSyncTaskLinkCardPatchesReleasedCardAndClearsPendingMarker(t *testing.T) {
	store := NewTaskLinkStore(t.TempDir())
	link, err := store.Upsert("thread-sync", "Sync me", "Project", "me")
	if err != nil {
		t.Fatal(err)
	}
	link, err = store.Update(link.TaskKey, func(value *TaskLink) { value.RootMessageID = "message-card" })
	if err != nil {
		t.Fatal(err)
	}
	link, err = store.Release(link.TaskKey)
	if err != nil {
		t.Fatal(err)
	}
	patcher := &recordingTaskCardPatcher{}
	if err := SyncTaskLinkCard(context.Background(), store, patcher, link, ""); err != nil {
		t.Fatal(err)
	}
	if patcher.messageID != "message-card" || !strings.Contains(patcher.card, "连接已解除") || strings.Contains(patcher.card, "task_link_release") {
		t.Fatalf("unexpected released card patch: id=%q card=%s", patcher.messageID, patcher.card)
	}
	stored, found, err := store.FindAnyByTaskKey(link.TaskKey)
	if err != nil || !found {
		t.Fatalf("released link missing after sync: found=%v err=%v", found, err)
	}
	if TaskLinkCardSyncPending(stored) {
		t.Fatal("successful card patch did not clear pending marker")
	}
}

func TestTaskLinkCardMessageIDFallsBackToLatestRecordedCard(t *testing.T) {
	link := TaskLink{MessageIDs: []string{"first", "latest"}}
	if got := TaskLinkCardMessageID(link); got != "latest" {
		t.Fatalf("unexpected fallback message id: %q", got)
	}
}
