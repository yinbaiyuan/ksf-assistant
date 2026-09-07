package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsV2MigrationSealsLegacyQueuedWritesWithoutReplay(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []string{"actionbox", "outbox"} {
		repo := newWorkRepository(root, kind)
		if err := repo.enqueue("fixture", map[string]any{"dryRun": true}, "fixture", "cli", "write", "none", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	store := NewSettingsStore(root)
	legacy := DefaultSettings()
	legacy.Version = 1
	legacy.Group.Enabled = false
	legacy.Outbound = DryRunSwitch{Enabled: true, DryRun: true}
	if err := writePrivateJSON(store.path, legacy); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		settings, err := store.Load()
		if err != nil || settings.Version != 2 || settings.Group.Enabled {
			t.Fatalf("migration changed group scope: %+v %v", settings, err)
		}
		for _, kind := range []string{"actionbox", "outbox"} {
			repo := newWorkRepository(root, kind)
			entries, err := os.ReadDir(repo.stateDir("pending"))
			if err != nil || len(entries) != 0 {
				t.Fatal("pending legacy write survived", err)
			}
			var item WorkItemV4
			if _, err := readWorkJSON(repo.path("terminal", "fixture"), &item); err != nil || item.RetryClass != "manual_review" || item.ExecutionPhase != "legacy_configuration_review" {
				t.Fatalf("lost review record: %+v %v", item, err)
			}
		}
	}
	sender := &recordingSender{}
	if err := NewOutbox(root).Process(context.Background(), sender, false); err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 0 {
		t.Fatal("migration replayed a write")
	}
	data, _ := os.ReadFile(filepath.Join(root, SettingsFilename))
	var object map[string]any
	_ = json.Unmarshal(data, &object)
	for _, key := range []string{"outbound", "actionbox", "directory", "groupDirectory"} {
		if _, ok := object[key]; ok {
			t.Fatal("retired field persisted", key)
		}
	}
}
