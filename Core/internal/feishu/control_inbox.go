package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const controlInboxSchema = 1

var controlIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

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
	return errors.New("legacy_control_inbox_retired_use_core_gateway")
}

func (inbox *ControlInbox) Retire(ctx context.Context) error {
	if _, err := os.Lstat(inbox.root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return withProcessFileLock(inbox.lockPath(), func() error {
		marker := filepath.Join(inbox.root, "retired-v2.json")
		var completed map[string]bool
		missing, err := readPrivateJSON(marker, &completed)
		if err != nil {
			return err
		}
		if !missing && completed["completed"] {
			return nil
		}
		entries, err := os.ReadDir(inbox.root)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".request.json") {
				continue
			}
			var request ControlRequest
			missing, err := readPrivateJSON(filepath.Join(inbox.root, entry.Name()), &request)
			if err != nil || missing || request.SchemaVersion != controlInboxSchema || !controlIDPattern.MatchString(request.ID) {
				return errors.New("invalid_legacy_control_request")
			}
			var prior ControlResult
			missing, err = readPrivateJSON(inbox.resultPath(request.ID), &prior)
			if err != nil {
				return err
			}
			if missing {
				result := ControlResult{SchemaVersion: controlInboxSchema, ID: request.ID, Status: "failed", ErrorClass: "legacy_request_requires_reconfirmation", CompletedAt: time.Now().UTC()}
				if err := writePrivateJSON(inbox.resultPath(request.ID), result); err != nil {
					return err
				}
			}
		}
		return writePrivateJSON(marker, map[string]bool{"completed": true})
	})
}

func (inbox *ControlInbox) Process(ctx context.Context, _ func(context.Context, ControlRequest) error) error {
	return inbox.Retire(ctx)
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
