package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const WorkItemSchemaVersion = 4

type WorkItemV3 = WorkItemV4

type WorkItemV4 struct {
	OperationID    string          `json:"operationId,omitempty"`
	ExecutionPhase string          `json:"executionPhase"`
	SchemaVersion  int             `json:"schemaVersion"`
	Kind           string          `json:"kind"`
	ID             string          `json:"id"`
	State          string          `json:"state"`
	ConflictKey    string          `json:"conflictKey"`
	Backend        string          `json:"backend"`
	ExecutionClass string          `json:"executionClass"`
	RetryClass     string          `json:"retryClass"`
	Request        json.RawMessage `json:"request,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	StartedAt      *time.Time      `json:"startedAt,omitempty"`
	CompletedAt    *time.Time      `json:"completedAt,omitempty"`
}

type WorkIndexV3 = WorkIndexV4

type WorkIndexV4 struct {
	SchemaVersion int       `json:"schemaVersion"`
	Revision      uint64    `json:"revision"`
	Pending       int       `json:"pending"`
	Running       int       `json:"running"`
	Terminal      int       `json:"terminal"`
	Processed     int       `json:"processed"`
	LastError     string    `json:"lastError,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type workRepository struct {
	dataRoot string
	kind     string
}

var workSignals sync.Map     // data root -> chan struct{}
var workCompletions sync.Map // work completion key -> chan struct{}

func workSignal(dataRoot string) chan struct{} {
	value, _ := workSignals.LoadOrStore(filepath.Clean(dataRoot), make(chan struct{}, 1))
	return value.(chan struct{})
}

func signalWork(dataRoot string) {
	select {
	case workSignal(dataRoot) <- struct{}{}:
	default:
	}
}

func workCompletionKey(repo workRepository, id string) string {
	return filepath.Clean(repo.dataRoot) + "\x00" + repo.kind + "\x00" + id
}

func notifyWorkCompleted(repo workRepository, id string) {
	if value, ok := workCompletions.LoadAndDelete(workCompletionKey(repo, id)); ok {
		close(value.(chan struct{}))
	}
}

func newWorkRepository(dataRoot, kind string) workRepository {
	return workRepository{dataRoot: dataRoot, kind: kind}
}

func (repo workRepository) root() string {
	return filepath.Join(repo.dataRoot, "private-cache", "workbox-v3", repo.kind)
}
func (repo workRepository) stateDir(state string) string { return filepath.Join(repo.root(), state) }
func (repo workRepository) path(state, id string) string {
	return filepath.Join(repo.stateDir(state), id+".json")
}
func (repo workRepository) indexPath() string { return filepath.Join(repo.root(), "index.json") }
func (repo workRepository) lockPath() string  { return filepath.Join(repo.root(), ".repository.lock") }
func (repo workRepository) migrationMarker() string {
	return filepath.Join(repo.root(), "migration-v2-complete.json")
}

func (repo workRepository) ensure() error {
	if !contains([]string{"actionbox", "outbox"}, repo.kind) {
		return errors.New("unsupported workbox kind")
	}
	for _, state := range []string{"pending", "running", "terminal"} {
		if err := ensurePrivateDirectory(repo.stateDir(state)); err != nil {
			return err
		}
	}
	return nil
}

func (repo workRepository) enqueue(id string, request any, conflictKey, backend, executionClass, retryClass string, createdAt time.Time) error {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, `/\\`) {
		return errors.New("invalid work item id")
	}
	if err := repo.ensure(); err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(payload) > 4*1024*1024 {
		return errors.New("work item exceeds 4 MiB")
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if conflictKey == "" {
		conflictKey = repo.kind
	}
	if backend == "" {
		backend = "service"
	}
	if executionClass == "" {
		executionClass = "standard"
	}
	if retryClass == "" {
		retryClass = "never"
	}
	item := WorkItemV3{SchemaVersion: WorkItemSchemaVersion, Kind: repo.kind, ID: id, State: "pending", ConflictKey: conflictKey, Backend: backend, ExecutionClass: executionClass, RetryClass: retryClass, Request: payload, CreatedAt: createdAt.UTC()}
	bindWorkOperation(&item)
	item.ExecutionPhase = "queued"
	err = withProcessFileLock(repo.lockPath(), func() error {
		for _, state := range []string{"pending", "running", "terminal"} {
			if info, statErr := os.Lstat(repo.path(state, id)); statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return errors.New("unsafe work item path")
				}
				return errors.New("duplicate_id")
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		}
		if err := writeWorkJSON(repo.path("pending", id), item); err != nil {
			return err
		}
		_ = repo.updateIndexLocked(func(index *WorkIndexV3) { index.Pending++ })
		return nil
	})
	if err == nil {
		signalWork(repo.dataRoot)
	}
	return err
}

func (repo workRepository) claim() (WorkItemV3, bool, error) {
	heads, err := repo.pendingHeads()
	if err != nil || len(heads) == 0 {
		return WorkItemV3{}, false, err
	}
	return repo.claimID(heads[0].ID)
}

// pendingHeads returns the oldest pending item for every conflict key. The
// scheduler rotates these heads, preserving FIFO within a target while keeping
// independent targets eligible for fair dispatch.
func (repo workRepository) pendingHeads() ([]WorkItemV3, error) {
	if err := repo.ensure(); err != nil {
		return nil, err
	}
	items := []WorkItemV3{}
	err := withProcessFileLock(repo.lockPath(), func() error {
		entries, err := os.ReadDir(repo.stateDir("pending"))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			var item WorkItemV3
			missing, readErr := readWorkJSON(repo.path("pending", id), &item)
			if readErr != nil {
				return readErr
			}
			if !missing && (item.ID != id || item.Kind != repo.kind) {
				return errors.New("invalid work item binding")
			}
			if item.SchemaVersion == 3 {
				eligible, err := repo.upgradePendingLocked(&item)
				if err != nil {
					return err
				}
				if !eligible {
					continue
				}
			}
			if missing || item.SchemaVersion != WorkItemSchemaVersion || item.Kind != repo.kind || item.ID != id || item.State != "pending" {
				continue
			}
			if item.ConflictKey == "" {
				item.ConflictKey = repo.kind
			}
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		})
		heads := items[:0]
		seen := map[string]bool{}
		for _, item := range items {
			if !seen[item.ConflictKey] {
				seen[item.ConflictKey] = true
				heads = append(heads, item)
			}
		}
		items = heads
		return nil
	})
	return items, err
}

func (repo workRepository) claimID(id string) (WorkItemV3, bool, error) {
	if err := repo.ensure(); err != nil {
		return WorkItemV3{}, false, err
	}
	var claimed WorkItemV3
	found := false
	err := withProcessFileLock(repo.lockPath(), func() error {
		path := repo.path("pending", id)
		missing, readErr := readWorkJSON(path, &claimed)
		if missing {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		if _, err := repo.readIndex(); err != nil {
			if repairErr := repo.rebuildIndexLocked(); repairErr == nil {
				signalWork(repo.dataRoot)
				return err
			}
		}
		if claimed.SchemaVersion == 3 {
			eligible, err := repo.upgradePendingLocked(&claimed)
			if err != nil || !eligible {
				return err
			}
		}
		if claimed.SchemaVersion != WorkItemSchemaVersion || claimed.Kind != repo.kind || claimed.ID != id || claimed.State != "pending" {
			return errors.New("invalid pending work item")
		}
		now := time.Now().UTC()
		claimed.State, claimed.StartedAt, claimed.ExecutionPhase = "running", &now, "queue_claimed"
		bindWorkOperation(&claimed)
		runningPath := repo.path("running", id)
		if err := os.Rename(path, runningPath); err != nil {
			return err
		}
		if err := writeWorkJSON(runningPath, claimed); err != nil {
			_ = os.Rename(runningPath, path)
			return err
		}
		if err := repo.updateIndexLocked(func(index *WorkIndexV3) {
			if index.Pending > 0 {
				index.Pending--
			}
			index.Running++
		}); err != nil {
			_ = repo.rebuildIndexLocked()
		}
		found = true
		return nil
	})
	return claimed, found, err
}

func (repo workRepository) finish(item WorkItemV3, result any, lastError string) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(payload) > 4*1024*1024 {
		return errors.New("work result exceeds 4 MiB")
	}
	err = withProcessFileLock(repo.lockPath(), func() error {
		path := repo.path("running", item.ID)
		var current WorkItemV3
		missing, err := readWorkJSON(path, &current)
		if missing {
			return errors.New("running work item not found")
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		current.State, current.CompletedAt, current.ExecutionPhase = "terminal", &now, "terminal"
		bindWorkOperation(&current)
		current.Request = nil
		current.Result = payload
		if err := writeWorkJSON(path, current); err != nil {
			return err
		}
		if err := os.Rename(path, repo.path("terminal", item.ID)); err != nil {
			return err
		}
		_ = repo.updateIndexLocked(func(index *WorkIndexV3) {
			if index.Running > 0 {
				index.Running--
			}
			index.Terminal++
			index.Processed++
			index.LastError = safeCommandError(lastError)
		})
		return nil
	})
	if err == nil {
		notifyWorkCompleted(repo, item.ID)
		signalMaintenance(repo.dataRoot)
	}
	return err
}

func (repo workRepository) waitResult(ctx context.Context, id string, timeout time.Duration, target any) (bool, error) {
	if found, err := repo.findResult(id, target); found || err != nil {
		return found, err
	}
	key := workCompletionKey(repo, id)
	created := make(chan struct{})
	value, _ := workCompletions.LoadOrStore(key, created)
	completed := value.(chan struct{})
	if found, err := repo.findResult(id, target); found || err != nil {
		workCompletions.CompareAndDelete(key, completed)
		return found, err
	}
	var deadline <-chan time.Time
	var timeoutTimer *time.Timer
	if timeout > 0 {
		timeoutTimer = time.NewTimer(timeout)
		defer timeoutTimer.Stop()
		deadline = timeoutTimer.C
	}
	// Completion channels make same-process waits event-driven. The bounded
	// backoff also supports CLI clients whose worker lives in another process;
	// every check is a direct operation-file read, never a history scan.
	delay := 100 * time.Millisecond
	poll := time.NewTimer(delay)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			workCompletions.CompareAndDelete(key, completed)
			return false, ctx.Err()
		case <-deadline:
			workCompletions.CompareAndDelete(key, completed)
			return false, context.DeadlineExceeded
		case <-completed:
			return repo.findResult(id, target)
		case <-poll.C:
			if found, err := repo.findResult(id, target); found || err != nil {
				workCompletions.CompareAndDelete(key, completed)
				return found, err
			}
			if delay < 2*time.Second {
				delay *= 2
				if delay > 2*time.Second {
					delay = 2 * time.Second
				}
			}
			poll.Reset(delay)
		}
	}
}

// recoverRunning never replays a side-effecting operation. A process loss
// after claim makes the remote outcome unknowable, so recovery emits a
// content-free terminal result for manual reconciliation.
func (repo workRepository) recoverRunning(limit int) (int, error) {
	if limit <= 0 {
		limit = 1000
	}
	if err := repo.ensure(); err != nil {
		return 0, err
	}
	recovered := 0
	err := withProcessFileLock(repo.lockPath(), func() error {
		entries, err := os.ReadDir(repo.stateDir("running"))
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if recovered >= limit || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			path := repo.path("running", id)
			var item WorkItemV3
			missing, readErr := readWorkJSON(path, &item)
			if missing || readErr != nil || item.ID != id {
				continue
			}
			bindWorkOperation(&item)
			if err := repo.backupV3("running", item); err != nil {
				return err
			}
			item.SchemaVersion = WorkItemSchemaVersion
			if err := repo.reconcileWorkOperation(item); err != nil {
				return err
			}
			if item.State != "terminal" || len(item.Result) == 0 {
				now := time.Now().UTC()
				item.State, item.Request, item.CompletedAt, item.ExecutionPhase = "terminal", nil, &now, "terminal"
				item.Result, _ = json.Marshal(repo.recoveryResult(item, "worker_interrupted"))
				if err := writeWorkJSON(path, item); err != nil {
					return err
				}
			}
			if err := os.Rename(path, repo.path("terminal", id)); err != nil {
				return err
			}
			recovered++
		}
		if recovered > 0 {
			_ = repo.updateIndexLocked(func(index *WorkIndexV3) {
				index.Running -= recovered
				if index.Running < 0 {
					index.Running = 0
				}
				index.Terminal += recovered
				index.Processed += recovered
				index.LastError = "worker_interrupted"
			})
		}
		return nil
	})
	return recovered, err
}

func (repo workRepository) rebuildIndex() error {
	return withProcessFileLock(repo.lockPath(), repo.rebuildIndexLocked)
}

func (repo workRepository) markOutcomeUnknown(item WorkItemV3, errorCode string) error {
	if errorCode == "" {
		errorCode = "worker_interrupted"
	}
	if err := repo.reconcileWorkOperation(item); err != nil {
		return err
	}
	result := repo.recoveryResult(item, errorCode)
	return repo.finish(item, result, errorCode)
}

func (repo workRepository) findResult(id string, target any) (bool, error) {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, `/\\`) {
		return false, errors.New("invalid work item id")
	}
	var item WorkItemV3
	missing, err := readWorkJSON(repo.path("terminal", id), &item)
	if missing {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if (item.SchemaVersion != WorkItemSchemaVersion && item.SchemaVersion != 3) || item.Kind != repo.kind || item.ID != id || item.State != "terminal" {
		return false, errors.New("invalid terminal work item")
	}
	if len(item.Result) == 0 {
		return false, nil
	}
	return true, json.Unmarshal(item.Result, target)
}

func (repo workRepository) recentResults(limit int) ([]map[string]any, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	entries, err := os.ReadDir(repo.stateDir("terminal"))
	if errors.Is(err, os.ErrNotExist) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	result := []map[string]any{}
	for _, entry := range entries {
		if len(result) >= limit || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var item WorkItemV3
		missing, readErr := readWorkJSON(filepath.Join(repo.stateDir("terminal"), entry.Name()), &item)
		if missing || readErr != nil || len(item.Result) == 0 {
			continue
		}
		var value map[string]any
		if json.Unmarshal(item.Result, &value) == nil {
			result = append(result, redactPublicMap(value))
		}
	}
	return result, nil
}

func (repo workRepository) readIndex() (WorkIndexV3, error) {
	index := WorkIndexV3{SchemaVersion: WorkItemSchemaVersion}
	missing, err := readPrivateJSON(repo.indexPath(), &index)
	if missing {
		return index, nil
	}
	if err != nil {
		return WorkIndexV3{}, err
	}
	if index.SchemaVersion != WorkItemSchemaVersion {
		return WorkIndexV3{}, errors.New("unsupported work index")
	}
	return index, nil
}

func (repo workRepository) updateIndexLocked(update func(*WorkIndexV3)) error {
	index, err := repo.readIndex()
	if err != nil {
		return repo.rebuildIndexLocked()
	}
	update(&index)
	index.SchemaVersion = WorkItemSchemaVersion
	index.Revision++
	index.UpdatedAt = time.Now().UTC()
	if err := writePrivateJSON(repo.indexPath(), index); err != nil {
		return repo.rebuildIndexLocked()
	}
	return nil
}

// migrateLegacy imports a v2 append-only queue once. It is intentionally
// called before a producer or consumer uses v3; failure leaves the v2 files in
// place and prevents writes, so two storage engines can never consume together.
func (repo workRepository) migrateLegacy(queuePath, resultPath, statePath string) error {
	if err := repo.migrateLegacyV2(queuePath, resultPath, statePath); err != nil {
		return err
	}
	return repo.migrateV3()
}

func (repo workRepository) migrateLegacyV2(queuePath, resultPath, statePath string) error {
	if err := repo.ensure(); err != nil {
		return err
	}
	if _, err := os.Lstat(repo.migrationMarker()); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return withProcessFileLock(repo.lockPath(), func() error {
		if _, err := os.Lstat(repo.migrationMarker()); err == nil {
			return nil
		}
		results := map[string]json.RawMessage{}
		if err := scanLegacyJSONL(resultPath, func(raw json.RawMessage) error {
			var value struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &value) == nil && value.ID != "" {
				results[value.ID] = append(json.RawMessage(nil), raw...)
			}
			return nil
		}); err != nil {
			return err
		}
		legacyState := struct {
			ProcessedLineCount int `json:"processedLineCount"`
		}{}
		_, _ = readPrivateJSON(statePath, &legacyState)
		line := 0
		if err := scanLegacyJSONL(queuePath, func(raw json.RawMessage) error {
			line++
			var header struct {
				ID           string    `json:"id"`
				CreatedAt    time.Time `json:"createdAt"`
				CapabilityID string    `json:"capabilityId"`
			}
			if json.Unmarshal(raw, &header) != nil || header.ID == "" {
				return nil
			}
			if _, found := results[header.ID]; found {
				return nil
			}
			item := WorkItemV3{SchemaVersion: WorkItemSchemaVersion, Kind: repo.kind, ID: header.ID, State: "pending", ExecutionPhase: "queued", ConflictKey: repo.kind, Backend: "legacy-v2", ExecutionClass: "standard", RetryClass: "never", Request: append(json.RawMessage(nil), raw...), CreatedAt: header.CreatedAt}
			if item.CreatedAt.IsZero() {
				item.CreatedAt = time.Now().UTC()
			}
			bindWorkOperation(&item)
			unauthorized := repo.validateLegacyPending(item) != nil
			if line <= legacyState.ProcessedLineCount || unauthorized {
				now := time.Now().UTC()
				item.State, item.Request, item.CompletedAt = "terminal", nil, &now
				code := "legacy_result_missing"
				if line > legacyState.ProcessedLineCount {
					code = "legacy_authorization_unverified"
				}
				item.Result, _ = json.Marshal(map[string]any{"id": header.ID, "operationId": item.OperationID, "status": string(OperationOutcomeUnknown), "error": code, "completedAt": now})
				if err := repo.reconcileWorkOperation(item); err != nil {
					return err
				}
				return writeWorkJSON(repo.path("terminal", header.ID), item)
			}
			return writeWorkJSON(repo.path("pending", header.ID), item)
		}); err != nil {
			return err
		}
		for id, raw := range results {
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			completed := time.Now().UTC()
			if text, _ := value["completedAt"].(string); text != "" {
				if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
					completed = parsed
				}
			}
			item := WorkItemV3{SchemaVersion: WorkItemSchemaVersion, Kind: repo.kind, ID: id, State: "terminal", ConflictKey: repo.kind, Backend: "legacy-v2", ExecutionClass: "standard", RetryClass: "never", Result: raw, CreatedAt: completed, CompletedAt: &completed}
			if err := writeWorkJSON(repo.path("terminal", id), item); err != nil {
				return err
			}
		}
		_ = repo.rebuildIndexLocked()
		if err := repo.archiveLegacy(queuePath, resultPath, statePath); err != nil {
			return err
		}
		marker := map[string]any{"schemaVersion": WorkItemSchemaVersion, "completedAt": time.Now().UTC()}
		return writePrivateJSON(repo.migrationMarker(), marker)
	})
}

func (repo workRepository) archiveLegacy(paths ...string) error {
	root := filepath.Join(repo.dataRoot, "private-cache", "workbox-v3", "legacy-v2", repo.kind)
	if err := ensurePrivateDirectory(root); err != nil {
		return err
	}
	candidates := append([]string(nil), paths...)
	for _, path := range paths {
		candidates = append(candidates, path+".lock")
	}
	candidates = append(candidates,
		filepath.Join(repo.dataRoot, "logs", "."+repo.kind+"-process.lock"),
	)
	type legacyMove struct{ source, destination string }
	moves := []legacyMove{}
	seen := map[string]bool{}
	for _, path := range candidates {
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe legacy queue path")
		}
		destination := filepath.Join(root, filepath.Base(path))
		if _, err := os.Lstat(destination); err == nil {
			return errors.New("legacy archive destination already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		moves = append(moves, legacyMove{source: path, destination: destination})
	}
	moved := []legacyMove{}
	for _, item := range moves {
		if err := os.Rename(item.source, item.destination); err != nil {
			for index := len(moved) - 1; index >= 0; index-- {
				_ = os.Rename(moved[index].destination, moved[index].source)
			}
			return err
		}
		moved = append(moved, item)
		now := time.Now().UTC()
		_ = os.Chtimes(item.destination, now, now)
	}
	return nil
}

func (repo workRepository) rebuildIndexLocked() error {
	index := WorkIndexV3{SchemaVersion: WorkItemSchemaVersion, Revision: 1, UpdatedAt: time.Now().UTC()}
	for _, state := range []string{"pending", "running", "terminal"} {
		entries, err := os.ReadDir(repo.stateDir(state))
		if err != nil {
			return err
		}
		count := 0
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.HasSuffix(entry.Name(), ".json") {
				count++
			}
		}
		switch state {
		case "pending":
			index.Pending = count
		case "running":
			index.Running = count
		case "terminal":
			index.Terminal, index.Processed = count, count
		}
	}
	return writePrivateJSON(repo.indexPath(), index)
}

func scanLegacyJSONL(path string, accept func(json.RawMessage) error) error {
	info, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	if statErr != nil {
		return statErr
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe legacy queue path")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := append(json.RawMessage(nil), scanner.Bytes()...)
		if json.Valid(line) {
			if err := accept(line); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func workConflictKey(definition CapabilityDefinition, input map[string]any) string {
	parts := []string{definition.Domain}
	for _, name := range definition.ConflictKey {
		if name == "$capability" {
			return definition.ID
		}
		if value := strings.TrimSpace(fmt.Sprint(input[name])); value != "" && value != "<nil>" {
			parts = append(parts, name+":"+AuditFingerprint(value))
		}
	}
	if len(parts) == 1 {
		return definition.ID
	}
	return strings.Join(parts, "/")
}
