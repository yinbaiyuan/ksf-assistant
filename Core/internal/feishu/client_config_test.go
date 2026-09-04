package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClientConfigV4PreservesUnknownTopLevelFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "client.json")
	value := `{"schemaVersion":4,"messageTargets":{"self":{"type":"open_id","id":"ou_secret"}},"future":{"enabled":true}}`
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewClientConfigStore(root)
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SetDocumentTarget("doc", DocumentTarget{Kind: "docx_token", Value: "doc_secret"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	var future map[string]bool
	if err := json.Unmarshal(raw["future"], &future); err != nil || !future["enabled"] {
		t.Fatalf("unknown field lost: %s", data)
	}
}

func TestPublicClientTargetsNeverExposeIdentifiers(t *testing.T) {
	config := DefaultClientConfig()
	config.MessageTargets["self"] = MessageTarget{Type: "open_id", ID: "ou_secret"}
	config.DocumentTargets["doc"] = DocumentTarget{Kind: "docx_token", Value: "doc_secret"}
	data, err := json.Marshal(PublicClientTargets(config))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || containsText(string(data), "ou_secret") || containsText(string(data), "doc_secret") {
		t.Fatalf("public targets leaked identifiers: %s", data)
	}
}

func containsText(value, wanted string) bool {
	for i := 0; i+len(wanted) <= len(value); i++ {
		if value[i:i+len(wanted)] == wanted {
			return true
		}
	}
	return false
}
