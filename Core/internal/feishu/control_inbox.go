package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const controlInboxSchema = 1

type ControlRequest struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	Operation     string    `json:"operation"`
	TaskKey       string    `json:"taskKey"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ControlResult struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	ErrorClass    string    `json:"errorClass,omitempty"`
	CompletedAt   time.Time `json:"completedAt"`
}

type ControlInbox struct{ root string }

func NewControlInbox(dataRoot string) *ControlInbox {
	return &ControlInbox{root: filepath.Join(dataRoot, "private-cache", "bridge-control")}
}

func NewControlID() (string, error) { return newQueueID("CTL") }

func (inbox *ControlInbox) Submit(request ControlRequest) error {
	request.SchemaVersion = controlInboxSchema
	request.ID = strings.TrimSpace(request.ID)
	request.TaskKey = strings.TrimSpace(request.TaskKey)
	if !controlIDPattern.MatchString(request.ID) || request.Operation != "taskLink.interrupt" || request.TaskKey == "" || len(request.TaskKey) > 256 {
		return errors.New("invalid_bridge_control_request")
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = time.Now().UTC()
	} else {
		request.CreatedAt = request.CreatedAt.UTC()
	}
	if err := ensurePrivateDirectory(inbox.root); err != nil {
		return err
	}
	path := inbox.requestPath(request.ID)
	return withProcessFileLock(inbox.lockPath(), func() error {
		if _, err := os.Lstat(path); err == nil {
			return errors.New("duplicate_bridge_control_request")
		} else if !os.IsNotExist(err) {
			return err
		}
		return writePrivateJSON(path, request)
	})
}

func (inbox *ControlInbox) Process(ctx context.Context, handler func(context.Context, ControlRequest) error) error {
	if handler == nil {
		return errors.New("missing_bridge_control_handler")
	}
	if err := ensurePrivateDirectory(inbox.root); err != nil {
		return err
	}
	return withProcessFileLock(inbox.lockPath(), func() error {
		entries, err := os.ReadDir(inbox.root)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".request.json") {
				continue
			}
			requestPath := filepath.Join(inbox.root, entry.Name())
			var request ControlRequest
			missing, readErr := readPrivateJSON(requestPath, &request)
			if readErr != nil || missing || request.SchemaVersion != controlInboxSchema || !controlIDPattern.MatchString(request.ID) || request.Operation != "taskLink.interrupt" || strings.TrimSpace(request.TaskKey) == "" {
				return errors.New("invalid_persisted_bridge_control")
			}
			result := ControlResult{SchemaVersion: controlInboxSchema, ID: request.ID, Status: "succeeded", CompletedAt: time.Now().UTC()}
			if callErr := handler(ctx, request); callErr != nil {
				result.Status = "failed"
				result.ErrorClass = "control_failed"
			}
			if err := writePrivateJSON(inbox.resultPath(request.ID), result); err != nil {
				return err
			}
			if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	})
}

func (inbox *ControlInbox) TakeResult(id string) (ControlResult, bool, error) {
	if !controlIDPattern.MatchString(id) {
		return ControlResult{}, false, errors.New("invalid_bridge_control_id")
	}
	var result ControlResult
	path := inbox.resultPath(id)
	missing, err := readPrivateJSON(path, &result)
	if err != nil || missing {
		return ControlResult{}, false, err
	}
	if result.SchemaVersion != controlInboxSchema || result.ID != id || (result.Status != "succeeded" && result.Status != "failed") {
		return ControlResult{}, false, errors.New("invalid_bridge_control_result")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return ControlResult{}, false, err
	}
	return result, true, nil
}

func (inbox *ControlInbox) requestPath(id string) string {
	return filepath.Join(inbox.root, id+".request.json")
}

func (inbox *ControlInbox) resultPath(id string) string {
	return filepath.Join(inbox.root, id+".result.json")
}

func (inbox *ControlInbox) lockPath() string {
	return filepath.Join(inbox.root, ".process.lock")
}
