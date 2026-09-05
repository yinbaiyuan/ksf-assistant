package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privatestore"
)

const eventWorkerCount = 4
const eventInboxMaxRecords = 2048
const eventInboxMaxBytes = 900 * 1024
const eventPayloadMaxBytes = 64 * 1024
const eventDedupeRetention = 30 * 24 * time.Hour

type inboxEvent struct {
	Event      feishuprotocol.Event `json:"event"`
	Digest     string               `json:"digest"`
	Partition  int                  `json:"partition"`
	State      string               `json:"state"`
	Attempts   int                  `json:"attempts"`
	AcceptedAt time.Time            `json:"acceptedAt"`
	FinishedAt time.Time            `json:"finishedAt,omitempty"`
	LastError  string               `json:"lastError,omitempty"`
}

type eventInboxFile struct {
	SchemaVersion int          `json:"schemaVersion"`
	Events        []inboxEvent `json:"events"`
}

type eventInbox struct {
	mu      sync.Mutex
	path    string
	file    eventInboxFile
	wake    [eventWorkerCount]chan struct{}
	started bool
}

func newEventInbox(dataRoot string) (*eventInbox, error) {
	inbox := &eventInbox{path: filepath.Join(dataRoot, "integration-events-v1.json"), file: eventInboxFile{SchemaVersion: 1, Events: []inboxEvent{}}}
	missing, err := privatestore.ReadJSON(inbox.path, &inbox.file)
	if err != nil {
		return nil, err
	}
	if inbox.file.SchemaVersion != 1 {
		return nil, errors.New("unsupported integration inbox schema")
	}
	for index := range inbox.wake {
		inbox.wake[index] = make(chan struct{}, 1)
	}
	seen := map[string]bool{}
	for index := range inbox.file.Events {
		event := &inbox.file.Events[index]
		if event.Event.ID == "" || seen[event.Event.ID] || event.Partition < 0 || event.Partition >= eventWorkerCount {
			return nil, errors.New("invalid integration inbox record")
		}
		seen[event.Event.ID] = true
		switch event.State {
		case "running":
			event.State, event.LastError = "outcome_unknown", "interrupted_before_durable_outcome"
			event.FinishedAt = time.Now().UTC()
			event.Event.Payload = nil
		case "pending", "completed", "failed", "outcome_unknown":
		default:
			return nil, errors.New("invalid integration inbox state")
		}
		if event.State == "pending" {
			_, _, digest, err := decodeEvent(event.Event)
			if err != nil || digest != event.Digest {
				return nil, errors.New("invalid integration inbox payload binding")
			}
		}
	}
	if !missing {
		if err := inbox.save(inbox.file); err != nil {
			return nil, err
		}
	}
	return inbox, nil
}

func (inbox *eventInbox) save(file eventInboxFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > eventInboxMaxBytes {
		return errors.New("integration inbox capacity exceeded")
	}
	return privatestore.WithFileLock(inbox.path+".lock", func() error { return privatestore.WriteJSON(inbox.path, file) })
}

func decodeEvent(event feishuprotocol.Event) (any, string, string, error) {
	if strings.TrimSpace(event.ID) == "" || len(event.ID) > 512 || len(event.Payload) == 0 || len(event.Payload) > eventPayloadMaxBytes {
		return nil, "", "", errors.New("invalid integration event")
	}
	var payload any
	var conversation string
	switch event.Kind {
	case "message":
		var message InboundMessage
		if err := json.Unmarshal(event.Payload, &message); err != nil {
			return nil, "", "", err
		}
		if message.MessageID == "" || message.SenderOpenID == "" {
			return nil, "", "", errors.New("invalid inbound message")
		}
		if message.EventID != "" && message.EventID != event.ID {
			return nil, "", "", errors.New("inbound event ID mismatch")
		}
		payload = message
		conversation = conversationKey(message.ChatID, message.SenderOpenID)
	case "card":
		var card InboundCardAction
		if err := json.Unmarshal(event.Payload, &card); err != nil {
			return nil, "", "", err
		}
		if card.TaskKey == "" || card.LinkID == "" || card.OperatorOpenID == "" || card.MessageID == "" || card.Action == "" {
			return nil, "", "", errors.New("invalid inbound card")
		}
		if card.EventID != "" && card.EventID != event.ID {
			return nil, "", "", errors.New("inbound event ID mismatch")
		}
		payload = card
		conversation = conversationKey(card.ChatID, card.OperatorOpenID)
	default:
		return nil, "", "", errors.New("unsupported integration event kind")
	}
	var raw any
	decoder := json.NewDecoder(strings.NewReader(string(event.Payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, "", "", err
	}
	canonical, err := json.Marshal(raw)
	if err != nil {
		return nil, "", "", err
	}
	digest := sha256.Sum256(append([]byte(event.Kind+"\x00"), canonical...))
	return payload, conversation, hex.EncodeToString(digest[:]), nil
}

func conversationKey(chatID, openID string) string {
	if chatID != "" {
		return "chat:" + chatID
	}
	return "user:" + openID
}

func (runtime *Runtime) AcceptEvent(ctx context.Context, event feishuprotocol.Event) (feishuprotocol.Accepted, error) {
	if err := ctx.Err(); err != nil {
		return feishuprotocol.Accepted{}, err
	}
	_, conversation, digest, err := decodeEvent(event)
	if err != nil {
		return feishuprotocol.Accepted{}, err
	}
	inbox := runtime.inbox
	inbox.mu.Lock()
	if runtime.watchCtx.Err() != nil {
		inbox.mu.Unlock()
		return feishuprotocol.Accepted{}, ErrRuntimeClosed
	}
	for _, record := range inbox.file.Events {
		if record.Event.ID != event.ID {
			continue
		}
		inbox.mu.Unlock()
		if record.Digest != digest {
			return feishuprotocol.Accepted{}, errors.New("integration event ID conflict")
		}
		runtime.startEventWorkers()
		return feishuprotocol.Accepted{Accepted: true}, nil
	}
	now := time.Now().UTC()
	file := eventInboxFile{SchemaVersion: 1, Events: make([]inboxEvent, 0, len(inbox.file.Events)+1)}
	for _, record := range inbox.file.Events {
		if expiredInboxReceipt(record, now) {
			continue
		}
		file.Events = append(file.Events, record)
	}
	if len(file.Events) >= eventInboxMaxRecords {
		inbox.mu.Unlock()
		runtime.setHealth("event inbox capacity exceeded", errors.New("capacity"))
		return feishuprotocol.Accepted{}, errors.New("integration inbox capacity exceeded")
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(conversation))
	partition := int(hash.Sum32() % eventWorkerCount)
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	file.Events = append(file.Events, inboxEvent{Event: event, Digest: digest, Partition: partition, State: "pending", AcceptedAt: now})
	err = inbox.save(file)
	runtime.setHealth("event acceptance persistence failed", err)
	if err == nil {
		inbox.file = file
		runtime.setHealth("event inbox capacity exceeded", nil)
	}
	inbox.mu.Unlock()
	if err != nil {
		return feishuprotocol.Accepted{}, err
	}
	runtime.startEventWorkers()
	select {
	case inbox.wake[partition] <- struct{}{}:
	default:
	}
	return feishuprotocol.Accepted{Accepted: true}, nil
}

func (runtime *Runtime) startEventWorkers() {
	inbox := runtime.inbox
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if inbox.started || runtime.watchCtx.Err() != nil {
		return
	}
	inbox.started = true
	for partition := 0; partition < eventWorkerCount; partition++ {
		partition := partition
		runtime.launchWatcher("inbox:"+string(rune('0'+partition)), func(ctx context.Context) { runtime.runEventWorker(ctx, partition) })
	}
}

func (inbox *eventInbox) claim(partition int) (inboxEvent, bool, error) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	for index, record := range inbox.file.Events {
		if record.Partition != partition || record.State != "pending" {
			continue
		}
		file := inbox.file
		file.Events = append([]inboxEvent(nil), file.Events...)
		record.State = "running"
		record.Attempts++
		file.Events[index] = record
		if err := inbox.save(file); err != nil {
			return inboxEvent{}, false, err
		}
		inbox.file = file
		return record, true, nil
	}
	return inboxEvent{}, false, nil
}

func (inbox *eventInbox) finish(record inboxEvent, dispatchErr error, cancelled bool) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	for index, current := range inbox.file.Events {
		if current.Event.ID != record.Event.ID {
			continue
		}
		file := inbox.file
		file.Events = append([]inboxEvent(nil), file.Events...)
		if dispatchErr == nil && !cancelled {
			current.State, current.LastError = "completed", ""
			current.FinishedAt = time.Now().UTC()
			current.Event.Payload = nil
		} else if errors.Is(dispatchErr, ErrInactiveTaskLink) {
			current.State, current.LastError = "failed", "inactive_task_link"
			current.FinishedAt = time.Now().UTC()
			current.Event.Payload = nil
		} else {
			current.State, current.LastError = "outcome_unknown", "execution_not_proven_safe_to_replay"
			current.FinishedAt = time.Now().UTC()
			current.Event.Payload = nil
		}
		file.Events[index] = current
		if err := inbox.save(file); err != nil {
			return err
		}
		inbox.file = file
		return nil
	}
	return errors.New("integration inbox record missing")
}

func (runtime *Runtime) runEventWorker(ctx context.Context, partition int) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		record, found, claimErr := runtime.inbox.claim(partition)
		runtime.setHealth(fmt.Sprintf("event worker %d claim persistence failed", partition), claimErr)
		if found {
			payload, _, _, err := decodeEvent(record.Event)
			if err == nil {
				dispatchCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				switch value := payload.(type) {
				case InboundMessage:
					err = runtime.HandleMessage(dispatchCtx, value)
				case InboundCardAction:
					err = runtime.HandleCard(dispatchCtx, value)
				}
				cancel()
			}
			for {
				finishErr := runtime.inbox.finish(record, err, ctx.Err() != nil && err != nil)
				runtime.setHealth(fmt.Sprintf("event worker %d outcome persistence failed", partition), finishErr)
				if finishErr == nil {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-runtime.inbox.wake[partition]:
		case <-ticker.C:
		}
	}
}

func (runtime *Runtime) ResumeEvents() error {
	if runtime.watchCtx.Err() != nil {
		return ErrRuntimeClosed
	}
	runtime.startEventWorkers()
	return nil
}

func expiredInboxReceipt(record inboxEvent, now time.Time) bool {
	terminal := record.State == "completed" || record.State == "failed" || record.State == "outcome_unknown"
	return terminal && !record.FinishedAt.IsZero() && now.Sub(record.FinishedAt) > eventDedupeRetention
}

func (inbox *eventInbox) prune(now time.Time) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	file := eventInboxFile{SchemaVersion: 1, Events: make([]inboxEvent, 0, len(inbox.file.Events))}
	for _, record := range inbox.file.Events {
		if !expiredInboxReceipt(record, now) {
			file.Events = append(file.Events, record)
		}
	}
	if len(file.Events) == len(inbox.file.Events) {
		return nil
	}
	if err := inbox.save(file); err != nil {
		return err
	}
	inbox.file = file
	return nil
}
