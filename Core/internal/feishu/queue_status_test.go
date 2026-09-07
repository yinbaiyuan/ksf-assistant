package feishu

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQueueHealthReportsEnabledPendingAndDisabledQueues(t *testing.T) {
	root := t.TempDir()
	repository := newWorkRepository(root, "outbox")
	for _, id := range []string{"OUT-1", "OUT-2"} {
		if err := repository.enqueue(id, map[string]any{"id": id}, id, "go-sdk", "standard", "never", testNow()); err != nil {
			t.Fatal(err)
		}
	}

	result := QueueHealthSnapshot(root, Settings{
		Outbound:  DryRunSwitch{Enabled: true},
		Actionbox: DryRunSwitch{Enabled: true},
	})
	if result["outbox"].State != "ready" || result["outbox"].Pending != 2 {
		t.Fatalf("outbox health = %#v", result["outbox"])
	}
	if _, present := result["docbox"]; present {
		t.Fatalf("docbox health = %#v", result["docbox"])
	}
}

func TestQueueHealthUsesAtomicIndexWithoutEnumeratingHistory(t *testing.T) {
	root := t.TempDir()
	repository := newWorkRepository(root, "actionbox")
	if err := repository.ensure(); err != nil {
		t.Fatal(err)
	}
	index := WorkIndexV3{SchemaVersion: WorkItemSchemaVersion, Revision: 42, Pending: 100_000, Running: 4, Terminal: 100_000, Processed: 100_000, UpdatedAt: testNow()}
	if err := writePrivateJSON(repository.indexPath(), index); err != nil {
		t.Fatal(err)
	}
	// A malformed historical entry would break an implementation that tried to
	// recount the repository. Queue health must consume only index.json.
	if err := os.WriteFile(filepath.Join(repository.stateDir("terminal"), "malformed.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	health := readQueueHealth(root, "actionbox", true)
	if health.State != "ready" || health.Revision != 42 || health.Pending != 100_000 || health.Terminal != 100_000 {
		t.Fatalf("queue health did not use its atomic index: %#v", health)
	}
}

func testNow() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) }
