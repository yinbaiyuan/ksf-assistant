package feishu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditNeverStoresRawTargetsContentOrSecrets(t *testing.T) {
	root := t.TempDir()
	target := "ou_private_target"
	body := "private message body"
	if err := NewAuditLog(root).Record("outbox_result", map[string]any{"target": AuditTargetDescriptor(MessageTarget{Type: "open_id", ID: target}), "content": AuditContentDescriptor(body), "error": "Authorization: Bearer private-token"}); err != nil {
		t.Fatal(err)
	}
	machineFiles, err := filepath.Glob(filepath.Join(root, "logs", "audit-machine", "*.jsonl"))
	if err != nil || len(machineFiles) != 1 {
		t.Fatalf("missing machine audit: %v %v", err, machineFiles)
	}
	machine, err := os.ReadFile(machineFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	auditEntries, err := os.ReadDir(filepath.Join(root, "logs", "audit"))
	if err != nil {
		t.Fatalf("missing audit: %v", err)
	}
	name := ""
	for _, entry := range auditEntries {
		if filepath.Ext(entry.Name()) == ".md" {
			name = entry.Name()
		}
	}
	if name == "" {
		t.Fatal("missing audit markdown")
	}
	human, err := os.ReadFile(filepath.Join(root, "logs", "audit", name))
	if err != nil {
		t.Fatal(err)
	}
	combined := string(machine) + string(human)
	if strings.Contains(combined, target) || strings.Contains(combined, body) || strings.Contains(combined, "private-token") {
		t.Fatalf("private value leaked into audit: %s", combined)
	}
	if !strings.Contains(combined, "sha256:") || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("audit descriptors missing: %s", combined)
	}
}

func TestAuditRecursivelyRedactsNestedSecrets(t *testing.T) {
	root := t.TempDir()
	secret := "sk-1234567890abcdefghijklmnop"
	if err := NewAuditLog(root).Record("nested", map[string]any{
		"result": map[string]any{"items": []any{map[string]any{"authorization": "Bearer " + secret, "secret": "secret=" + secret}}},
	}); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "logs", "audit-machine", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files=%#v err=%v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("nested secret leaked: %s", data)
	}
}
