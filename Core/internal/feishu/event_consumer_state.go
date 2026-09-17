package feishu

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

type EventConsumerStateStore struct{ path string }

func NewEventConsumerStateStore(dataRoot string) EventConsumerStateStore {
	return EventConsumerStateStore{path: filepath.Join(dataRoot, "logs", "event-consumer-state.json")}
}

func (store EventConsumerStateStore) UpdateConnection(state string) error {
	return store.update(func(value map[string]any, now string) {
		status := state
		if state == "disconnected" {
			status = "stopped"
		}
		for key, status := range feishuprotocol.ManagedEventConsumerStatus() {
			value[key] = status
		}
		value["status"] = status
		value["errorCode"] = ""
		connection, _ := value["connection"].(map[string]any)
		if connection == nil {
			connection = map[string]any{}
		}
		connection["state"] = state
		if state == "connected" {
			connection["lastConnectTime"] = time.Now().UnixMilli()
			connection["reconnectAttempts"] = 0
		}
		value["connection"] = connection
		events := eventStateMap(value)
		for _, key := range FixedEventKeys {
			item, _ := events[key].(map[string]any)
			if item == nil {
				item = map[string]any{}
			}
			if state == "connected" {
				item["status"] = "running"
				item["errorCode"] = ""
				item["restartScheduled"] = false
			} else if state == "starting" || state == "reconnecting" {
				item["status"] = state
			} else {
				item["status"] = "stopped"
			}
			item["updatedAt"] = now
			if !contains(CLIManagedEventKeys, key) {
				item["status"] = "not_enabled"
				item["errorCode"] = "explicit_subscription_required"
				if key == MailMessageReceivedEvent {
					item["status"] = "unsupported"
					item["errorCode"] = "unsupported_by_pinned_cli"
				}
			}
			events[key] = item
		}
		value["events"] = events
	})
}

func (store EventConsumerStateStore) MarkReceived(eventKey string) error {
	if !contains(FixedEventKeys, eventKey) {
		return errors.New("unsupported_event_key")
	}
	return store.update(func(value map[string]any, now string) {
		events := eventStateMap(value)
		item, _ := events[eventKey].(map[string]any)
		if item == nil {
			item = map[string]any{}
		}
		item["status"] = "running"
		item["lastReceivedAt"] = now
		item["updatedAt"] = now
		events[eventKey] = item
		value["events"] = events
	})
}

func (store EventConsumerStateStore) Read() (map[string]any, error) {
	value := map[string]any{}
	missing, err := readPrivateJSON(store.path, &value)
	if missing {
		return map[string]any{}, nil
	}
	return value, err
}

func (store EventConsumerStateStore) update(change func(map[string]any, string)) error {
	return withProcessFileLock(store.path+".lock", func() error {
		value := map[string]any{}
		missing, err := readPrivateJSON(store.path, &value)
		if err != nil && !missing && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		value["schemaVersion"] = 3
		value["transport"] = "official-sdk"
		value["updatedAt"] = now
		change(value, now)
		return writePrivateJSON(store.path, value)
	})
}

func eventStateMap(value map[string]any) map[string]any {
	events, _ := value["events"].(map[string]any)
	if events == nil {
		events = map[string]any{}
	}
	return events
}
