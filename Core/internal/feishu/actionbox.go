package feishu

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const QueueSchemaVersion = 2

type ActionRequest struct {
	ID                    string         `json:"id"`
	Type                  string         `json:"type"`
	Domain                string         `json:"domain"`
	Action                string         `json:"action"`
	CapabilityID          string         `json:"capabilityId"`
	Identity              string         `json:"identity"`
	Input                 map[string]any `json:"input"`
	ExplicitAuthorization bool           `json:"explicitAuthorization"`
	ConfirmHighImpact     bool           `json:"confirmHighImpact,omitempty"`
	DryRun                bool           `json:"dryRun,omitempty"`
	RemoteTimeoutMS       int            `json:"remoteTimeoutMs,omitempty"`
	PollIntervalMS        int            `json:"pollIntervalMs,omitempty"`
	SaveAs                string         `json:"saveAs,omitempty"`
	Source                string         `json:"source"`
	Reason                string         `json:"reason,omitempty"`
	Trace                 map[string]any `json:"trace,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
}
type ActionResult struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	CapabilityID string         `json:"capabilityId"`
	Result       map[string]any `json:"result,omitempty"`
	Error        string         `json:"error,omitempty"`
	CompletedAt  time.Time      `json:"completedAt"`
}
type queueState struct {
	SchemaVersion      int               `json:"schemaVersion"`
	ProcessedLineCount int               `json:"processedLineCount"`
	ProcessedIDs       map[string]string `json:"processedIds"`
	LastProcessedAt    string            `json:"lastProcessedAt"`
	LastError          string            `json:"lastError"`
	LastTrigger        any               `json:"lastTrigger,omitempty"`
	Wake               queueWake         `json:"wake"`
}
type queueWake struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	ConfiguredPort int    `json:"configuredPort"`
	ActualPort     *int   `json:"actualPort"`
}

func defaultQueueState() queueState {
	return queueState{SchemaVersion: QueueSchemaVersion, ProcessedIDs: map[string]string{}, Wake: queueWake{Host: "127.0.0.1"}}
}
func normalizeQueueState(state *queueState) {
	state.SchemaVersion = QueueSchemaVersion
	if state.ProcessedIDs == nil {
		state.ProcessedIDs = map[string]string{}
	}
	if state.Wake.Host == "" {
		state.Wake.Host = "127.0.0.1"
	}
	if len(state.ProcessedIDs) > 5000 {
		trimProcessedIDs(state.ProcessedIDs, 5000)
	}
}
func trimProcessedIDs(values map[string]string, limit int) {
	for len(values) > limit {
		for key := range values {
			delete(values, key)
			break
		}
	}
}

type Actionbox struct {
	root, dataRoot string
	mu             sync.Mutex
}

func NewActionbox(dataRoot string) *Actionbox {
	return &Actionbox{root: filepath.Join(dataRoot, "logs"), dataRoot: dataRoot}
}
func (box *Actionbox) queuePath() string  { return filepath.Join(box.root, "actionbox.jsonl") }
func (box *Actionbox) resultPath() string { return filepath.Join(box.root, "actionbox-results.jsonl") }
func (box *Actionbox) statePath() string  { return filepath.Join(box.root, "actionbox-state.json") }
func (box *Actionbox) processLockPath() string {
	return filepath.Join(box.root, ".actionbox-process.lock")
}
func NewActionID() (string, error) {
	return newQueueID("ACT")
}

func NewOutboxID() (string, error) { return newQueueID("OUT") }

func newQueueID(prefix string) (string, error) {
	value := make([]byte, 10)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "-" + time.Now().UTC().Format("20060102150405") + "-" + strings.ToUpper(hex.EncodeToString(value[:4])), nil
}

func (box *Actionbox) Submit(request ActionRequest) error {
	box.mu.Lock()
	defer box.mu.Unlock()
	if request.ID == "" || request.Type != "feishu_capability" || request.Domain != "capability" || request.Action != "execute" || !request.ExplicitAuthorization {
		return errors.New("invalid actionbox request")
	}
	definition, ok := CapabilityByID(request.CapabilityID)
	if !ok {
		return errors.New("unknown_capability")
	}
	if definition.Risk == "read" {
		return errors.New("read_capability_must_not_enter_actionbox")
	}
	if definition.Queue != "actionbox" {
		return errors.New("capability_requires_" + definition.Queue)
	}
	if definition.Identity != request.Identity {
		return errors.New("unsupported_identity")
	}
	if definition.Risk == "high-impact-write" && !request.ConfirmHighImpact {
		return errors.New("high_impact_confirmation_required")
	}
	if definition.Risk == "remote-operation" {
		if request.RemoteTimeoutMS != 0 && (request.RemoteTimeoutMS < 10000 || request.RemoteTimeoutMS > 30*60*1000) {
			return errors.New("invalid_remote_timeout")
		}
		if request.PollIntervalMS != 0 && (request.PollIntervalMS < 250 || request.PollIntervalMS > 30000) {
			return errors.New("invalid_remote_poll_interval")
		}
	} else if request.RemoteTimeoutMS != 0 || request.PollIntervalMS != 0 {
		return errors.New("remote_poll_not_supported")
	}
	if err := ValidateCapabilityInput(request.CapabilityID, request.Input); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(box.root); err != nil {
		return err
	}
	return appendPrivateJSONL(box.queuePath(), request)
}

func (box *Actionbox) Process(ctx context.Context, executor CapabilityExecutor) error {
	box.mu.Lock()
	defer box.mu.Unlock()
	return withProcessFileLock(box.processLockPath(), func() error {
		return box.processLocked(ctx, executor)
	})
}

func (box *Actionbox) processLocked(ctx context.Context, executor CapabilityExecutor) error {
	state := defaultQueueState()
	if missing, err := readPrivateJSON(box.statePath(), &state); err != nil && !missing {
		return err
	}
	normalizeQueueState(&state)
	file, err := os.Open(box.queuePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if line <= state.ProcessedLineCount {
			continue
		}
		var request ActionRequest
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			state.ProcessedLineCount = line
			state.LastError = "invalid_json_line"
			continue
		}
		if _, done := state.ProcessedIDs[request.ID]; done {
			state.ProcessedLineCount = line
			continue
		}
		result := ActionResult{ID: request.ID, CapabilityID: request.CapabilityID, CompletedAt: time.Now().UTC()}
		if request.DryRun {
			result.Status = "dry_run"
		} else if value, runErr := executor.ExecuteWithOptions(ctx, request.CapabilityID, request.Input, CapabilityExecutionOptions{RemoteTimeout: time.Duration(request.RemoteTimeoutMS) * time.Millisecond, RemotePollInterval: time.Duration(request.PollIntervalMS) * time.Millisecond}); runErr != nil {
			result.Status = "failed"
			result.Error = safeCommandError(runErr.Error())
			state.LastError = result.Error
		} else {
			result.Status = "completed"
			result.Result = value
			state.LastError = ""
		}
		if err := appendPrivateJSONL(box.resultPath(), result); err != nil {
			return err
		}
		inputJSON, _ := json.Marshal(request.Input)
		_ = NewAuditLog(box.dataRoot).Record("actionbox_result", map[string]any{"id": result.ID, "capability": result.CapabilityID, "status": result.Status, "input": AuditContentDescriptor(string(inputJSON)), "error": result.Error})
		state.ProcessedLineCount = line
		state.ProcessedIDs[request.ID] = result.Status
		trimProcessedIDs(state.ProcessedIDs, 5000)
		state.LastProcessedAt = result.CompletedAt.Format(time.RFC3339)
		if err := writePrivateJSON(box.statePath(), state); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (box *Actionbox) FindResult(id string) (ActionResult, bool, error) {
	var result ActionResult
	var found bool
	err := withProcessFileLock(box.resultPath()+".lock", func() error {
		var innerErr error
		result, found, innerErr = box.findResultUnlocked(id)
		return innerErr
	})
	return result, found, err
}

func (box *Actionbox) findResultUnlocked(id string) (ActionResult, bool, error) {
	file, err := os.Open(box.resultPath())
	if errors.Is(err, os.ErrNotExist) {
		return ActionResult{}, false, nil
	}
	if err != nil {
		return ActionResult{}, false, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var result ActionResult
	found := false
	for scanner.Scan() {
		var item ActionResult
		if json.Unmarshal(scanner.Bytes(), &item) == nil && item.ID == id {
			result = item
			found = true
		}
	}
	return result, found, scanner.Err()
}
func appendPrivateJSONL(path string, value any) error {
	return withProcessFileLock(path+".lock", func() error {
		return appendPrivateJSONLUnlocked(path, value)
	})
}

func appendPrivateJSONLUnlocked(path string, value any) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if runtimePrivatePermissions() {
		return os.Chmod(path, 0o600)
	}
	return securePrivatePath(path, false)
}
func runtimePrivatePermissions() bool { return os.PathSeparator == '/' }
