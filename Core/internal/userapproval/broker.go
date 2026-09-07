package userapproval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const Lifetime = 5 * time.Minute
const HeartbeatLifetime = 5 * time.Second
const MaximumPending = 10

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Attachment struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Preview struct {
	Content      string `json:"content"`
	ConfirmLabel string `json:"confirmLabel"`
	Destructive  bool   `json:"destructive"`
}

type Review struct {
	Preview     *Preview     `json:"preview,omitempty"`
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	User        string       `json:"user"`
	Application string       `json:"application"`
	Action      string       `json:"action"`
	Target      string       `json:"target"`
	Content     string       `json:"content"`
	Attachments []Attachment `json:"attachments"`
	Source      string       `json:"source"`
	ExpiresAt   time.Time    `json:"expiresAt"`
}

type AuditEvent struct {
	ID     string    `json:"id"`
	Digest string    `json:"digest"`
	State  string    `json:"state"`
	At     time.Time `json:"at"`
}

type entry struct {
	review    Review
	digest    string
	owner     context.Context
	state     string
	presented bool
	deadline  time.Time
}

type Broker struct {
	mu          sync.Mutex
	now         func() time.Time
	audit       func(AuditEvent) error
	entries     map[string]*entry
	order       []string
	interactive bool
	heartbeat   time.Time
	closed      bool
}

func New(now func() time.Time, audit func(AuditEvent) error) *Broker {
	if now == nil {
		now = time.Now
	}
	return &Broker{now: now, audit: audit, entries: map[string]*entry{}}
}

func (broker *Broker) live() bool {
	return !broker.closed && broker.interactive && !broker.heartbeat.IsZero() && broker.now().Sub(broker.heartbeat) <= HeartbeatLifetime
}

func (broker *Broker) transition(value *entry, state string) error {
	if broker.audit != nil {
		if err := broker.audit(AuditEvent{ID: value.review.ID, Digest: value.digest, State: state, At: broker.now().UTC()}); err != nil {
			value.state = "audit_unavailable"
			value.review = Review{ID: value.review.ID, ExpiresAt: value.review.ExpiresAt}
			return errors.New("approval_audit_unavailable")
		}
	}
	value.state = state
	if state != "pending" && state != "approved" {
		value.review = Review{ID: value.review.ID, ExpiresAt: value.review.ExpiresAt}
	}
	return nil
}

func (broker *Broker) refresh() {
	for _, value := range broker.entries {
		if value.state != "pending" && value.state != "approved" && value.state != "executing" {
			continue
		}
		if value.owner.Err() != nil || broker.closed {
			state := "cancelled"
			if value.state == "executing" {
				state = "unknown"
			}
			_ = broker.transition(value, state)
		} else if value.state != "executing" && !broker.now().Before(value.deadline) {
			_ = broker.transition(value, "expired")
		} else if value.state != "executing" && !broker.live() {
			_ = broker.transition(value, "desktop_unavailable")
		}
	}
}

func (broker *Broker) Request(owner context.Context, digest string, review Review) (string, error) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.refresh()
	if owner == nil || owner.Done() == nil || owner.Err() != nil {
		return "", errors.New("approval_connection_required")
	}
	if !broker.live() {
		return "", errors.New("approval_desktop_unavailable")
	}
	if !digestPattern.MatchString(digest) || review.Action == "" || review.User == "" || review.Application == "" || review.Target == "" || len(review.Content) > 256*1024 {
		return "", errors.New("approval_invalid_request")
	}
	for _, value := range []string{review.Title, review.User, review.Application, review.Action, review.Target} {
		if len(value) > 65536 || strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
			return "", errors.New("approval_invalid_request")
		}
	}
	if strings.ContainsRune(review.Content, 0) || !utf8.ValidString(review.Content) || len(review.Attachments) > 1000 {
		return "", errors.New("approval_invalid_request")
	}
	if preview := review.Preview; preview != nil {
		if len(preview.Content) > 256*1024 || !utf8.ValidString(preview.Content) || strings.ContainsRune(preview.Content, 0) || len([]rune(preview.ConfirmLabel)) > 8 || strings.TrimSpace(preview.ConfirmLabel) == "" || strings.ContainsAny(preview.ConfirmLabel, "\x00\r\n") {
			return "", errors.New("approval_invalid_request")
		}
		copy := *preview
		review.Preview = &copy
	}
	for _, attachment := range review.Attachments {
		if attachment.Name == "" || len(attachment.Name) > 4096 || strings.ContainsRune(attachment.Name, 0) || !utf8.ValidString(attachment.Name) || attachment.Size < 0 || !digestPattern.MatchString(attachment.SHA256) {
			return "", errors.New("approval_invalid_request")
		}
	}
	active := 0
	for _, value := range broker.entries {
		if value.state == "pending" || value.state == "approved" || value.state == "executing" {
			active++
		}
	}
	if active >= MaximumPending {
		return "", errors.New("approval_busy")
	}
	if len(broker.order) >= 1000 {
		retained := broker.order[:0]
		for _, id := range broker.order {
			value := broker.entries[id]
			if value.state == "pending" || value.state == "approved" || value.state == "executing" {
				retained = append(retained, id)
			} else {
				delete(broker.entries, id)
			}
		}
		broker.order = retained
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("approval_identity_unavailable")
	}
	review.ID = "approval_" + hex.EncodeToString(random[:])
	deadline := broker.now().Add(Lifetime)
	review.ExpiresAt = deadline.UTC()
	review.Source = "来源未验证"
	review.Attachments = append([]Attachment{}, review.Attachments...)
	value := &entry{review: review, digest: digest, owner: owner, deadline: deadline}
	if err := broker.transition(value, "pending"); err != nil {
		return "", err
	}
	broker.entries[review.ID] = value
	broker.order = append(broker.order, review.ID)
	return review.ID, nil
}

func (broker *Broker) Poll(interactive bool) *Review {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.refresh()
	broker.interactive = interactive
	broker.heartbeat = broker.now()
	broker.refresh()
	if !broker.live() {
		return nil
	}
	for _, id := range broker.order {
		value := broker.entries[id]
		if value.state == "pending" {
			value.presented = true
			copy := value.review
			copy.Attachments = append([]Attachment{}, value.review.Attachments...)
			return &copy
		}
	}
	return nil
}

func (broker *Broker) Decide(id string, approve bool) bool {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.refresh()
	value := broker.entries[id]
	if value == nil || value.state != "pending" || !value.presented || !broker.live() {
		return false
	}
	state := "denied"
	if approve {
		state = "approved"
	}
	return broker.transition(value, state) == nil
}

func (broker *Broker) owned(owner context.Context, id string) (*entry, error) {
	value := broker.entries[id]
	if value == nil || value.owner != owner {
		return nil, errors.New("approval_not_found")
	}
	return value, nil
}

func (broker *Broker) Status(owner context.Context, id string) (string, error) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.refresh()
	value, err := broker.owned(owner, id)
	if err != nil {
		return "", err
	}
	return value.state, nil
}

func (broker *Broker) Consume(owner context.Context, id, digest string) error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.refresh()
	value, err := broker.owned(owner, id)
	if err != nil {
		return err
	}
	if value.state != "approved" {
		return errors.New("approval_" + value.state)
	}
	if digest != value.digest {
		_ = broker.transition(value, "request_changed")
		return errors.New("approval_request_changed")
	}
	return broker.transition(value, "executing")
}

func (broker *Broker) Result(owner context.Context, id, outcome string) error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.refresh()
	value, err := broker.owned(owner, id)
	if err != nil {
		return err
	}
	if value.state != "executing" {
		return errors.New("approval_not_executing")
	}
	if outcome != "succeeded" && outcome != "failed" && outcome != "unknown" && outcome != "not_started" {
		return errors.New("approval_invalid_outcome")
	}
	return broker.transition(value, outcome)
}

func (broker *Broker) Cancel(owner context.Context, id string) error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.refresh()
	value, err := broker.owned(owner, id)
	if err != nil {
		return err
	}
	if value.state == "pending" || value.state == "approved" {
		return broker.transition(value, "cancelled")
	}
	if value.state == "executing" {
		return broker.transition(value, "unknown")
	}
	return nil
}

func (broker *Broker) Close() {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.closed = true
	broker.refresh()
}
