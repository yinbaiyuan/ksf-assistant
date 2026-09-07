package tokens

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// Turn identities distinguish a replayed prefix from a fork's new execution.
// Index all filenames (including old ancestors), but load only referenced turns.
// This catalog is scan-local and never stores prompts or persists identities.
type turnCatalog struct {
	paths  map[string]string
	loaded map[string]map[string]bool
}

func newTurnCatalog(paths []string) *turnCatalog {
	value := &turnCatalog{paths: map[string]string{}, loaded: map[string]map[string]bool{}}
	for _, path := range paths {
		value.paths[sessionIdentity(path)] = path
	}
	return value
}

func (catalog *turnCatalog) turns(id string) map[string]bool {
	if value, ok := catalog.loaded[id]; ok {
		return value
	}
	catalog.loaded[id] = nil
	path := catalog.paths[id]
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	value := map[string]bool{}
	for {
		line, err := reader.ReadBytes('\n')
		if strings.Contains(string(line), `"turn_context"`) || strings.Contains(string(line), `"task_started"`) {
			var event struct {
				Type    string `json:"type"`
				Payload struct {
					Type   string `json:"type"`
					TurnID string `json:"turn_id"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &event) == nil && (event.Type == "turn_context" || event.Type == "event_msg" && event.Payload.Type == "task_started") && event.Payload.TurnID != "" {
				value[event.Payload.TurnID] = true
			}
		}
		if err != nil {
			if err != io.EOF {
				return nil // An incomplete source cannot prove a turn is new.
			}
			break
		}
	}
	catalog.loaded[id] = value
	return value
}
