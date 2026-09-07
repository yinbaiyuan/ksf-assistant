package feishucli

import (
	_ "embed"
	"encoding/json"
	"errors"

	"ksfassistant/core/internal/feishuprotocol"
)

//go:embed catalog.json
var catalogJSON []byte

type staticField struct {
	Type   string   `json:"type"`
	Output bool     `json:"output"`
	Path   bool     `json:"path"`
	Values []string `json:"values"`
}
type staticBundle struct {
	Fields       map[string]map[string]staticField `json:"fields"`
	Catalog      []map[string]any                  `json:"catalog"`
	Capabilities json.RawMessage                   `json:"capabilities"`
	Events       json.RawMessage                   `json:"events"`
}

var bundle, bundleError = loadStatic()

func loadStatic() (staticBundle, error) {
	var value staticBundle
	err := json.Unmarshal(catalogJSON, &value)
	if err == nil && (len(value.Fields) == 0 || len(value.Catalog) == 0) {
		err = errors.New("invalid embedded CLI catalog")
	}
	return value, err
}
func capabilityFields(id string) (map[string]staticField, error) {
	if bundleError != nil {
		return nil, bundleError
	}
	fields, ok := bundle.Fields[id]
	if !ok {
		return nil, errors.New("unknown or unpublished capability")
	}
	return fields, nil
}
func Static(request Request) (any, bool, error) {
	if err := Validate(request); err != nil {
		return nil, false, err
	}
	switch request.Command {
	case "help":
		return map[string]any{"usage": []string{"ksf-assistant-feishu-bridge client snapshot", "ksf-assistant-feishu-bridge client status|doctor", "ksf-assistant-feishu-bridge client targets init|list|set|remove", "ksf-assistant-feishu-bridge client send ...", "ksf-assistant-feishu-bridge client task-link protocol|list|create|status|interrupt|release", "ksf-assistant-feishu-bridge client capability catalog|get|read|write", "ksf-assistant-feishu-bridge client operation prepare|confirm|cancel|status", "ksf-assistant-feishu-bridge client policy read|update", "ksf-assistant-feishu-bridge client events catalog|status|recent|get", "ksf-assistant-feishu-bridge client result <outbox|actionbox> <id>", "ksf-assistant-feishu-bridge client recent <outbox|actionbox|messages|audit>"}}, true, nil
	case "version":
		return map[string]any{"version": Version}, true, nil
	case "capabilities":
		return append(json.RawMessage(nil), bundle.Capabilities...), true, bundleError
	case "profile":
		if request.Action == "catalog" {
			return map[string]any{"status": "ok", "profiles": []any{}, "eventConsumer": feishuprotocol.ManagedEventConsumerStatus()}, true, nil
		}
	case "events":
		if request.Action == "catalog" {
			return append(json.RawMessage(nil), bundle.Events...), true, bundleError
		}
	case "capability":
		if request.Action == "catalog" {
			if bundleError != nil {
				return nil, true, bundleError
			}
			items := []any{}
			for _, definition := range bundle.Catalog {
				if request.Options["domain"] == "" || definition["domain"] == request.Options["domain"] {
					items = append(items, definition)
				}
			}
			return cloneStatic(map[string]any{"status": "ok", "count": len(items), "capabilities": items})
		}
		if request.Action == "get" {
			for _, definition := range bundle.Catalog {
				if definition["id"] == request.Positionals[0] {
					return cloneStatic(map[string]any{"status": "ok", "capability": definition})
				}
			}
		}
	}
	return nil, false, nil
}
func cloneStatic(value any) (any, bool, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, true, err
	}
	return json.RawMessage(encoded), true, nil
}
