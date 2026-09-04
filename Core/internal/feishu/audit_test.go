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
	machine, err := os.ReadFile(filepath.Join(root, "logs", "messages.jsonl"))
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
