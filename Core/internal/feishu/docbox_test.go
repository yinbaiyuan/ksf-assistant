package feishu

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDocboxUpdateCreatesOfficialVersionBeforeWriteAndRereads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	root := t.TempDir()
	logPath := filepath.Join(root, "calls")
	bin := filepath.Join(root, "fake-lark")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$*" in
 *+fetch*) printf '{"data":{"file_token":"doc_test"}}\n' ;;
 *versions*) printf '{"data":{"version_id":"v1"}}\n' ;;
 *+update*) printf '{"data":{"revision_id":"2"}}\n' ;;
 *) printf '{}\n' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	request := DocumentRequest{ID: "DOC-test", Type: "document_task", Action: "update_document", Identity: "user", Target: &DocumentTarget{Kind: "docx_token", Value: "doc_test"}, Content: DocumentContent{Format: "markdown", Text: "new"}, Instruction: "update", ExplicitAuthorization: true, Source: "test", VersionPolicy: "official_before_update", UpdateMode: "append", CreatedAt: time.Now().UTC()}
	result := executeDocumentRequest(context.Background(), CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root}, request, false)
	if result.Status != "completed" || !result.Verified {
		t.Fatalf("unexpected result: %#v", result)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 || !strings.Contains(lines[1], "versions") || !strings.Contains(lines[2], "+update") {
		t.Fatalf("unsafe document sequence: %#v", lines)
	}
}
