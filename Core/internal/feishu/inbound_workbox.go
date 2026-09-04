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
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"createdAt"`
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
	work := inboundWork{SchemaVersion: 1, ID: hex.EncodeToString(sum[:16]), EventKey: eventKey, Payload: append(json.RawMessage(nil), payload...), CreatedAt: time.Now().UTC()}
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
	return os.Remove(filepath.Join(box.root, id+".json"))
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
		if err != nil || missing || work.SchemaVersion != 1 || work.ID == "" || !json.Valid(work.Payload) {
			return nil, errors.New("invalid_persisted_inbound_work")
		}
		result = append(result, work)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (box *InboundWorkbox) Recover(ctx context.Context, dispatch func(context.Context, string, []byte) error) error {
	items, err := box.Pending()
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
