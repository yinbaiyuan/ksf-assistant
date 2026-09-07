package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBoundMessageRequiresRealConfirmedInputAndPersistsOutcome(t *testing.T) {
	for _, mode := range []string{"success", "missing-operation", "awaiting-confirmation", "changed-input", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			settings := enableCapabilityWrites(t, root)
			settings.Outbound.Enabled, settings.Outbound.DryRun = true, false
			if err := NewSettingsStore(root).Save(settings); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"request-id": "OUT-bound", "target-type": "open_id", "target-id": "ou_fixture", "format": "text", "text": "fixture", "source": "test"}
			operations := NewOperationService(root, NewCapabilityPolicyStore(root), nil)
			definition, _ := CapabilityByID("im.sdk.message.send")
			view, challenge, err := operations.Prepare(context.Background(), definition, input, "test")
			if err != nil {
				t.Fatal(err)
			}
			if mode != "awaiting-confirmation" {
				if _, err := operations.Confirm(context.Background(), view.ID, challenge); err != nil {
					t.Fatal(err)
				}
			}
			operationID := view.ID
			switch mode {
			case "missing-operation":
				operationID = "OP-20260905120000-FFFFFFFF"
			case "changed-input":
				input["text"] = "changed"
			case "disabled":
				settings.Outbound.Enabled = false
			case "dry-run":
				settings.Outbound.DryRun = true
			}
			if err := NewSettingsStore(root).Save(settings); err != nil {
				t.Fatal(err)
			}
			calls := 0
			result, err := operations.ExecuteBoundMessage(context.Background(), operationID, definition.ID, input, func(ctx context.Context) (map[string]any, error) {
				if err := ValidateMessageExecution(ctx, definition.ID, input); err != nil {
					t.Fatal(err)
				}
				calls++
				if mode == "timeout" {
					return nil, context.DeadlineExceeded
				}
				return map[string]any{"messageID": "om_fixture"}, nil
			})
			if mode == "success" {
				if err != nil || result.Status != string(OperationSucceeded) || calls != 1 {
					t.Fatalf("result=%#v calls=%d err=%v", result, calls, err)
				}
			} else if mode == "dry-run" {
				if err != nil || result.Status != "dry_run" || calls != 0 {
					t.Fatalf("result=%#v calls=%d err=%v", result, calls, err)
				}
			} else if mode == "timeout" {
				if err == nil || result.Status != string(OperationOutcomeUnknown) || calls != 1 {
					t.Fatalf("result=%#v calls=%d err=%v", result, calls, err)
				}
			} else if err == nil || calls != 0 {
				t.Fatalf("unauthorized callback: %d %v", calls, err)
			}
		})
	}
}

func TestDirectExecutorRejectsInputChangedAfterClaim(t *testing.T) {
	root := t.TempDir()
	input := map[string]any{"request-id": "OUT-bound", "target-type": "open_id", "target-id": "ou_fixture", "format": "text", "text": "fixture", "source": "test"}
	ctx, operationID := reviewRunningBoundary(t, root, "im.sdk.message.send", input)
	changed := map[string]any{}
	for key, value := range input {
		changed[key] = value
	}
	changed["text"] = "not authorized"
	sender := &reviewConcurrentSender{}
	_, err := (UnifiedCapabilityExecutor{DataRoot: root, Sender: sender}).ExecuteWithOptions(ctx, "im.sdk.message.send", changed, CapabilityExecutionOptions{OperationID: operationID})
	if !errors.Is(err, ErrOperationRequestMismatch) || sender.calls.Load() != 0 {
		t.Fatalf("err=%v sends=%d", err, sender.calls.Load())
	}
}

func TestWorkV4MigratesV3PendingConservativelyAndPreservesOperation(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "bound"}[authorized], func(t *testing.T) {
			root := t.TempDir()
			enableCapabilityWrites(t, root)
			service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
			prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "fixture"}, "test")
			if err != nil {
				t.Fatal(err)
			}
			repo := service.actionbox.repository
			heads, err := repo.pendingHeads()
			if err != nil || len(heads) != 1 {
				t.Fatalf("%v %v", heads, err)
			}
			item := heads[0]
			item.SchemaVersion, item.OperationID, item.ExecutionPhase = 3, "", ""
			if !authorized {
				var request ActionRequest
				_ = json.Unmarshal(item.Request, &request)
				request.OperationID = ""
				item.Request, _ = json.Marshal(request)
			}
			if err := writeWorkJSON(repo.path("pending", item.ID), item); err != nil {
				t.Fatal(err)
			}
			heads, err = repo.pendingHeads()
			if err != nil {
				t.Fatal(err)
			}
			if !authorized {
				if len(heads) != 0 {
					t.Fatal("unbound v3 pending replayed")
				}
				return
			}
			if len(heads) != 1 || heads[0].SchemaVersion != 4 || heads[0].OperationID != prepared.Operation.ID {
				t.Fatalf("migration=%#v", heads)
			}
			claimed, found, err := repo.claim()
			if err != nil || !found {
				t.Fatalf("claim %v %v", found, err)
			}
			if claimed.ExecutionPhase != "queue_claimed" {
				t.Fatal(claimed.ExecutionPhase)
			}
			if _, err := repo.recoverRunning(100); err != nil {
				t.Fatal(err)
			}
			var terminal WorkItemV4
			if _, err := readWorkJSON(repo.path("terminal", item.ID), &terminal); err != nil {
				t.Fatal(err)
			}
			if terminal.OperationID != prepared.Operation.ID || terminal.ExecutionPhase != "terminal" || len(terminal.Request) > 0 {
				t.Fatalf("terminal=%#v", terminal)
			}
			view, err := service.Status(prepared.Operation.ID)
			if err != nil || view.Status != OperationOutcomeUnknown {
				t.Fatalf("operation=%#v %v", view, err)
			}
		})
	}
}

func TestWorkIndexUnwritableCannotStrandClaimOrFinish(t *testing.T) {
	repo := newWorkRepository(t.TempDir(), "outbox")
	if err := repo.ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repo.indexPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := repo.enqueue("OUT-projection", map[string]any{"id": "OUT-projection"}, "key", "go-sdk", "standard", "never", time.Now()); err != nil {
		t.Fatal(err)
	}
	item, found, err := repo.claim()
	if err != nil || !found {
		t.Fatalf("claim=%v %v", found, err)
	}
	if err := repo.finish(item, map[string]any{"id": item.ID, "status": "sent"}, ""); err != nil {
		t.Fatal(err)
	}
	var result OutboxResult
	if found, err := repo.findResult(item.ID, &result); err != nil || !found || result.Status != "sent" {
		t.Fatalf("result=%#v %v", result, err)
	}
}

func TestRecoveryPreservesOperationSuccessAfterQueuePersistenceGap(t *testing.T) {
	root := t.TempDir()
	enableCapabilityWrites(t, root)
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	prepared, err := service.Prepare(context.Background(), "im.chat.create", map[string]any{"name": "fixture"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	item, found, err := service.actionbox.repository.claim()
	if err != nil || !found {
		t.Fatalf("%v %v", found, err)
	}
	record, err := service.operations.Request(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.operations.ClaimExecution(record.ID, record.CapabilityID, record.Input); err != nil {
		t.Fatal(err)
	}
	if _, err := service.operations.CompleteWithResult(record.ID, map[string]any{"receipt": "created"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverInterrupted(1000); err != nil {
		t.Fatal(err)
	}
	var result ActionResult
	if found, err := service.actionbox.repository.findResult(item.ID, &result); err != nil || !found || result.Status != string(OperationSucceeded) || result.OperationID != record.ID {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestV3MigrationPreservesVersionedRawBackupsBeforeCompletion(t *testing.T) {
	repo := newWorkRepository(t.TempDir(), "outbox")
	if err := repo.ensure(); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, state := range []string{"pending", "running", "terminal"} {
		item := WorkItemV4{SchemaVersion: 3, Kind: "outbox", ID: "OUT-" + state, State: state, CreatedAt: time.Now(), Request: json.RawMessage(`{"id":"fixture"}`)}
		if state == "terminal" {
			item.Request = nil
			item.Result = json.RawMessage(`{"status":"sent"}`)
		}
		if err := writeWorkJSON(repo.path(state, item.ID), item); err != nil {
			t.Fatal(err)
		}
		before[state], _ = os.ReadFile(repo.path(state, item.ID))
	}
	if err := repo.migrateV3(); err != nil {
		t.Fatal(err)
	}
	for state, original := range before {
		backup := filepath.Join(repo.root(), "backups", "schema-v3", state, "OUT-"+state+".json")
		data, err := os.ReadFile(backup)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("backup %s changed: %v", state, err)
		}
		if _, err := os.Stat(repo.path("terminal", "OUT-"+state)); err != nil {
			t.Fatal(err)
		}
	}
	var marker map[string]any
	if missing, err := readPrivateJSON(filepath.Join(repo.root(), "migration-v3-complete.json"), &marker); err != nil || missing || marker["migrated"] != float64(3) {
		t.Fatalf("marker=%#v %v", marker, err)
	}
	if err := repo.migrateV3(); err != nil {
		t.Fatal(err)
	}
}

func TestV3MigrationBackupFailureLeavesOriginalAndNoCompleteMarker(t *testing.T) {
	repo := newWorkRepository(t.TempDir(), "outbox")
	if err := repo.ensure(); err != nil {
		t.Fatal(err)
	}
	item := WorkItemV4{SchemaVersion: 3, Kind: "outbox", ID: "OUT-running", State: "running", Request: json.RawMessage(`{"id":"OUT-running"}`)}
	if err := writeWorkJSON(repo.path("running", item.ID), item); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.root(), "backups"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.migrateV3(); err == nil {
		t.Fatal("migration ignored backup failure")
	}
	var after WorkItemV4
	if _, err := readWorkJSON(repo.path("running", item.ID), &after); err != nil || after.SchemaVersion != 3 {
		t.Fatalf("source changed=%#v %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(repo.root(), "migration-v3-complete.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completion marker exists")
	}
}
