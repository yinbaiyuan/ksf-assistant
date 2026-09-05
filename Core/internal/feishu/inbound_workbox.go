package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type inboundWork struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	EventKey      string          `json:"eventKey"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	PayloadHash   string          `json:"payloadFingerprint,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	CompletedAt   *time.Time      `json:"completedAt,omitempty"`
	AttemptCount  int             `json:"attemptCount,omitempty"`
	LastError     string          `json:"lastError,omitempty"`
	NextAttemptAt *time.Time      `json:"nextAttemptAt,omitempty"`
	Status        string          `json:"status,omitempty"`
	Existing      bool            `json:"-"`
}

type InboundWorkbox struct{ root string }

func NewInboundWorkbox(dataRoot string) *InboundWorkbox {
	return &InboundWorkbox{root: filepath.Join(dataRoot, "private-cache", "inbound-work")}
}

func (box *InboundWorkbox) Enqueue(eventKey string, payload []byte) (inboundWork, error) {
	if !json.Valid(payload) || !contains(FixedEventKeys, eventKey) {
		return inboundWork{}, errors.New("invalid_inbound_work")
	}
	sum := sha256.Sum256(append(append([]byte(eventKey), 0), payload...))
	work := inboundWork{SchemaVersion: 3, ID: hex.EncodeToString(sum[:16]), EventKey: eventKey, Payload: append(json.RawMessage(nil), payload...), PayloadHash: AuditFingerprint(string(payload)), CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := ensurePrivateDirectory(box.root); err != nil {
		return inboundWork{}, err
	}
	path := filepath.Join(box.root, work.ID+".json")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return inboundWork{}, errors.New("unsafe_existing_inbound_work")
		}
		work.Existing = true
		return work, nil
	} else if !os.IsNotExist(err) {
		return inboundWork{}, err
	}
	if err := writePrivateJSON(path, work); err != nil {
		return inboundWork{}, err
	}
	return work, nil
}

func (box *InboundWorkbox) Complete(id string) error {
	if len(id) != 32 {
		return errors.New("invalid_inbound_work_id")
	}
	path := filepath.Join(box.root, id+".json")
	var work inboundWork
	missing, err := readPrivateJSON(path, &work)
	if missing || err != nil {
		return err
	}
	now := time.Now().UTC()
	work.SchemaVersion = 3
	if work.PayloadHash == "" && len(work.Payload) > 0 {
		work.PayloadHash = AuditFingerprint(string(work.Payload))
	}
	work.Payload = nil
	work.Status = "completed"
	work.CompletedAt = &now
	work.NextAttemptAt = nil
	work.LastError = ""
	work.Existing = false
	err = writePrivateJSON(path, work)
	if err == nil {
		signalMaintenance(filepath.Dir(filepath.Dir(box.root)))
	}
	return err
}

func (box *InboundWorkbox) RecordFailure(work inboundWork, errorClass string, attempts int, terminal bool, retryAt time.Time) error {
	if attempts < 1 {
		attempts = 1
	}
	work.SchemaVersion = 3
	work.AttemptCount += attempts
	work.LastError = errorClass
	if terminal {
		work.Status = "failed"
		work.NextAttemptAt = nil
		now := time.Now().UTC()
		work.CompletedAt = &now
		if work.PayloadHash == "" && len(work.Payload) > 0 {
			work.PayloadHash = AuditFingerprint(string(work.Payload))
		}
		work.Payload = nil
	} else {
		work.Status = "pending"
		retryAt = retryAt.UTC()
		work.NextAttemptAt = &retryAt
	}
	work.Existing = false
	err := writePrivateJSON(filepath.Join(box.root, work.ID+".json"), work)
	if err == nil && terminal {
		signalMaintenance(filepath.Dir(filepath.Dir(box.root)))
	}
	return err
}

func (box *InboundWorkbox) Pending() ([]inboundWork, error) {
	entries, err := os.ReadDir(box.root)
	if os.IsNotExist(err) {
		return []inboundWork{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []inboundWork{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var work inboundWork
		missing, err := readPrivateJSON(filepath.Join(box.root, entry.Name()), &work)
		if err != nil || missing || (work.SchemaVersion != 1 && work.SchemaVersion != 2 && work.SchemaVersion != 3) || work.ID == "" {
			return nil, errors.New("invalid_persisted_inbound_work")
		}
		if work.Status == "failed" || work.Status == "completed" {
			continue
		}
		if !json.Valid(work.Payload) {
			return nil, errors.New("invalid_persisted_inbound_work")
		}
		result = append(result, work)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

// ScrubTerminal removes legacy raw payloads from terminal receipts without
// replaying the event. It is safe to run at every startup.
func (box *InboundWorkbox) ScrubTerminal() (int, error) {
	entries, err := os.ReadDir(box.root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(box.root, entry.Name())
		var work inboundWork
		missing, readErr := readPrivateJSON(path, &work)
		if missing || readErr != nil || (work.Status != "failed" && work.Status != "completed") || len(work.Payload) == 0 {
			continue
		}
		if work.PayloadHash == "" {
			work.PayloadHash = AuditFingerprint(string(work.Payload))
		}
		work.SchemaVersion = 3
		work.Payload = nil
		if work.CompletedAt == nil {
			now := time.Now().UTC()
			work.CompletedAt = &now
		}
		if err := writePrivateJSON(path, work); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (box *InboundWorkbox) Ready(now time.Time) ([]inboundWork, error) {
	items, err := box.Pending()
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	result := make([]inboundWork, 0, len(items))
	for _, item := range items {
		if item.NextAttemptAt != nil && item.NextAttemptAt.After(now) {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func (box *InboundWorkbox) Recover(ctx context.Context, dispatch func(context.Context, string, []byte) error) error {
	items, err := box.Ready(time.Now().UTC())
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := dispatch(ctx, item.EventKey, item.Payload); err != nil {
			// A handler failure is recoverable work, not a corrupt queue. Keep the
			// item for the next pass and continue so one stale event cannot block
			// the bridge from connecting or starve later inbound messages.
			continue
		}
		if err := box.Complete(item.ID); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
