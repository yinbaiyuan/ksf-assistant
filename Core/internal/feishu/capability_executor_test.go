package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCapabilityResultIsClippedBeforePersistence(t *testing.T) {
	input := map[string]any{"data": strings.Repeat("x", maximumCapabilityResultBytes*2)}
	result := boundCapabilityResult(input)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maximumCapabilityResultBytes+4096 || result["_truncated"] != true {
		t.Fatalf("bounded result bytes=%d result=%#v", len(encoded), result)
	}
}

func TestCapabilityInvocationUsesOnlyDeclaredInputCarriers(t *testing.T) {
	definition, ok := CapabilityByID("im.message.reply")
	if !ok {
		t.Fatal("missing capability")
	}
	args, stdin, files, err := capabilityInvocation(definition, map[string]any{"message-id": "om_test", "text": "private body"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stdin) != 0 || !strings.Contains(strings.Join(args, " "), "--text private body") || len(files) != 0 {
		t.Fatalf("plain text used an undeclared input carrier: %#v", args)
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

func TestApprovalDecisionInvocationUsesPrivateDataAndCLIGate(t *testing.T) {
	definition, ok := CapabilityByID("approval.tasks.approve")
	if !ok {
		t.Fatal("missing approval decision capability")
	}
	args, stdin, files, err := capabilityInvocation(definition, map[string]any{"data": map[string]any{"instance_code": "instance_1", "task_id": "task_1", "comment": "private approval comment"}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--yes") || strings.Contains(joined, "private approval comment") || len(files) != 0 || !strings.Contains(string(stdin), "private approval comment") {
		t.Fatalf("unsafe approval invocation: args=%#v files=%d", args, len(files))
	}
}

func TestEnabledApprovalCancellationStillUsesTheCLIGate(t *testing.T) {
	definition, ok := CapabilityByID("approval.instances.cancel")
	if !ok || definition.Risk != "destructive" {
		t.Fatal("missing destructive approval cancellation capability")
	}
	args, _, _, err := capabilityInvocation(definition, map[string]any{"data": map[string]any{"instance_code": "instance_1"}})
	if err != nil {
		t.Fatal(err)
	}
	if contains(args, "--yes") {
		t.Fatalf("destructive approval invocation confirmed before the gate: %#v", args)
	}
}

func TestApprovalEventSubscriptionUsesFixedRawEndpoint(t *testing.T) {
	definition, ok := CapabilityByID("approval.events.instance.subscribe")
	if !ok {
		t.Fatal("missing approval event subscription capability")
	}
	args, _, files, err := capabilityInvocation(definition, map[string]any{"subscription-type": "INVOLVED_APPROVAL"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "api POST /open-apis/approval/v4/instances/subscription") || strings.Contains(joined, "INVOLVED_APPROVAL") || len(files) != 1 {
		t.Fatalf("unexpected approval subscription invocation: args=%#v files=%d", args, len(files))
	}
}

func TestApprovalWritePayloadUsesFixedNestedSchema(t *testing.T) {
	definition, _ := CapabilityByID("approval.tasks.approve")
	valid := map[string]any{"data": map[string]any{"instance_code": "instance_1", "task_id": "task_1", "comment": "同意"}}
	if err := validateCapabilityInput(definition, valid); err != nil {
		t.Fatalf("valid approval input rejected: %v", err)
	}
	for _, input := range []map[string]any{
		{"data": map[string]any{"instance_code": "instance_1"}},
		{"data": map[string]any{"instance_code": "instance_1", "task_id": "task_1", "unexpected": true}},
		{"data": map[string]any{"instance_code": "instance_1", "task_id": "task_1", "form": "not-json"}},
		{"data": map[string]any{"instance_code": "instance_1", "task_id": "task_1", "form": `{}`}},
	} {
		if err := validateCapabilityInput(definition, input); err == nil {
			t.Fatalf("invalid approval input accepted: %#v", input)
		}
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
	runner := CapabilityExecutor{Binary: bin, Profile: "test", DataRoot: root, WorkingDirectory: root, UserApproval: allowFixtureBusinessCommands()}
	result, err := runner.Execute(context.Background(), "markdown.overwrite", map[string]any{
		"file-token": "fm_test", "content": "replacement",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["verified"] != false || result["verificationState"] != string(VerificationInconclusive) || result["preflight"] == nil || result["verification"] == nil {
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

func TestRawCapabilityExecutorRejectsSDKAndServiceRoutes(t *testing.T) {
	runner := CapabilityExecutor{}
	tests := []struct {
		id    string
		input map[string]any
	}{
		{
			id: "im.sdk.message.send",
			input: map[string]any{
				"request-id": "OUT-raw-route", "target-type": "open_id", "target-id": "ou_private",
				"format": "text", "text": "private", "source": "test",
			},
		},
		{
			id: "docs.service.document.create",
			input: map[string]any{
				"content": "private", "format": "markdown", "source": "test",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			if _, err := runner.ExecuteWithOptions(context.Background(), test.id, test.input, CapabilityExecutionOptions{}); err == nil || err.Error() != "capability_requires_unified_executor" {
				t.Fatalf("raw executor route error = %v", err)
			}
		})
	}
}

func TestRereadFailureReturnsPartialWriteEvidenceForReconciliation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	fetchCount := filepath.Join(root, "fetch-count")
	bin := filepath.Join(root, "fake-lark-cli")
	script := `#!/bin/sh
case "$*" in
  *markdown\ +fetch*)
    count=0
    if [ -f "` + fetchCount + `" ]; then count=$(cat "` + fetchCount + `"); fi
    count=$((count + 1))
    printf '%s' "$count" > "` + fetchCount + `"
    if [ "$count" -gt 1 ]; then printf 'reread unavailable\n' >&2; exit 1; fi
    printf '{"data":{"content":"before"}}\n'
    ;;
  *markdown\ +overwrite*) printf '{"data":{"file_token":"fm_test"}}\n' ;;
  *) printf '{}\n' ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root, UserApproval: allowFixtureBusinessCommands()}
	result, err := runner.Execute(context.Background(), "markdown.overwrite", map[string]any{"file-token": "fm_test", "content": "replacement"})
	if err == nil || !CapabilityOutcomeUncertain(err) {
		t.Fatalf("reread error = %v", err)
	}
	if result == nil || result["response"] == nil || result["verified"] != false || result["verificationState"] != string(VerificationInconclusive) {
		t.Fatalf("partial result = %#v", result)
	}
}

func TestMappedCapabilityInputReadsNestedAndRecursiveResult(t *testing.T) {
	step := &CapabilityStep{Map: map[string]string{"release-id": "$result.release_id"}}
	value := mappedCapabilityInput(step, nil, map[string]any{"data": map[string]any{"release_id": "rel_1"}})
	if value["release-id"] != "rel_1" {
		t.Fatalf("result mapping failed: %#v", value)
	}
}

func TestMappedCapabilityInputReadsNestedRequestValues(t *testing.T) {
	step := &CapabilityStep{Map: map[string]string{"instance-code": "$input.data.instance_code"}}
	got := mappedCapabilityInput(step, map[string]any{"data": map[string]any{"instance_code": "instance_1"}}, nil)
	if got["instance-code"] != "instance_1" {
		t.Fatalf("nested input mapping = %#v", got)
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

func TestNoteTranscriptUsesPrivateTemporaryArtifactAndCleansIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "fake-lark-cli")
	script := `#!/bin/sh
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then shift; output="$1"; fi
  shift
done
mkdir -p "$(dirname "$output")"
printf 'private transcript' > "$output"
printf '{"data":{"ok":true}}\n'
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root, UserApproval: allowFixtureBusinessCommands()}
	result, err := runner.Execute(context.Background(), "note.shortcut.transcript", map[string]any{"note-id": "note_test"})
	if err != nil {
		t.Fatal(err)
	}
	response, _ := result["response"].(map[string]any)
	if response["transcript"] != "private transcript" {
		t.Fatalf("transcript artifact was not returned: %#v", result)
	}
	entries, err := os.ReadDir(filepath.Join(root, "private-cache", "transcripts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("transcript artifact was not cleaned: %#v", entries)
	}
}

func TestCapabilityExecutorRejectsOversizedCLIOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "fake-lark-cli")
	script := "#!/bin/sh\ndd if=/dev/zero bs=1048576 count=5 2>/dev/null | tr '\\000' x\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := CapabilityExecutor{Binary: bin, DataRoot: root, WorkingDirectory: root, UserApproval: allowFixtureBusinessCommands()}
	_, err := runner.run(context.Background(), CapabilityDefinition{Command: []string{"fake"}}, []string{"fake"}, nil, nil, time.Minute)
	if err == nil || err.Error() != "lark_cli_output_limit" {
		t.Fatalf("oversized output result = %v", err)
	}
}

func TestBoundedDestructivePreflightUsesOnlyLocalRedactedEvidence(t *testing.T) {
	const target = "wb_private_target"
	runner := CapabilityExecutor{}
	evidence, err := runner.ReadPreflight(context.Background(), "events.watch.whiteboard.remove", map[string]any{"target": target})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if evidence["kind"] != "input_summary" || strings.Contains(string(encoded), target) || !strings.Contains(string(encoded), "sha256:") {
		t.Fatalf("bounded preflight exposed raw input or lost its fingerprint: %s", encoded)
	}
}
