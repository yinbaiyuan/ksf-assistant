package feishu

import (
	"encoding/json"
	"errors"
	"ksfassistant/core/internal/feishuprotocol"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var inboundReviewID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type InboundReview struct {
	ID            string     `json:"id"`
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	ErrorCode     string     `json:"errorCode"`
	Stage         string     `json:"stage"`
	CreatedAt     time.Time  `json:"createdAt"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
}

func (box *InboundWorkbox) Review() ([]InboundReview, error) {
	entries, err := os.ReadDir(box.root)
	if os.IsNotExist(err) {
		return []InboundReview{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []InboundReview{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var w inboundWork
		_, err := readPrivateJSON(filepath.Join(box.root, e.Name()), &w)
		if err != nil {
			return nil, err
		}
		if w.Status == "pending" || w.Status == "needs_review" {
			out = append(out, InboundReview{w.ID, w.Status, w.AttemptCount, w.LastError, w.FailureStage, w.CreatedAt, w.NextAttemptAt})
		}
	}
	return out, nil
}
func (box *InboundWorkbox) reviewWork(id string) (inboundWork, error) {
	var w inboundWork
	if !inboundReviewID.MatchString(id) {
		return w, errors.New("invalid review id")
	}
	missing, err := readPrivateJSON(filepath.Join(box.root, id+".json"), &w)
	if err != nil {
		return w, err
	}
	if missing || w.Status != "needs_review" {
		return w, errors.New("event is not awaiting review")
	}
	return w, nil
}

// ReviewEvent is private to Core; never render its payload in CLI output.
func (box *InboundWorkbox) ReviewEvent(id string) (feishuprotocol.Event, error) {
	w, err := box.reviewWork(id)
	if err != nil {
		return feishuprotocol.Event{}, err
	}
	var raw map[string]any
	if json.Unmarshal(w.Payload, &raw) != nil {
		return feishuprotocol.Event{}, errors.New("invalid payload")
	}
	var value any
	kind := "message"
	eventID := ""
	if w.EventKey == "im.message.receive_v1" {
		m, e := normalizeInboundMessage(raw)
		if e != nil {
			return feishuprotocol.Event{}, e
		}
		value = m
		eventID = m.EventID
	} else if w.EventKey == "card.action.trigger" {
		c, e := normalizeInboundCard(raw)
		if e != nil {
			return feishuprotocol.Event{}, e
		}
		value = c
		eventID = c.EventID
		kind = "card"
	} else {
		return feishuprotocol.Event{}, errors.New("unsupported review event")
	}
	payload, err := json.Marshal(value)
	return feishuprotocol.Event{ID: eventID, Kind: kind, Payload: payload}, err
}
func (box *InboundWorkbox) RetryReviewed(id string) error {
	w, err := box.reviewWork(id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	w.RetryStartedAt = &now
	w.RetryBase = w.AttemptCount
	w.Status = "pending"
	w.NextAttemptAt = nil
	w.LastError = ""
	w.FailureStage = ""
	return writePrivateJSON(filepath.Join(box.root, id+".json"), w)
}
