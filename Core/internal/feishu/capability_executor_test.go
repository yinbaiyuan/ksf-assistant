package feishu

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCapabilityInvocationKeepsPrivateTextOutOfArguments(t *testing.T) {
	definition, ok := CapabilityByID("im.message.reply")
	if !ok {
		t.Fatal("missing capability")
	}
	args, stdin, files, err := capabilityInvocation(definition, map[string]any{"message-id": "om_test", "text": "private body"})
	if err != nil {
		t.Fatal(err)
	}
	if string(stdin) != "private body" {
		t.Fatalf("private body not routed to stdin: %q", stdin)
	}
	if strings.Contains(strings.Join(args, " "), "private body") || len(files) != 0 {
		t.Fatalf("private body leaked into invocation: %#v", args)
	}
}

func TestCapabilityValidationFailsClosed(t *testing.T) {
	definition, _ := CapabilityByID("im.message.reply")
	for _, input := range []map[string]any{{"message-id": "om_test", "text": "ok", "unknown": "x"}, {"message-id": "../escape", "text": "ok"}, {"message-id": "om_test", "text": "ok", "markdown": "also"}} {
		if err := validateCapabilityInput(definition, input); err == nil {
			t.Fatalf("input unexpectedly accepted: %#v", input)
		}
	}
}

func TestRawCapabilityUsesPrivatePayloadFiles(t *testing.T) {
	definition, ok := CapabilityByID("apps.app.create")
	if !ok {
		t.Fatal("missing capability")
	}
	args, _, files, err := capabilityInvocation(definition, map[string]any{"name": "private app"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "private app") || len(files) == 0 {
		t.Fatalf("raw body leaked: args=%#v files=%d", args, len(files))
	}
}

func TestForbiddenPayloadTraversesArrays(t *testing.T) {
	if !forbiddenPayload(map[string]any{"items": []any{map[string]any{"operation": "delete"}}}) {
		t.Fatal("nested destructive operation accepted")
	}
}

func TestExecuteRunsPreflightWriteAndReread(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	logPath := filepath.Join(root, "calls.log")
	bin := filepath.Join(root, "fake-lark-cli")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$*" in
  *markdown\ +fetch*) printf '{"data":{"content":"before"}}\n' ;;
  *markdown\ +overwrite*) printf '{"data":{"file_token":"fm_test"}}\n' ;;
  *) printf '{}\n' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := CapabilityExecutor{Binary: bin, Profile: "test", DataRoot: root, WorkingDirectory: root}
	result, err := runner.Execute(context.Background(), "markdown.overwrite", map[string]any{
		"file-token": "fm_test", "content": "replacement",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["verified"] != true || result["preflight"] == nil || result["verification"] == nil {
		t.Fatalf("execution did not preserve verification envelope: %#v", result)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "markdown +overwrite") {
		t.Fatalf("unexpected execution sequence: %q", lines)
	}
	if strings.Contains(string(calls), "replacement") {
		t.Fatal("private replacement leaked into arguments")
	}
}

func TestMappedCapabilityInputReadsNestedAndRecursiveResult(t *testing.T) {
	step := &CapabilityStep{Map: map[string]string{"release-id": "$result.release_id"}}
	value := mappedCapabilityInput(step, nil, map[string]any{"data": map[string]any{"release_id": "rel_1"}})
	if value["release-id"] != "rel_1" {
		t.Fatalf("result mapping failed: %#v", value)
	}
}

func TestDocWhiteboardRejectsActiveSVG(t *testing.T) {
	_, err := docWhiteboardXML(map[string]any{"doc-format": "svg", "content": `<svg><script>alert(1)</script></svg>`})
	if err == nil || err.Error() != "unsafe_doc_whiteboard_svg" {
		t.Fatalf("unsafe svg accepted: %v", err)
	}
}

func TestCapabilityPathPreparationRejectsSymlinkInputAndOutputParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	input := filepath.Join(outside, "input.txt")
	if err := os.WriteFile(input, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(input, filepath.Join(root, "input.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "output")); err != nil {
		t.Fatal(err)
	}
	runner := CapabilityExecutor{DataRoot: root, WorkingDirectory: root}
	inputDefinition := CapabilityDefinition{Flags: map[string]CapabilityField{"file": {Type: "path"}}}
	if _, err := runner.prepareCapabilityPaths(inputDefinition, map[string]any{"file": "input.txt"}); err == nil {
		t.Fatal("symlink input was accepted")
	}
	outputDefinition := CapabilityDefinition{Flags: map[string]CapabilityField{"file": {Type: "path", Output: true}}}
	if _, err := runner.prepareCapabilityPaths(outputDefinition, map[string]any{"file": "output/result.txt"}); err == nil {
		t.Fatal("symlink output parent was accepted")
	}
}
