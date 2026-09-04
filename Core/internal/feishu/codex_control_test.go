package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenCodexControlFixture(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "protocol", "fixtures", "codex-control-v1.valid.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var request CodexControlRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCodexControlRequest(request); err != nil {
		t.Fatal(err)
	}
}
func TestCodexControlRequiresExplicitBridgeAuthorization(t *testing.T) {
	request := CodexControlRequest{Protocol: CodexControlProtocol, RequestID: "request_001", Operation: "turn.interrupt", TaskKey: "task_0123456789abcdef", Payload: map[string]any{}}
	if err := ValidateCodexControlRequest(request); err == nil {
		t.Fatal("unauthorized control accepted")
	}
}
