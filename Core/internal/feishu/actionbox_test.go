package feishu

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestActionboxDryRunPersistsTerminalState(t *testing.T) {
	root := t.TempDir()
	box := NewActionbox(root)
	id, _ := NewActionID()
	definition, _ := CapabilityByID("im.message.reply")
	request := ActionRequest{ID: id, Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: map[string]any{"message-id": "om_test", "text": "hello"}, ExplicitAuthorization: true, DryRun: true, Source: "test", CreatedAt: time.Now().UTC()}
	if err := box.Submit(request); err != nil {
		t.Fatal(err)
	}
	if err := box.Process(context.Background(), CapabilityExecutor{}); err != nil {
		t.Fatal(err)
	}
	result, found, err := box.FindResult(id)
	if err != nil {
		t.Fatal(err)
	}
	if !found || result.Status != "dry_run" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if err := box.Process(context.Background(), CapabilityExecutor{}); err != nil {
		t.Fatal(err)
	}
	state := queueState{}
	if _, err := readPrivateJSON(box.statePath(), &state); err != nil {
		t.Fatal(err)
	}
	if state.ProcessedLineCount != 1 || len(state.ProcessedIDs) != 1 {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestQueueStatePreservesNodeLastTrigger(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":2,"processedLineCount":0,"processedIds":{},"lastProcessedAt":"","lastError":"","lastTrigger":{"source":"timer"},"wake":{"enabled":true,"host":"127.0.0.1","configuredPort":0,"actualPort":null}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var state queueState
	missing, err := readPrivateJSON(path, &state)
	if err != nil || missing || state.LastTrigger == nil {
		t.Fatalf("Node state was not accepted: missing=%v err=%v state=%#v", missing, err, state)
	}
}

func TestActionboxRejectsMissingHighImpactConfirmation(t *testing.T) {
	box := NewActionbox(t.TempDir())
	definition, ok := CapabilityByID("sheets.range.move")
	if !ok {
		t.Fatal("missing capability")
	}
	if definition.Risk != "high-impact-write" {
		t.Skip("fixture capability risk changed")
	}
	err := box.Submit(ActionRequest{ID: "ACT-test", Type: "feishu_capability", Domain: "capability", Action: "execute", CapabilityID: definition.ID, Identity: definition.Identity, Input: map[string]any{}, ExplicitAuthorization: true})
	if err == nil {
		t.Fatal("high-impact request accepted without confirmation")
	}
}

func TestActionboxConcurrentSubmitKeepsEveryJSONLRecord(t *testing.T) {
	root := t.TempDir()
	const total = 40
	var wait sync.WaitGroup
	for index := 0; index < total; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			box := NewActionbox(root)
			request := ActionRequest{
				ID: "ACT-CONCURRENT-" + time.Unix(int64(index), 0).UTC().Format("150405"), Type: "feishu_capability",
				Domain: "capability", Action: "execute", CapabilityID: "im.chat.create", Identity: "user",
				Input: map[string]any{"name": "concurrent test"}, ExplicitAuthorization: true,
				Source: "test", CreatedAt: time.Now().UTC(),
			}
			if err := box.Submit(request); err != nil {
				t.Errorf("submit %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	file, err := os.Open(NewActionbox(root).queuePath())
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != total {
		t.Fatalf("queue has %d records, want %d", count, total)
	}
}
