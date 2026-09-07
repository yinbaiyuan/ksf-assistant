package feishu

import (
	"errors"
	"os"
)

type QueueHealth struct {
	State     string `json:"state"`
	Revision  uint64 `json:"revision,omitempty"`
	Pending   int    `json:"pending"`
	Running   int    `json:"running,omitempty"`
	Terminal  int    `json:"terminal,omitempty"`
	Processed int    `json:"processed"`
	LastError string `json:"lastError,omitempty"`
}

// QueueHealthSnapshot returns a content-free queue projection suitable for
// dashboard and diagnostic output. It never exposes request or result bodies.
func QueueHealthSnapshot(dataRoot string, settings Settings) map[string]QueueHealth {
	result := map[string]QueueHealth{}
	for _, item := range []struct {
		name    string
		enabled bool
	}{
		{name: "outbox", enabled: true},
		{name: "actionbox", enabled: true},
	} {
		result[item.name] = readQueueHealth(dataRoot, item.name, item.enabled)
	}
	return result
}

func readQueueHealth(dataRoot, name string, enabled bool) QueueHealth {
	if !enabled {
		return QueueHealth{State: "disabled"}
	}
	repository := newWorkRepository(dataRoot, name)
	index, err := repository.readIndex()
	if err != nil {
		return QueueHealth{State: "degraded"}
	}
	if index.Revision == 0 {
		if _, legacyErr := os.Lstat(repositoryLegacyQueuePath(dataRoot, name)); legacyErr == nil {
			return QueueHealth{State: "degraded", LastError: "legacy_queue_migration_required"}
		} else if !errors.Is(legacyErr, os.ErrNotExist) {
			return QueueHealth{State: "degraded"}
		}
	}
	return QueueHealth{State: "ready", Revision: index.Revision, Pending: index.Pending, Running: index.Running, Terminal: index.Terminal, Processed: index.Processed, LastError: index.LastError}
}

func repositoryLegacyQueuePath(dataRoot, name string) string {
	switch name {
	case "outbox":
		return NewOutbox(dataRoot).queuePath()
	default:
		return NewActionbox(dataRoot).queuePath()
	}
}
