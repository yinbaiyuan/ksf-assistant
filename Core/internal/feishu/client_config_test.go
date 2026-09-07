package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClientConfigStaleSavePreservesOperatorAndUnknownFields(t *testing.T) {
	store := NewClientConfigStore(t.TempDir())
	stale, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	operator, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	operator.MessageTargets["我"] = MessageTarget{Type: "open_id", ID: "ou_fixture"}
	operator.DirectAllowedAliases = []string{"我"}
	operator.Extra["future"] = json.RawMessage(`{"enabled":true}`)
	if err := store.Save(operator); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	stale.NameBindings["fixture"] = map[string]any{"id": "ou_other"}
	for _, config := range []ClientConfig{stale, DefaultClientConfig()} {
		if err := store.Save(config); !errors.Is(err, ErrClientConfigConflict) {
			t.Fatalf("stale/default save did not conflict: %v", err)
		}
	}
	after, err := os.ReadFile(store.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("conflicting write changed persisted operator configuration")
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	reloaded.NameBindings["fixture"] = map[string]any{"id": "ou_other"}
	if err := store.Save(reloaded); err != nil {
		t.Fatal(err)
	}
	final, err := store.Load()
	if err != nil || final.MessageTargets["我"].ID != "ou_fixture" || !contains(final.DirectAllowedAliases, "我") || string(final.Extra["future"]) == "" || final.NameBindings["fixture"] == nil {
		t.Fatalf("reload did not preserve independent fields: %+v %v", final, err)
	}
}

func TestClientConfigBaselineIsValueScopedAndNotSerialized(t *testing.T) {
	store := NewClientConfigStore(t.TempDir())
	if err := store.Save(DefaultClientConfig()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	sibling := loaded
	loaded.DefaultSource = "first"
	if err := store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	for _, config := range []ClientConfig{loaded, sibling} {
		config.DefaultSource = "stale-second"
		if err := store.Save(config); !errors.Is(err, ErrClientConfigConflict) {
			t.Fatalf("saving updated a shared baseline: %v", err)
		}
	}
	data, err := json.Marshal(loaded)
	if err != nil || strings.Contains(string(data), "baseline") || strings.Contains(string(data), "Fingerprint") {
		t.Fatalf("private concurrency metadata leaked: %s %v", data, err)
	}
	var roundTripped ClientConfig
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(roundTripped); !errors.Is(err, ErrClientConfigConflict) {
		t.Fatalf("unloaded JSON gained overwrite authority: %v", err)
	}
}

func TestClientConfigExternalChangesAreNotOverwritten(t *testing.T) {
	for _, change := range []string{"changed", "deleted", "malformed", "directory"} {
		t.Run(change, func(t *testing.T) {
			store := NewClientConfigStore(t.TempDir())
			if err := store.Save(DefaultClientConfig()); err != nil {
				t.Fatal(err)
			}
			config, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			config.DefaultSource = "stale"
			var content []byte
			switch change {
			case "changed":
				content = []byte(`{"schemaVersion":4,"future":"external"}`)
			case "malformed":
				content = []byte(`broken`)
			default:
				if err := os.Remove(store.Path()); err != nil {
					t.Fatal(err)
				}
				if change == "directory" {
					if err := os.Mkdir(store.Path(), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if content != nil {
				if err := os.WriteFile(store.Path(), content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Save(config); err == nil {
				t.Fatal("external change accepted for overwrite")
			}
			if content != nil {
				after, err := os.ReadFile(store.Path())
				if err != nil || !bytes.Equal(after, content) {
					t.Fatal("external bytes changed")
				}
			} else if info, err := os.Stat(store.Path()); change == "deleted" && !os.IsNotExist(err) || change == "directory" && (err != nil || !info.IsDir()) {
				t.Fatal("external deletion or directory was replaced")
			}
		})
	}
}

func TestClientConfigCASProcess(t *testing.T) {
	root, role := os.Getenv("KSF_CLIENT_CONFIG_CAS_ROOT"), os.Getenv("KSF_CLIENT_CONFIG_CAS_ROLE")
	if root == "" {
		return
	}
	if !filepath.IsAbs(root) || role != "first" && role != "second" {
		t.Fatal("invalid isolated fixture")
	}
	store := NewClientConfigStore(root)
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, role+".ready"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "start")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture barrier timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	config.DefaultSource = role
	result := "saved"
	if err := store.Save(config); errors.Is(err, ErrClientConfigConflict) {
		result = "conflict"
	} else if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, role+".result"), []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClientConfigCASAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	store := NewClientConfigStore(root)
	if err := store.Save(DefaultClientConfig()); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var processes []*exec.Cmd
	var outputs []*bytes.Buffer
	defer func() {
		cancel()
		for _, command := range processes {
			_ = command.Wait()
		}
	}()
	for _, role := range []string{"first", "second"} {
		command := exec.CommandContext(ctx, executable, "-test.run=^TestClientConfigCASProcess$")
		command.Env = append(os.Environ(), "KSF_CLIENT_CONFIG_CAS_ROOT="+root, "KSF_CLIENT_CONFIG_CAS_ROLE="+role)
		output := &bytes.Buffer{}
		command.Stdout, command.Stderr = output, output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		processes, outputs = append(processes, command), append(outputs, output)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, firstErr := os.Stat(filepath.Join(root, "first.ready"))
		_, secondErr := os.Stat(filepath.Join(root, "second.ready"))
		if firstErr == nil && secondErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child processes did not reach barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(root, "start"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for index, command := range processes {
		if err := command.Wait(); err != nil {
			t.Fatalf("child failed: %v %s", err, outputs[index])
		}
	}
	winner := ""
	for _, role := range []string{"first", "second"} {
		result, err := os.ReadFile(filepath.Join(root, role+".result"))
		if err != nil {
			t.Fatal(err)
		}
		if string(result) == "saved" && winner == "" {
			winner = role
		} else if string(result) != "conflict" {
			t.Fatalf("expected exactly one successful writer: %s", result)
		}
	}
	final, err := store.Load()
	if err != nil || winner == "" || final.DefaultSource != winner {
		t.Fatalf("winner not retained: %s %+v %v", winner, final, err)
	}
}

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
