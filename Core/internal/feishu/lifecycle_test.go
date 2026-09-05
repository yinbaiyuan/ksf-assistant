package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalInboundPayloadIsScrubbedImmediately(t *testing.T) {
	root := t.TempDir()
	box := NewInboundWorkbox(root)
	work, err := box.Enqueue("card.action.trigger", []byte(`{"event":{"answer":"private answer"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := box.RecordFailure(work, "submit_failed", 1, true, time.Time{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(box.root, work.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private answer") || strings.Contains(string(data), `"payload"`) {
		t.Fatalf("terminal payload survived: %s", data)
	}
	var receipt inboundWork
	if missing, err := readPrivateJSON(filepath.Join(box.root, work.ID+".json"), &receipt); err != nil || missing || receipt.PayloadHash == "" || receipt.Status != "failed" {
		t.Fatalf("receipt=%#v missing=%v err=%v", receipt, missing, err)
	}
}

func TestLifecycleProtectsUnknownWorkForNinetyDays(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	repo := newWorkRepository(root, "actionbox")
	if err := repo.enqueue("ACT-UNKNOWN", map[string]any{"id": "ACT-UNKNOWN"}, "target", "service", "standard", "never", now.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	item, found, err := repo.claim()
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	if err := repo.finish(item, map[string]any{"id": item.ID, "status": string(OperationOutcomeUnknown)}, "unknown"); err != nil {
		t.Fatal(err)
	}
	path := repo.path("terminal", item.ID)
	var stored WorkItemV3
	if missing, err := readPrivateJSON(path, &stored); err != nil || missing {
		t.Fatal(err)
	}
	completed := now.Add(-60 * 24 * time.Hour)
	stored.CompletedAt = &completed
	if err := writePrivateJSON(path, stored); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupDataAt(root, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("protected unknown work was removed early: %v", err)
	}
	completed = now.Add(-91 * 24 * time.Hour)
	stored.CompletedAt = &completed
	if err := writePrivateJSON(path, stored); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupDataAt(root, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired unknown work still exists: %v", err)
	}
}

func TestDiagnosticLogDoesNotStoreUnstructuredStderr(t *testing.T) {
	root := t.TempDir()
	raw := []byte("failed for ou_private_user using secret=very-private")
	diagnostic := ParseSupervisorDiagnostic(raw)
	if err := NewDiagnosticLog(root).Record(diagnostic); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "logs", "diagnostics", "feishu-child-0.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ou_private_user") || strings.Contains(string(data), "very-private") {
		t.Fatalf("stderr leaked: %s", data)
	}
	var stored SupervisorDiagnostic
	if err := json.Unmarshal(bytesTrimLine(data), &stored); err != nil || stored.Fingerprint == "" || stored.Length != len(raw) {
		t.Fatalf("diagnostic=%#v err=%v", stored, err)
	}
}

func TestLifecycleRemovesTerminalOperationAndItsPerRecordLock(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	operationRoot := filepath.Join(root, "operations")
	if err := os.MkdirAll(operationRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(operationRoot, "OP-20260101000000-AAAAAAAA.json")
	record := OperationRecord{Version: OperationRecordVersion, ID: "OP-20260101000000-AAAAAAAA", Status: OperationSucceeded, UpdatedAt: now.Add(-91 * 24 * time.Hour)}
	if err := writePrivateJSON(path, record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupDataAt(root, now); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".lock"} {
		if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
			t.Fatalf("retained operation artifact survived: %s (%v)", candidate, err)
		}
	}
}

func TestLifecycleRemovesAbandonedControlResultsButKeepsPendingRequests(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	inbox := NewControlInbox(root)
	if err := ensurePrivateDirectory(inbox.root); err != nil {
		t.Fatal(err)
	}
	id := "CTL-20260101000000-AAAAAAAA"
	resultPath := inbox.resultPath(id)
	if err := writePrivateJSON(resultPath, ControlResult{SchemaVersion: controlInboxSchema, ID: id, Status: "failed", ErrorClass: "control_failed", CompletedAt: now.Add(-31 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	pendingID := "CTL-20260101000000-BBBBBBBB"
	pendingPath := inbox.requestPath(pendingID)
	if err := writePrivateJSON(pendingPath, ControlRequest{SchemaVersion: controlInboxSchema, ID: pendingID, Operation: "taskLink.interrupt", TaskKey: "task", CreatedAt: now.Add(-31 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	summary, err := CleanupDataAt(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ControlResults != 1 {
		t.Fatalf("control result cleanup count = %d", summary.ControlResults)
	}
	if _, err := os.Lstat(resultPath); !os.IsNotExist(err) {
		t.Fatalf("abandoned control result survived: %v", err)
	}
	if _, err := os.Lstat(pendingPath); err != nil {
		t.Fatalf("active control request was removed: %v", err)
	}
}

func bytesTrimLine(value []byte) []byte { return []byte(strings.TrimSpace(string(value))) }
