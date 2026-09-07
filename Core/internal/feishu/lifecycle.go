package feishu

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	workRetentionAge        = 30 * 24 * time.Hour
	workRetentionCount      = 20_000
	operationRetentionAge   = 90 * 24 * time.Hour
	operationRetentionCount = 10_000
	auditRetentionAge       = 180 * 24 * time.Hour
	auditRetentionBytes     = int64(200 * 1024 * 1024)
	protectedOutcomeAge     = 90 * 24 * time.Hour
)

type LifecycleSummary struct {
	WorkItems       int `json:"workItems"`
	InboundReceipts int `json:"inboundReceipts"`
	ControlResults  int `json:"controlResults"`
	Events          int `json:"events"`
	Operations      int `json:"operations"`
	AuditFiles      int `json:"auditFiles"`
	LegacyFiles     int `json:"legacyFiles"`
}

var maintenanceSignals sync.Map

func maintenanceSignal(dataRoot string) chan struct{} {
	value, _ := maintenanceSignals.LoadOrStore(filepath.Clean(dataRoot), make(chan struct{}, 1))
	return value.(chan struct{})
}

func signalMaintenance(dataRoot string) {
	select {
	case maintenanceSignal(dataRoot) <- struct{}{}:
	default:
	}
}

// RunLifecycleMaintenance performs startup cleanup, daily cleanup and
// capacity-triggered cleanup. Capacity signals are coalesced to avoid turning
// retention into a per-request scan.
func RunLifecycleMaintenance(ctx context.Context, dataRoot string) {
	_, _ = CleanupDataAt(dataRoot, time.Now().UTC())
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()
	lastRun := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-daily.C:
			_, _ = CleanupDataAt(dataRoot, now.UTC())
			lastRun = now
		case <-maintenanceSignal(dataRoot):
			if time.Since(lastRun) < time.Minute {
				continue
			}
			_, _ = CleanupDataAt(dataRoot, time.Now().UTC())
			lastRun = time.Now()
		}
	}
}

func CleanupDataAt(dataRoot string, now time.Time) (LifecycleSummary, error) {
	var summary LifecycleSummary
	var combined error
	for _, kind := range []string{"actionbox", "outbox"} {
		count, err := cleanupWorkRepository(newWorkRepository(dataRoot, kind), now)
		summary.WorkItems += count
		combined = errors.Join(combined, err)
	}
	count, err := cleanupInboundReceipts(dataRoot, now)
	summary.InboundReceipts += count
	combined = errors.Join(combined, err)
	count, err = cleanupControlResults(dataRoot, now)
	summary.ControlResults += count
	combined = errors.Join(combined, err)
	count, err = cleanupEventRecords(dataRoot, now)
	summary.Events += count
	combined = errors.Join(combined, err)
	count, err = cleanupOperationRecords(dataRoot, now)
	summary.Operations += count
	combined = errors.Join(combined, err)
	count, err = cleanupAuditFiles(dataRoot, now)
	summary.AuditFiles += count
	combined = errors.Join(combined, err)
	count, err = cleanupLegacyWorkFiles(dataRoot, now)
	summary.LegacyFiles += count
	combined = errors.Join(combined, err)
	return summary, combined
}

func cleanupControlResults(dataRoot string, now time.Time) (int, error) {
	inbox := NewControlInbox(dataRoot)
	entries, err := os.ReadDir(inbox.root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	files := []retentionFile{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".result.json") {
			continue
		}
		path := filepath.Join(inbox.root, entry.Name())
		var result ControlResult
		missing, readErr := readPrivateJSON(path, &result)
		if missing || readErr != nil || result.SchemaVersion != controlInboxSchema || !controlIDPattern.MatchString(result.ID) || result.CompletedAt.IsZero() {
			continue
		}
		files = append(files, retentionFile{path: path, when: result.CompletedAt})
	}
	removed := 0
	err = withProcessFileLock(inbox.lockPath(), func() error {
		var removeErr error
		removed, removeErr = removeExpiredOrExcess(files, now, workRetentionAge, workRetentionCount)
		return removeErr
	})
	return removed, err
}

type retentionFile struct {
	path      string
	when      time.Time
	protected bool
	size      int64
}

func cleanupWorkRepository(repo workRepository, now time.Time) (int, error) {
	if err := repo.ensure(); err != nil {
		return 0, err
	}
	removed := 0
	err := withProcessFileLock(repo.lockPath(), func() error {
		entries, err := os.ReadDir(repo.stateDir("terminal"))
		if err != nil {
			return err
		}
		files := []retentionFile{}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			path := filepath.Join(repo.stateDir("terminal"), entry.Name())
			var item WorkItemV3
			missing, readErr := readPrivateJSON(path, &item)
			if missing || readErr != nil || item.State != "terminal" {
				continue
			}
			when := item.CreatedAt
			if item.CompletedAt != nil {
				when = *item.CompletedAt
			}
			status := terminalResultStatus(item.Result)
			files = append(files, retentionFile{path: path, when: when, protected: status == string(OperationOutcomeUnknown) || status == "manual_review"})
		}
		deleted := map[string]bool{}
		for _, file := range files {
			ageLimit := workRetentionAge
			if file.protected {
				ageLimit = protectedOutcomeAge
			}
			if now.Sub(file.when) >= ageLimit {
				if file.protected {
					_ = NewAuditLog(repo.dataRoot).Record("protected_work_abandoned", map[string]any{"kind": repo.kind, "item": AuditFingerprint(filepath.Base(file.path)), "status": "manual_review"})
				}
				if err := removePrivateRegular(file.path); err != nil {
					return err
				}
				deleted[file.path] = true
				removed++
			}
		}
		remaining := make([]retentionFile, 0, len(files)-removed)
		for _, file := range files {
			if !deleted[file.path] {
				remaining = append(remaining, file)
			}
		}
		sort.Slice(remaining, func(i, j int) bool { return remaining[i].when.Before(remaining[j].when) })
		for len(remaining) > workRetentionCount {
			index := -1
			for candidate, file := range remaining {
				if !file.protected || now.Sub(file.when) >= protectedOutcomeAge {
					index = candidate
					break
				}
			}
			if index < 0 {
				break
			}
			if err := removePrivateRegular(remaining[index].path); err != nil {
				return err
			}
			remaining = append(remaining[:index], remaining[index+1:]...)
			removed++
		}
		if removed > 0 {
			return repo.updateIndexLocked(func(index *WorkIndexV3) {
				index.Terminal -= removed
				if index.Terminal < 0 {
					index.Terminal = 0
				}
			})
		}
		return nil
	})
	return removed, err
}

func terminalResultStatus(raw json.RawMessage) string {
	var value struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Status
}

func cleanupInboundReceipts(dataRoot string, now time.Time) (int, error) {
	box := NewInboundWorkbox(dataRoot)
	if _, err := box.ScrubTerminal(); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(box.root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	files := []retentionFile{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(box.root, entry.Name())
		var work inboundWork
		missing, readErr := readPrivateJSON(path, &work)
		if missing || readErr != nil || (work.Status != "completed" && work.Status != "failed") {
			continue
		}
		when := work.CreatedAt
		if work.CompletedAt != nil {
			when = *work.CompletedAt
		}
		files = append(files, retentionFile{path: path, when: when})
	}
	return removeExpiredOrExcess(files, now, workRetentionAge, workRetentionCount)
}

func cleanupOperationRecords(dataRoot string, now time.Time) (int, error) {
	root := filepath.Join(dataRoot, "operations")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	files := []retentionFile{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		var record OperationRecord
		missing, readErr := readPrivateJSON(path, &record)
		if missing || readErr != nil {
			continue
		}
		terminal := record.Status == OperationSucceeded || record.Status == OperationFailed || record.Status == OperationExpired || record.Status == OperationCancelled
		protected := record.Status == OperationOutcomeUnknown || record.NextAction == "manual_review"
		if !terminal && !protected {
			continue
		}
		files = append(files, retentionFile{path: path, when: record.UpdatedAt, protected: protected})
	}
	removed := 0
	sort.Slice(files, func(i, j int) bool { return files[i].when.Before(files[j].when) })
	for _, file := range files {
		if now.Sub(file.when) < operationRetentionAge {
			continue
		}
		if file.protected {
			_ = NewAuditLog(dataRoot).Record("operation_manual_review_abandoned", map[string]any{"operation": AuditFingerprint(filepath.Base(file.path))})
		}
		if err := removeRetainedFile(file.path, true); err != nil {
			return removed, err
		}
		file.path = ""
		removed++
	}
	remaining := []retentionFile{}
	for _, file := range files {
		if file.path != "" {
			remaining = append(remaining, file)
		}
	}
	for len(remaining) > operationRetentionCount {
		index := -1
		for candidate, file := range remaining {
			if !file.protected || now.Sub(file.when) >= protectedOutcomeAge {
				index = candidate
				break
			}
		}
		if index < 0 {
			break
		}
		if err := removeRetainedFile(remaining[index].path, true); err != nil {
			return removed, err
		}
		remaining = append(remaining[:index], remaining[index+1:]...)
		removed++
	}
	return removed, nil
}

func cleanupEventRecords(dataRoot string, now time.Time) (int, error) {
	root := filepath.Join(dataRoot, "events")
	files, err := filepath.Glob(filepath.Join(root, "events-*.jsonl"))
	if err != nil {
		return 0, err
	}
	sort.Strings(files)
	removed := 0
	remaining := []string{}
	for _, path := range files {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if now.Sub(info.ModTime()) >= workRetentionAge {
			if err := removeRetainedFile(path, true); err != nil {
				return removed, err
			}
			removed++
			continue
		}
		remaining = append(remaining, path)
	}
	lineCounts := make([]int, len(remaining))
	total := 0
	for index, path := range remaining {
		count, countErr := jsonlLineCount(path)
		if countErr != nil {
			return removed, countErr
		}
		lineCounts[index] = count
		total += count
	}
	for index, path := range remaining {
		if total <= workRetentionCount {
			break
		}
		if total-lineCounts[index] >= workRetentionCount {
			if err := removeRetainedFile(path, true); err != nil {
				return removed, err
			}
			total -= lineCounts[index]
			removed++
			continue
		}
		drop := total - workRetentionCount
		if drop > 0 {
			if err := withProcessFileLock(path+".lock", func() error { return trimJSONLPrefix(path, drop) }); err != nil {
				return removed, err
			}
			total -= drop
		}
	}
	return removed, nil
}

func cleanupAuditFiles(dataRoot string, now time.Time) (int, error) {
	files := []retentionFile{}
	for _, pattern := range []string{
		filepath.Join(dataRoot, "logs", "messages.jsonl"),
		filepath.Join(dataRoot, "logs", "audit-machine", "*.jsonl"),
		filepath.Join(dataRoot, "logs", "audit", "*.md"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return 0, err
		}
		for _, path := range matches {
			info, statErr := os.Lstat(path)
			if statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				files = append(files, retentionFile{path: path, when: info.ModTime(), size: info.Size()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].when.Before(files[j].when) })
	removed := 0
	total := int64(0)
	for _, file := range files {
		total += file.size
	}
	for index := range files {
		if now.Sub(files[index].when) < auditRetentionAge {
			continue
		}
		if err := removeRetainedFile(files[index].path, true); err != nil {
			return removed, err
		}
		total -= files[index].size
		files[index].path = ""
		removed++
	}
	for _, file := range files {
		if total <= auditRetentionBytes {
			break
		}
		if file.path == "" || now.Sub(file.when) < time.Minute {
			continue
		}
		if err := removeRetainedFile(file.path, true); err != nil {
			return removed, err
		}
		total -= file.size
		removed++
	}
	return removed, nil
}

func cleanupLegacyWorkFiles(dataRoot string, now time.Time) (int, error) {
	root := filepath.Join(dataRoot, "private-cache", "workbox-v3", "legacy-v2")
	removed := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.SkipDir
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) >= workRetentionAge {
			if err := removePrivateRegular(path); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return removed, err
}

func removeExpiredOrExcess(files []retentionFile, now time.Time, maxAge time.Duration, maxCount int) (int, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].when.Before(files[j].when) })
	removed := 0
	remaining := []retentionFile{}
	for _, file := range files {
		if now.Sub(file.when) >= maxAge {
			if err := removePrivateRegular(file.path); err != nil {
				return removed, err
			}
			removed++
		} else {
			remaining = append(remaining, file)
		}
	}
	for len(remaining) > maxCount {
		if err := removePrivateRegular(remaining[0].path); err != nil {
			return removed, err
		}
		remaining = remaining[1:]
		removed++
	}
	return removed, nil
}

func removePrivateRegular(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe retention target")
	}
	return os.Remove(path)
}

func removeRetainedFile(path string, companionLock bool) error {
	if !companionLock {
		return removePrivateRegular(path)
	}
	lockPath := path + ".lock"
	if err := withProcessFileLock(lockPath, func() error { return removePrivateRegular(path) }); err != nil {
		return err
	}
	// Retained objects are terminal/immutable. Once the data file has been
	// removed, the per-object lock has no valid writer and can be removed too.
	return removePrivateRegular(lockPath)
}

func jsonlLineCount(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}

func trimJSONLPrefix(path string, drop int) error {
	if drop <= 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lines := [][]byte{}
	index := 0
	for scanner.Scan() {
		if index >= drop {
			lines = append(lines, append([]byte(nil), scanner.Bytes()...))
		}
		index++
	}
	readErr := scanner.Err()
	_ = file.Close()
	if readErr != nil {
		return readErr
	}
	payload := []byte{}
	for _, line := range lines {
		payload = append(payload, line...)
		payload = append(payload, '\n')
	}
	return writePrivateBytes(path, payload)
}

func writePrivateBytes(path string, payload []byte) error {
	root := filepath.Dir(path)
	if err := ensurePrivateDirectory(root); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, ".retention-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	_, writeErr := temporary.Write(payload)
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	if closeErr := temporary.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	return replacePrivateFile(temporaryPath, path)
}
