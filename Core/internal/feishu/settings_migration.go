package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// v2 retires product feature gates. Migration never promotes legacy queued
// writes to live work; uncertain queued work is sealed for manual review.
func (store SettingsStore) migrateLegacy() error {
	return withProcessFileLock(store.path+".lock", func() error {
		settings := DefaultSettings()
		stored, err := readKnownStoreJSON(store.path, &settings)
		if err != nil {
			return err
		}
		if settings.Version != 1 {
			return nil
		}
		root := filepath.Dir(store.path)
		for _, kind := range []string{"actionbox", "outbox"} {
			repo := newWorkRepository(root, kind)
			if _, err := os.Stat(repo.root()); os.IsNotExist(err) {
				continue
			}
			if err := withProcessFileLock(repo.lockPath(), func() error {
				for _, state := range []string{"pending", "running"} {
					entries, err := os.ReadDir(repo.stateDir(state))
					if os.IsNotExist(err) {
						continue
					}
					if err != nil {
						return err
					}
					for _, entry := range entries {
						if filepath.Ext(entry.Name()) != ".json" {
							continue
						}
						path := filepath.Join(repo.stateDir(state), entry.Name())
						var item WorkItemV4
						if _, err := readWorkJSON(path, &item); err != nil {
							return err
						}
						now := time.Now().UTC()
						item.State = "terminal"
						item.RetryClass = "manual_review"
						item.ExecutionPhase = "legacy_configuration_review"
						item.CompletedAt = &now
						item.Result, _ = json.Marshal(map[string]any{"id": item.ID, "status": "failed", "error": "legacy_configuration_requires_review", "completedAt": now})
						if err := writeWorkJSON(repo.path("terminal", item.ID), item); err != nil {
							return err
						}
						if err := os.Remove(path); err != nil {
							return err
						}
					}
				}
				return repo.rebuildIndexLocked()
			}); err != nil {
				return err
			}
		}
		settings.Version = 2
		settings.Outbound = DryRunSwitch{Enabled: true}
		settings.Actionbox = DryRunSwitch{Enabled: true}
		settings.Directory = Switch{}
		settings.GroupDirectory = Switch{}
		for _, key := range []string{"outbound", "actionbox", "directory", "groupDirectory", "docbox"} {
			delete(stored, key)
		}
		return writeKnownStoreJSON(store.path, stored, settings)
	})
}
