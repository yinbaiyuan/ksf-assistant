package feishu

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type EventRecord struct {
	SchemaVersion           int    `json:"schemaVersion"`
	EventKey                string `json:"eventKey"`
	EventFingerprint        string `json:"eventFingerprint"`
	ActorFingerprint        string `json:"actorFingerprint"`
	ResourceFingerprint     string `json:"resourceFingerprint"`
	OccurredAt              string `json:"occurredAt"`
	ReceivedAt              string `json:"receivedAt"`
	ContentLength           int    `json:"contentLength"`
	ContentFingerprint      string `json:"contentFingerprint"`
	TranscriptContentStored bool   `json:"transcriptContentStored"`
	TranscriptEvent         bool   `json:"transcriptEvent"`
}

func (inbox *EventInbox) Status() (map[string]any, error) {
	state := eventInboxState{SchemaVersion: 1, Processed: map[string]string{}}
	missing, err := readPrivateJSON(filepath.Join(inbox.root, "state.json"), &state)
	if err != nil {
		return nil, err
	}
	if missing {
		return map[string]any{"schemaVersion": 1, "received": 0, "duplicates": 0, "processedCount": 0}, nil
	}
	return map[string]any{"schemaVersion": state.SchemaVersion, "received": state.Received, "duplicates": state.Duplicates, "processedCount": len(state.Processed), "lastReceivedAt": state.LastReceivedAt, "lastDuplicateAt": state.LastDuplicateAt, "lastEventKey": state.LastEventKey, "lastError": state.LastError}, nil
}

func (inbox *EventInbox) Recent(limit int) ([]EventRecord, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	files, err := filepath.Glob(filepath.Join(inbox.root, "events-*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	values := []EventRecord{}
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var record EventRecord
			if json.Unmarshal(scanner.Bytes(), &record) == nil {
				values = append(values, record)
			}
		}
		err = scanner.Err()
		_ = file.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
	return values, nil
}

func (inbox *EventInbox) Get(fingerprint string) (EventRecord, bool, error) {
	values, err := inbox.Recent(100)
	if err != nil {
		return EventRecord{}, false, err
	}
	for _, value := range values {
		if value.EventFingerprint == fingerprint {
			return value, true, nil
		}
	}
	return EventRecord{}, false, nil
}

type eventInboxState struct {
	SchemaVersion   int               `json:"schemaVersion"`
	Processed       map[string]string `json:"processed"`
	Received        int               `json:"received"`
	Duplicates      int               `json:"duplicates"`
	LastReceivedAt  string            `json:"lastReceivedAt,omitempty"`
	LastDuplicateAt string            `json:"lastDuplicateAt,omitempty"`
	LastEventKey    string            `json:"lastEventKey,omitempty"`
	LastError       string            `json:"lastError,omitempty"`
}
type EventInbox struct {
	root string
	mu   sync.Mutex
}

func NewEventInbox(dataRoot string) *EventInbox {
	return &EventInbox{root: filepath.Join(dataRoot, "events")}
}
func (inbox *EventInbox) Put(eventKey string, payload []byte) (EventRecord, bool, error) {
	if !contains(FixedEventKeys, eventKey) {
		return EventRecord{}, false, errors.New("unsupported event key")
	}
	var raw any
	if json.Unmarshal(payload, &raw) != nil {
		return EventRecord{}, false, errors.New("invalid event payload")
	}
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	eventID := firstNestedValue(raw, []string{"event_id", "eventId", "uuid", "message_id"})
	if eventID == "" {
		return EventRecord{}, false, errors.New("event_id_missing")
	}
	actor := firstNestedValue(raw, []string{"operator_id", "open_id", "user_id", "sender_id", "participant_id"})
	resource := firstNestedValue(raw, []string{"chat_id", "message_id", "task_guid", "whiteboard_id", "whiteboard_token", "meeting_id", "note_id", "minute_token", "recording_id", "app_id"})
	occurred := firstNestedValue(raw, []string{"create_time", "event_time", "timestamp", "update_time"})
	if occurred == "" {
		occurred = receivedAt
	}
	text := eventText(raw, "")
	record := EventRecord{SchemaVersion: 1, EventKey: eventKey, EventFingerprint: eventFingerprint(eventID), ActorFingerprint: eventFingerprint(actor), ResourceFingerprint: eventFingerprint(resource), OccurredAt: occurred, ReceivedAt: receivedAt, ContentLength: len([]rune(text)), ContentFingerprint: eventFingerprint(text), TranscriptEvent: eventKey == "vc.recording.recording_transcript_generated_v1"}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	state := eventInboxState{SchemaVersion: 1, Processed: map[string]string{}}
	if missing, err := readPrivateJSON(filepath.Join(inbox.root, "state.json"), &state); err != nil && !missing {
		return EventRecord{}, false, err
	}
	if state.Processed == nil {
		state.Processed = map[string]string{}
	}
	if state.Processed[record.EventFingerprint] != "" {
		state.Duplicates++
		state.LastDuplicateAt = receivedAt
		if err := writePrivateJSON(filepath.Join(inbox.root, "state.json"), state); err != nil {
			return EventRecord{}, false, err
		}
		return record, false, nil
	}
	if err := ensurePrivateDirectory(inbox.root); err != nil {
		return EventRecord{}, false, err
	}
	if err := appendPrivateJSONL(filepath.Join(inbox.root, "events-"+receivedAt[:10]+".jsonl"), record); err != nil {
		return EventRecord{}, false, err
	}
	state.Processed[record.EventFingerprint] = receivedAt
	if len(state.Processed) > 10000 {
		trimEventState(state.Processed, 10000)
	}
	state.Received++
	state.LastReceivedAt = receivedAt
	state.LastEventKey = eventKey
	state.LastError = ""
	if err := writePrivateJSON(filepath.Join(inbox.root, "state.json"), state); err != nil {
		return EventRecord{}, false, err
	}
	return record, true, nil
}
func eventFingerprint(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:20]
}
func firstNestedValue(value any, keys []string) string {
	for _, key := range keys {
		if found := nestedScalarForKey(value, key); found != "" {
			return found
		}
	}
	return ""
}
func nestedScalarForKey(value any, wanted string) string {
	switch current := value.(type) {
	case map[string]any:
		if item, ok := current[wanted]; ok && item != nil && fmt.Sprint(item) != "" {
			return fmt.Sprint(item)
		}
		for _, child := range current {
			if found := nestedScalarForKey(child, wanted); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range current {
			if found := nestedScalarForKey(child, wanted); found != "" {
				return found
			}
		}
	}
	return ""
}
func eventText(value any, key string) string {
	values := []string{}
	switch current := value.(type) {
	case map[string]any:
		for childKey, child := range current {
			if text := eventText(child, childKey); text != "" {
				values = append(values, text)
			}
		}
	case []any:
		for _, child := range current {
			if text := eventText(child, key); text != "" {
				values = append(values, text)
			}
		}
	case string:
		lower := strings.ToLower(key)
		if strings.Contains(lower, "text") || strings.Contains(lower, "content") || strings.Contains(lower, "transcript") || strings.Contains(lower, "word") || strings.Contains(lower, "sentence") {
			values = append(values, current)
		}
	}
	return strings.Join(values, "\n")
}
func trimEventState(values map[string]string, limit int) {
	type entry struct{ key, value string }
	items := make([]entry, 0, len(values))
	for key, value := range values {
		items = append(items, entry{key, value})
	}
	for len(items) > limit {
		oldest := 0
		for index := 1; index < len(items); index++ {
			if items[index].value < items[oldest].value {
				oldest = index
			}
		}
		delete(values, items[oldest].key)
		items = append(items[:oldest], items[oldest+1:]...)
	}
}
