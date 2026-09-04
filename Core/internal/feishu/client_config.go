package feishu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const ClientConfigSchemaVersion = 4

type MessageTarget struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type DocumentTarget struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type ClientConfig struct {
	SchemaVersion        int                        `json:"schemaVersion"`
	LaunchdLabel         string                     `json:"launchdLabel"`
	WindowsTaskName      string                     `json:"windowsTaskName"`
	DefaultSource        string                     `json:"defaultSource"`
	MessageTargets       map[string]MessageTarget   `json:"messageTargets"`
	DirectAllowedAliases []string                   `json:"directAllowedAliases"`
	NameBindings         map[string]map[string]any  `json:"nameBindings"`
	GroupNameBindings    map[string]map[string]any  `json:"groupNameBindings"`
	DocumentTargets      map[string]DocumentTarget  `json:"documentTargets"`
	TestAssets           map[string]map[string]any  `json:"testAssets"`
	EventWatches         []any                      `json:"eventWatches"`
	Extra                map[string]json.RawMessage `json:"-"`
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		SchemaVersion: ClientConfigSchemaVersion, LaunchdLabel: "com.example.feishu-bot-bridge",
		WindowsTaskName: "FeishuBotBridge", DefaultSource: "codex",
		MessageTargets: map[string]MessageTarget{}, NameBindings: map[string]map[string]any{},
		GroupNameBindings: map[string]map[string]any{}, DocumentTargets: map[string]DocumentTarget{},
		TestAssets: map[string]map[string]any{}, DirectAllowedAliases: []string{}, EventWatches: []any{}, Extra: map[string]json.RawMessage{},
	}
}

func (config *ClientConfig) UnmarshalJSON(data []byte) error {
	type plain ClientConfig
	defaults := DefaultClientConfig()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(data, (*plain)(&defaults)); err != nil {
		return err
	}
	for _, key := range []string{"schemaVersion", "launchdLabel", "windowsTaskName", "defaultSource", "messageTargets", "directAllowedAliases", "nameBindings", "groupNameBindings", "documentTargets", "testAssets", "eventWatches"} {
		delete(raw, key)
	}
	defaults.Extra = raw
	*config = defaults
	return config.normalize()
}

func (config ClientConfig) MarshalJSON() ([]byte, error) {
	type plain ClientConfig
	known, err := json.Marshal(plain(config))
	if err != nil {
		return nil, err
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(known, &value); err != nil {
		return nil, err
	}
	delete(value, "Extra")
	for key, item := range config.Extra {
		if _, exists := value[key]; !exists {
			value[key] = item
		}
	}
	return json.Marshal(value)
}

func (config *ClientConfig) normalize() error {
	if config.SchemaVersion < ClientConfigSchemaVersion {
		config.SchemaVersion = ClientConfigSchemaVersion
	}
	if config.MessageTargets == nil {
		config.MessageTargets = map[string]MessageTarget{}
	}
	if config.NameBindings == nil {
		config.NameBindings = map[string]map[string]any{}
	}
	if config.GroupNameBindings == nil {
		config.GroupNameBindings = map[string]map[string]any{}
	}
	if config.DocumentTargets == nil {
		config.DocumentTargets = map[string]DocumentTarget{}
	}
	if config.TestAssets == nil {
		config.TestAssets = map[string]map[string]any{}
	}
	seen := map[string]bool{}
	aliases := config.DirectAllowedAliases[:0]
	for _, alias := range config.DirectAllowedAliases {
		if alias != "" && len(alias) <= 100 && !seen[alias] {
			seen[alias] = true
			aliases = append(aliases, alias)
		}
	}
	config.DirectAllowedAliases = aliases
	return nil
}

type ClientConfigStore struct{ path string }

func NewClientConfigStore(dataRoot string) ClientConfigStore {
	return ClientConfigStore{path: filepath.Join(dataRoot, "client.json")}
}

func NewClientConfigStorePath(path string) ClientConfigStore { return ClientConfigStore{path: path} }

func (store ClientConfigStore) Path() string { return store.path }

func (store ClientConfigStore) Load() (ClientConfig, error) {
	config := DefaultClientConfig()
	missing, err := readPrivateJSON(store.path, &config)
	if missing {
		return config, nil
	}
	if err != nil {
		return ClientConfig{}, err
	}
	return config, config.normalize()
}

func (store ClientConfigStore) Save(config ClientConfig) error {
	if err := config.normalize(); err != nil {
		return err
	}
	return withProcessFileLock(store.path+".lock", func() error { return writePrivateJSON(store.path, config) })
}

func (config ClientConfig) SetMessageTarget(alias string, target MessageTarget) error {
	if strings.TrimSpace(alias) == "" || len(alias) > 100 {
		return errors.New("invalid_target_alias")
	}
	if target.Type != "chat_id" && target.Type != "open_id" {
		return errors.New("message target type must be chat_id or open_id")
	}
	if strings.TrimSpace(target.ID) == "" || len(target.ID) > 4000 {
		return errors.New("invalid_message_target")
	}
	config.MessageTargets[alias] = target
	return nil
}

func (config ClientConfig) SetDocumentTarget(alias string, target DocumentTarget) error {
	if strings.TrimSpace(alias) == "" || len(alias) > 100 {
		return errors.New("invalid_target_alias")
	}
	if !contains([]string{"url", "docx_token", "wiki_url", "wiki_token", "folder_token"}, target.Kind) {
		return errors.New("unsupported_document_target_kind")
	}
	if strings.TrimSpace(target.Value) == "" || len(target.Value) > 4000 {
		return errors.New("invalid_document_target")
	}
	config.DocumentTargets[alias] = target
	return nil
}

func (config ClientConfig) ResolveMessageTarget(value string) (MessageTarget, error) {
	if target, ok := config.MessageTargets[value]; ok {
		return target, nil
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || (parts[0] != "chat_id" && parts[0] != "open_id") || strings.TrimSpace(parts[1]) == "" {
		return MessageTarget{}, errors.New("message target must be a configured alias or chat_id:<id>/open_id:<id>")
	}
	return MessageTarget{Type: parts[0], ID: parts[1]}, nil
}

func (config ClientConfig) ResolveDocumentTarget(value string) (DocumentTarget, error) {
	if target, ok := config.DocumentTargets[value]; ok {
		return target, nil
	}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		kind := "url"
		if strings.Contains(value, "/wiki/") {
			kind = "wiki_url"
		}
		return DocumentTarget{Kind: kind, Value: value}, nil
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 || !contains([]string{"url", "docx_token", "wiki_url", "wiki_token", "folder_token"}, parts[0]) || strings.TrimSpace(parts[1]) == "" {
		return DocumentTarget{}, errors.New("document target must be an alias, URL, or kind:value")
	}
	return DocumentTarget{Kind: parts[0], Value: parts[1]}, nil
}

func PublicClientTargets(config ClientConfig) map[string]any {
	messages := make([]map[string]any, 0, len(config.MessageTargets))
	for _, alias := range sortedKeys(config.MessageTargets) {
		target := config.MessageTargets[alias]
		messages = append(messages, map[string]any{"alias": alias, "type": target.Type, "idFingerprint": fingerprintIdentifier(target.ID), "taskLinkEligible": target.Type == "open_id" && contains(config.DirectAllowedAliases, alias)})
	}
	documents := make([]map[string]any, 0, len(config.DocumentTargets))
	for _, alias := range sortedKeys(config.DocumentTargets) {
		target := config.DocumentTargets[alias]
		documents = append(documents, map[string]any{"alias": alias, "kind": target.Kind, "valueFingerprint": fingerprintIdentifier(target.Value)})
	}
	return map[string]any{"messages": messages, "nameBindings": publicBindings(config.NameBindings), "groupNameBindings": publicBindings(config.GroupNameBindings), "documents": documents, "testAssets": publicTestAssets(config.TestAssets)}
}

func fingerprintIdentifier(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func publicBindings(values map[string]map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(values))
	for _, key := range sortedKeys(values) {
		value := values[key]
		result = append(result, map[string]any{"name": fallbackString(value["name"], key), "type": fmt.Sprint(value["type"]), "idFingerprint": fingerprintIdentifier(fmt.Sprint(value["id"])), "boundAt": value["boundAt"]})
	}
	return result
}

func publicTestAssets(values map[string]map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(values))
	for _, alias := range sortedKeys(values) {
		value := values[alias]
		result = append(result, map[string]any{"alias": alias, "kind": value["kind"], "valueFingerprint": fingerprintIdentifier(fmt.Sprint(value["value"])), "capabilityId": value["capabilityId"], "savedAt": value["savedAt"]})
	}
	return result
}

func fallbackString(value any, fallback string) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return fallback
	}
	return text
}
