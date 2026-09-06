package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFixedBotRejectsIdentityOverridesAndOtherCommands(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation fixedBotOperation
		args      []string
	}{
		{"user_override", fixedBotDocumentCreate, []string{"docs", "+create", "--doc-format", "text", "--content", "-", "--as", "user"}},
		{"bot_override", fixedBotDocumentCreate, []string{"docs", "+create", "--doc-format", "text", "--content", "-", "--as", "bot"}},
		{"duplicate", fixedBotDocumentCreate, []string{"docs", "+create", "--doc-format", "text", "--content", "-", "--content", "other"}},
		{"other_command", fixedBotDocumentCreate, []string{"mail", "+message-modify"}},
		{"other_operation", fixedBotOperation(99), []string{"docs", "+create"}},
		{"other_api", fixedBotDocumentVersion, []string{"api", "POST", "/open-apis/im/v1/messages", "--data", "-"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			if _, err := runner.runFixedBotBusiness(context.Background(), test.operation, test.args, []byte("body"), time.Second); err == nil {
				t.Fatal("invalid fixed invocation executed")
			}
			if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) || *requests != 0 {
				t.Fatal("invalid invocation reached business process or approval")
			}
		})
	}
}

func TestFixedBotEntrypointsKeepBotIdentityAndExistingCreateRestriction(t *testing.T) {
	runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "cat >/dev/null", "printf '%s\\n' \"$*\" >> '"+logPath+".args'; while [ $# -gt 0 ]; do if [ \"$1\" = '--output' ]; then shift; mkdir -p \"$(dirname \"$1\")\"; printf 'resource' > \"$1\"; fi; shift; done; cat >/dev/null", 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	if err := runner.DownloadMessageResource(context.Background(), "om_fixture", "file_fixture", "file", filepath.Join(runner.DataRoot, "resource"), time.Second); err != nil {
		t.Fatal(err)
	}
	resource, err := os.ReadFile(filepath.Join(runner.DataRoot, "resource"))
	if err != nil || string(resource) != "resource" {
		t.Fatalf("materialized resource did not return to private destination: %q %v", resource, err)
	}
	request := DocumentRequest{Action: "create_document", Identity: "bot", Target: &DocumentTarget{Kind: "wiki_token", Value: "wiki_fixture"}, Content: DocumentContent{Format: "markdown", Text: "resource-free document"}, Source: "test"}
	id, input := documentCapabilityInput(request)
	if err := ValidateCapabilityInput(id, input); err == nil || err.Error() != "document_create_target_invalid" {
		t.Fatalf("existing bot-create restriction changed: %v", err)
	}
	request.Action, request.UpdateMode = "update_document", "append"
	id, input = documentCapabilityInput(request)
	ctx, _ := reviewRunningBoundary(t, runner.DataRoot, id, input)
	if _, err := runner.documentUpdate(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.documentVersion(ctx, *request.Target, map[string]any{"obj_token": "doc_fixture"}, "version \"fixture\""); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(logPath + ".args")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 3 || *requests != 0 {
		t.Fatalf("calls=%q approvals=%d", args, *requests)
	}
	for _, line := range lines {
		if strings.Count(line, "--as bot") != 1 || strings.Contains(line, "--as user") {
			t.Fatalf("identity not fixed: %s", line)
		}
	}
}

func TestFixedBotCannotSpoofUserRequestOrReuseOtherBoundary(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "denied")
	request := DocumentRequest{Identity: "bot", Target: &DocumentTarget{Kind: "docx_token", Value: "doc_fixture"}, Content: DocumentContent{Format: "text", Text: "body"}, Source: "test"}
	if _, err := runner.documentCreate(context.Background(), request); err == nil || err.Error() != "unsupported_identity" {
		t.Fatalf("spoof: %v", err)
	}
	request.Target.Kind = "wiki_token"
	if _, err := runner.documentCreate(context.Background(), request); err == nil || err.Error() != "operation_execution_boundary_required" {
		t.Fatalf("unbound: %v", err)
	}
	ctx := context.WithValue(context.Background(), executionBoundaryKey{}, executionBoundary{capabilityID: "im.sdk.message.send"})
	if _, err := runner.runFixedBotBusiness(ctx, fixedBotDocumentCreate, []string{"docs", "+create", "--doc-format", "text", "--content", "-"}, []byte("body"), time.Second); err == nil || err.Error() != "fixed_bot_operation_boundary_mismatch" {
		t.Fatalf("other boundary: %v", err)
	}
	definition := CapabilityDefinition{ID: "docbox.create", Identity: "bot", Risk: "write", Command: []string{"docs", "+create"}}
	if _, err := runner.run(context.Background(), definition, []string{"docs", "+create", "--doc-format", "text", "--content", "-"}, []byte("<whiteboard>XML</whiteboard>"), nil, time.Second); err == nil {
		t.Fatal("ad-hoc inherited fixed bot bypass")
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("spoof reached business execution")
	}
}
