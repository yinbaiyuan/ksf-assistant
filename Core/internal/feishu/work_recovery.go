package feishu

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maximumWorkJSONBytes = 8 << 20

func readWorkJSON(path string, target any) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumWorkJSONBytes {
		return false, errors.New("unsafe work file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return false, errors.New("insecure work permissions")
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maximumWorkJSONBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return false, errors.New("work file trailing data")
	}
	return false, nil
}

func writeWorkJSON(path string, item WorkItemV4) error {
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > maximumWorkJSONBytes {
		return errors.New("work envelope exceeds 8 MiB")
	}
	return writePrivateJSON(path, item)
}

func bindWorkOperation(item *WorkItemV4) {
	if item.OperationID != "" {
		return
	}
	var binding struct {
		OperationID string `json:"operationId"`
	}
	if len(item.Request) > 0 {
		_ = json.Unmarshal(item.Request, &binding)
	} else {
		_ = json.Unmarshal(item.Result, &binding)
	}
	item.OperationID = binding.OperationID
}

func (repo workRepository) setExecutionPhase(id, phase string) error {
	return withProcessFileLock(repo.lockPath(), func() error {
		var item WorkItemV4
		missing, err := readWorkJSON(repo.path("running", id), &item)
		if err != nil {
			return err
		}
		if missing || item.State != "running" {
			return errors.New("work_not_running")
		}
		item.ExecutionPhase = phase
		return writeWorkJSON(repo.path("running", id), item)
	})
}

func (repo workRepository) validateLegacyPending(item WorkItemV4) error {
	operations := NewOperationService(repo.dataRoot, NewCapabilityPolicyStore(repo.dataRoot), nil)
	switch repo.kind {
	case "actionbox":
		var request ActionRequest
		if err := json.Unmarshal(item.Request, &request); err != nil {
			return err
		}
		return operations.ValidateQueuedRequest(request.OperationID, request.CapabilityID, request.Input)
	case "outbox":
		var request OutboxRequest
		if err := json.Unmarshal(item.Request, &request); err != nil {
			return err
		}
		_, err := boundQueueInput(operations, request.OperationID, "im.sdk.message.send", outboxCapabilityInput(repo.dataRoot, request))
		return err
	case "docbox":
		var request DocumentRequest
		if err := json.Unmarshal(item.Request, &request); err != nil {
			return err
		}
		id, input := documentCapabilityInput(request)
		_, err := boundQueueInput(operations, request.OperationID, id, input)
		return err
	}
	return errors.New("unknown_queue")
}

func (repo workRepository) upgradePendingLocked(item *WorkItemV4) (bool, error) {
	if item.SchemaVersion != 3 {
		return true, nil
	}
	if err := repo.backupV3("pending", *item); err != nil {
		return false, err
	}
	if item.Kind != repo.kind || item.ID == "" || strings.ContainsAny(item.ID, `/\`) || item.State != "pending" {
		return false, errors.New("invalid legacy work item")
	}
	bindWorkOperation(item)
	item.SchemaVersion, item.ExecutionPhase = WorkItemSchemaVersion, "queued"
	if err := repo.validateLegacyPending(*item); err != nil {
		now := time.Now().UTC()
		item.State, item.ExecutionPhase, item.CompletedAt = "terminal", "terminal", &now
		item.Result, _ = json.Marshal(map[string]any{"id": item.ID, "operationId": item.OperationID, "status": string(OperationOutcomeUnknown), "error": "legacy_authorization_unverified", "completedAt": now})
		item.Request = nil
		if err := repo.reconcileWorkOperation(*item); err != nil {
			return false, err
		}
		if err := writeWorkJSON(repo.path("pending", item.ID), *item); err != nil {
			return false, err
		}
		if err := os.Rename(repo.path("pending", item.ID), repo.path("terminal", item.ID)); err != nil {
			return false, err
		}
		_ = repo.rebuildIndexLocked()
		return false, nil
	}
	operations := NewOperationService(repo.dataRoot, NewCapabilityPolicyStore(repo.dataRoot), nil)
	record, err := operations.load(item.OperationID)
	if err != nil {
		return false, err
	}
	definition, known := CapabilityByID(record.CapabilityID)
	if !known {
		return false, errors.New("unknown_legacy_capability")
	}
	item.Backend, item.ExecutionClass, item.RetryClass = definition.Backend, definition.ExecutionClass, definition.RetryClass
	item.ConflictKey = workConflictKey(definition, record.Input)
	return true, writeWorkJSON(repo.path("pending", item.ID), *item)
}

func (repo workRepository) backupV3(state string, item WorkItemV4) error {
	if item.SchemaVersion != 3 {
		return nil
	}
	if item.ID == "" || strings.ContainsAny(item.ID, `/\`) || !contains([]string{"pending", "running", "terminal"}, state) {
		return errors.New("invalid_v3_backup_binding")
	}
	source := repo.path(state, item.ID)
	var checked WorkItemV4
	if missing, err := readWorkJSON(source, &checked); err != nil || missing {
		return errors.Join(errors.New("v3_backup_source_unavailable"), err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	destination := filepath.Join(repo.root(), "backups", "schema-v3", state, item.ID+".json")
	var prior WorkItemV4
	if _, err := readWorkJSON(destination, &prior); err != nil {
		return err
	}
	if existing, err := os.ReadFile(destination); err == nil {
		if !bytes.Equal(existing, data) {
			return errors.New("v3_backup_conflict")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ensurePrivateDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".v3-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), destination); err != nil {
		return err
	}
	return securePrivatePath(destination, false)
}

func (repo workRepository) migrateV3() error {
	if err := repo.ensure(); err != nil {
		return err
	}
	marker := filepath.Join(repo.root(), "migration-v3-complete.json")
	var complete struct {
		SchemaVersion int       `json:"schemaVersion"`
		BackupVersion int       `json:"backupVersion"`
		Migrated      int       `json:"migrated"`
		CompletedAt   time.Time `json:"completedAt"`
	}
	if missing, err := readPrivateJSON(marker, &complete); err != nil {
		return err
	} else if !missing {
		if complete.SchemaVersion != 4 || complete.BackupVersion != 3 {
			return errors.New("invalid_v3_migration_marker")
		}
		return nil
	}
	return withProcessFileLock(repo.lockPath(), func() error {
		type migrationItem struct {
			state string
			item  WorkItemV4
		}
		items := []migrationItem{}
		for _, state := range []string{"pending", "running", "terminal"} {
			entries, err := os.ReadDir(repo.stateDir(state))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				var item WorkItemV4
				if _, err := readWorkJSON(filepath.Join(repo.stateDir(state), entry.Name()), &item); err != nil {
					return err
				}
				if item.ID != strings.TrimSuffix(entry.Name(), ".json") || item.Kind != repo.kind {
					return errors.New("invalid_v3_migration_item")
				}
				if state == "pending" && item.State != "pending" || state == "terminal" && item.State != "terminal" || state == "running" && !contains([]string{"pending", "running", "terminal"}, item.State) {
					return errors.New("invalid_v3_migration_state")
				}
				if item.SchemaVersion != 3 {
					if item.SchemaVersion != 4 {
						return errors.New("unsupported_work_migration_schema")
					}
					continue
				}
				if err := repo.backupV3(state, item); err != nil {
					return err
				}
				items = append(items, migrationItem{state, item})
			}
		}
		for _, entry := range items {
			item := entry.item
			if entry.state == "pending" {
				if _, err := repo.upgradePendingLocked(&item); err != nil {
					return err
				}
				continue
			}
			bindWorkOperation(&item)
			item.SchemaVersion, item.ExecutionPhase = 4, "terminal"
			if entry.state == "running" {
				if err := repo.reconcileWorkOperation(item); err != nil {
					return err
				}
				if item.State != "terminal" || len(item.Result) == 0 {
					now := time.Now().UTC()
					item.State, item.Request, item.CompletedAt = "terminal", nil, &now
					item.Result, _ = json.Marshal(repo.recoveryResult(item, "worker_interrupted"))
				}
			}
			if err := writeWorkJSON(repo.path(entry.state, item.ID), item); err != nil {
				return err
			}
			if entry.state == "running" {
				if err := os.Rename(repo.path("running", item.ID), repo.path("terminal", item.ID)); err != nil {
					return err
				}
			}
		}
		_ = repo.rebuildIndexLocked()
		backups := 0
		for _, state := range []string{"pending", "running", "terminal"} {
			entries, err := os.ReadDir(filepath.Join(repo.root(), "backups", "schema-v3", state))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
					backups++
				}
			}
		}
		complete.SchemaVersion, complete.BackupVersion, complete.Migrated, complete.CompletedAt = 4, 3, backups, time.Now().UTC()
		return writePrivateJSON(marker, complete)
	})
}

func (repo workRepository) reconcileWorkOperation(item WorkItemV4) error {
	bindWorkOperation(&item)
	if item.OperationID == "" {
		return nil
	}
	operations := NewOperationService(repo.dataRoot, NewCapabilityPolicyStore(repo.dataRoot), nil)
	record, err := operations.load(item.OperationID)
	if err != nil {
		if err.Error() == "operation_not_found" || err.Error() == "invalid_operation_id" {
			return nil
		}
		return err
	}
	if record.Status == OperationQueued || record.Status == OperationRunning || record.Status == OperationVerifying {
		_, err = operations.MarkOutcomeUnknown(item.OperationID, "worker_interrupted")
	}
	return err
}

func (repo workRepository) reconcileTerminalOperations() error {
	entries, err := os.ReadDir(repo.stateDir("terminal"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var item WorkItemV4
		if _, err := readWorkJSON(repo.path("terminal", strings.TrimSuffix(entry.Name(), ".json")), &item); err != nil {
			return err
		}
		if err := repo.reconcileWorkOperation(item); err != nil {
			return err
		}
	}
	return nil
}

func (repo workRepository) recoveryResult(item WorkItemV4, code string) map[string]any {
	result := map[string]any{"id": item.ID, "operationId": item.OperationID, "status": string(OperationOutcomeUnknown), "error": code, "completedAt": time.Now().UTC()}
	if item.OperationID == "" {
		return result
	}
	operations := NewOperationService(repo.dataRoot, NewCapabilityPolicyStore(repo.dataRoot), nil)
	record, err := operations.load(item.OperationID)
	if err != nil {
		return result
	}
	switch record.Status {
	case OperationSucceeded:
		result["status"], result["result"] = string(OperationSucceeded), record.Result
		delete(result, "error")
		if repo.kind != "actionbox" && record.Result != nil {
			if response, exists := record.Result["response"]; exists {
				data, _ := json.Marshal(response)
				_ = json.Unmarshal(data, &result)
				result["id"], result["operationId"] = item.ID, item.OperationID
			}
		}
	case OperationFailed, OperationCancelled, OperationExpired:
		result["status"], result["error"] = string(record.Status), record.LastError
	}
	return result
}
