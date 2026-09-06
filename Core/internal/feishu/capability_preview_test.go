package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/usercommand"
)

func TestCapabilityInvocationDoesNotSelfApprove(t *testing.T) {
	definition, _ := CapabilityByID("approval.tasks.approve")
	args, _, _, err := capabilityInvocation(definition, map[string]any{"data": map[string]any{"instance_code": "instance_1", "task_id": "task_1"}})
	if err != nil || contains(args, "--yes") {
		t.Fatalf("approval was inserted before the gate: %v %v", args, err)
	}
}

func TestCapabilityRunPreservesBusinessJSON(t *testing.T) {
	runner, _, _ := fakeApprovedBusinessRunner(t, "approved")
	definition, _ := CapabilityByID("base.shortcut.record.batch.create")
	runner.UserApproval.testExecute = func(_ context.Context, _ CapabilityDefinition, args []string, _ []byte) error {
		if strings.Count(strings.Join(args, " "), "--json") != 1 {
			t.Fatalf("business JSON overwritten by output switch: %v", args)
		}
		return nil
	}
	_, err := runner.run(context.Background(), definition, []string{"base", "+record-batch-create", "--json", "{\"fields\":{\"role\":\"reader\"}}"}, nil, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStructuredCLIErrorIsRedactedAndNeverReplayed(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "approved")
	script := "#!/bin/sh\nprintf 'executed\\n' >> '" + logPath + "'\nprintf '%s' '{\"ok\":false,\"error\":{\"type\":\"network\",\"subtype\":\"timeout\",\"retryable\":true,\"code\":123,\"message\":\"secret user content\",\"hint\":\"Bearer private-secret\",\"access_token\":\"private-secret\"}}' >&2\nexit 4\n"
	if err := os.WriteFile(runner.Binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := runner.runBusinessProcess(context.Background(), []string{"fixture"}, nil, runner.DataRoot, time.Second)
	var failure *CLIExecutionError
	if !errors.As(err, &failure) || !failure.Started || failure.Outcome != "unknown" || failure.Structured["type"] != "network" || failure.Structured["retryable"] != true {
		t.Fatalf("structured failure lost: %v %#v", err, result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "content") || !strings.Contains(string(encoded), "structuredCLI") {
		t.Fatalf("unsafe or missing error: %s", encoded)
	}
	data, _ := os.ReadFile(logPath)
	if string(data) != "executed\n" || !isUncertainExecutionError(err) {
		t.Fatalf("execution was replayed or marked retryable: %q %v", data, err)
	}
}

func TestUserCommandSafeErrorsKeepTheirServiceCodes(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "usercommand", "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("usercommand source unavailable: %v", err)
	}
	pattern := regexp.MustCompile(`"(user_command_[a-z0-9_]+)`)
	codes := map[string]bool{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			codes[match[1]] = true
		}
	}
	if len(codes) == 0 {
		t.Fatal("no canonical error codes checked")
	}
	for code := range codes {
		t.Run(code, func(t *testing.T) {
			cause := errors.New(code + ": private detail")
			wrapped := fmt.Errorf("preparation: %w", cause)
			approval := approvalCommandError(wrapped)
			if approval.Error() != code || CapabilityOperationErrorCode(wrapped) != code || CapabilityOutcomeUncertain(approval) {
				t.Fatalf("pre-execution code was collapsed: %v", approval)
			}
			for _, started := range []bool{false, true} {
				failure := commandExecutionError("lark_cli_artifact_delivery_failed", 0, started, wrapped)
				result := cliFailureResult(nil, failure)
				structured, _ := result["structuredCLI"].(map[string]any)
				if failure.Code != code || CapabilityOperationErrorCode(failure) != code || structured["code"] != code || structured["started"] != started || CapabilityOutcomeUncertain(failure) != started {
					t.Fatalf("canonical failure projection lost: %#v %v", result, failure)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "private") {
					t.Fatalf("error details leaked: %s", encoded)
				}
			}
		})
	}
	for _, unsafe := range []string{"user_command_private_secret", "user_command_file_unsafe/private-secret", "user_command_file_unsafe\nprivate-secret"} {
		if safeUserCommandErrorCode(errors.New(unsafe)) != "" || approvalCommandError(errors.New(unsafe)).Error() != "approval_unavailable" {
			t.Fatalf("unregistered or malformed error code accepted: %q", unsafe)
		}
	}
}

func TestCLIStartFailureIsNotAnUnknownWrite(t *testing.T) {
	runner := CapabilityExecutor{Binary: "/nonexistent/ksfas-fixture", DataRoot: t.TempDir()}
	result, err := runner.runBusinessProcess(context.Background(), nil, nil, runner.DataRoot, time.Second)
	var failure *CLIExecutionError
	if !errors.As(err, &failure) || failure.Started || failure.Outcome != "not_started" || result["structuredCLI"] == nil {
		t.Fatalf("start failure lost: %#v %v", result, err)
	}
	if isUncertainExecutionError(&CapabilityExecutionError{Phase: "write", Err: err}) {
		t.Fatal("unstarted process recorded as unknown write")
	}
}

func TestCanonicalMessageInputDoesNotRequireEveryAlternative(t *testing.T) {
	if err := ValidateCapabilityInput("im.shortcut.messages.send", map[string]any{"chat-id": "oc_fixture", "text": "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCapabilityInput("im.shortcut.messages.send", map[string]any{"user-id": "ou_fixture", "text": "hello"}); err != nil {
		t.Fatal(err)
	}
}

func TestReviewedBusinessJSONKeysAreNotOperations(t *testing.T) {
	input := map[string]any{"base-token": "bas_fixture", "table-id": "tbl_fixture", "json": map[string]any{"create_records": []any{map[string]any{"role": "reader", "member": "alice", "permission": "read", "delete": false}}}}
	if err := ValidateCapabilityInput("base.shortcut.record.batch.create", input); err != nil {
		t.Fatal(err)
	}
	input["json"] = 12
	if err := ValidateCapabilityInput("base.shortcut.record.batch.create", input); err == nil {
		t.Fatal("scalar accepted for object body")
	}
}

func TestBusinessFormatIsNotAnOutputSwitch(t *testing.T) {
	if !capabilityBusinessFlag(usercommand.FlagDescriptor{Name: "format", Type: "string"}) {
		t.Fatal("business format was omitted")
	}
	if capabilityBusinessFlag(usercommand.FlagDescriptor{Name: "format", Role: "output-format", Type: "string"}) {
		t.Fatal("output format was exposed as business input")
	}
}

func TestInlineJSONActuallyReachesProcessWithoutFileCarrier(t *testing.T) {
	runner, _, requests := fakeApprovedBusinessRunner(t, "approved")
	argumentPath := filepath.Join(runner.DataRoot, "arguments")
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "cat >/dev/null", "printf '%s\\n' \"$@\" > '"+argumentPath+"'; cat >/dev/null", 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = runner.Execute(context.Background(), "im.shortcut.messages.send", map[string]any{"chat-id": "oc_fixture", "content": map[string]any{"text": "literal content"}})
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argumentPath)
	if err != nil || !strings.Contains(string(args), "--content\n{\"text\":\"literal content\"}\n") || *requests != 1 {
		t.Fatalf("actual argv did not preserve inline JSON: %q, approvals=%d, err=%v", args, *requests, err)
	}
}

func TestTranscriptConsumesMaterializedArtifactAndCleansStaging(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "denied")
	runner.UserApproval = nil
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	body := "pwd > '" + logPath + ".cwd'; while [ $# -gt 0 ]; do if [ \"$1\" = '--output' ]; then shift; mkdir -p \"$(dirname \"$1\")\"; printf 'materialized transcript' > \"$1\"; fi; shift; done; cat >/dev/null"
	script = []byte(strings.Replace(string(script), "cat >/dev/null", body, 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := runner.Execute(context.Background(), "note.shortcut.transcript", map[string]any{"note-id": "note_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	response, _ := result["response"].(map[string]any)
	if response["transcript"] != "materialized transcript" {
		t.Fatalf("artifact did not reach result: %#v", result)
	}
	directory, err := os.ReadFile(logPath + ".cwd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(directory))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("internal artifact staging retained: %s %v", directory, err)
	}
}

func TestBusinessArtifactsPublishWithoutOverwritingExistingFiles(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "conflict"}[conflict], func(t *testing.T) {
			runner, logPath, _ := fakeApprovedBusinessRunner(t, "denied")
			runner.UserApproval = nil
			script, err := os.ReadFile(runner.Binary)
			if err != nil {
				t.Fatal(err)
			}
			body := "pwd > '" + logPath + ".cwd'; while [ $# -gt 0 ]; do if [ \"$1\" = '--output' ]; then shift; mkdir -p \"$(dirname \"$1\")\"; printf 'downloaded' > \"$1\"; fi; shift; done; cat >/dev/null"
			script = []byte(strings.Replace(string(script), "cat >/dev/null", body, 1))
			if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(runner.DataRoot, "resource")
			if conflict {
				if err := os.WriteFile(destination, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := runner.Execute(context.Background(), "im.shortcut.messages.resources.download", map[string]any{"message-id": "om_fixture", "file-key": "file_fixture", "type": "file", "output": "resource"})
			if conflict {
				if err == nil || !CapabilityOutcomeUncertain(err) || result["structuredCLI"] == nil || result["ok"] != false {
					t.Fatalf("publish conflict was not retained: %#v %v", result, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				response, _ := result["response"].(map[string]any)
				artifacts, _ := response["artifacts"].([]usercommand.Artifact)
				if len(artifacts) != 1 || artifacts[0].Bytes != 10 || len(artifacts[0].SHA256) != 64 {
					t.Fatalf("published manifest missing: %#v", response)
				}
				delivery, _ := response["artifactDelivery"].(map[string]any)
				if delivery["status"] != "published" || delivery["upstreamPaths"] != "staging_removed" || delivery["canonicalPaths"] != "artifacts" {
					t.Fatalf("upstream artifact paths were not marked as staging: %#v", response)
				}
			}
			data, readErr := os.ReadFile(destination)
			if readErr != nil || string(data) != map[bool]string{false: "downloaded", true: "original"}[conflict] {
				t.Fatalf("destination not preserved: %q %v", data, readErr)
			}
			calls, _ := os.ReadFile(logPath)
			if strings.Count(string(calls), "write") != 1 {
				t.Fatalf("CLI was replayed: %q", calls)
			}
			staging, _ := os.ReadFile(logPath + ".cwd")
			if _, err := os.Stat(strings.TrimSpace(string(staging))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staging was not cleaned: %s %v", staging, err)
			}
		})
	}
}

func TestBusinessMissingArtifactIsNotSuccessfulOrReplayed(t *testing.T) {
	runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
	runner.UserApproval = nil
	result, err := runner.Execute(context.Background(), "im.shortcut.messages.resources.download", map[string]any{"message-id": "om_fixture", "file-key": "file_fixture", "type": "file", "output": "missing"})
	var failure *CLIExecutionError
	if !errors.As(err, &failure) || !failure.Started || failure.Outcome != "unknown" || result["ok"] != false || result["structuredCLI"] == nil || result["artifacts"] != nil {
		t.Fatalf("missing artifact reported as successful: %#v %v", result, err)
	}
	if !errors.Is(failure.Cause, os.ErrNotExist) && (failure.Cause == nil || failure.Cause.Error() != "user_command_artifact_missing") {
		t.Fatalf("unexpected publication failure: %v", failure.Cause)
	}
	calls, _ := os.ReadFile(logPath)
	if strings.Count(string(calls), "write") != 1 || *requests != 0 {
		t.Fatalf("download replayed or opened desktop: %q %d", calls, *requests)
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing artifact was published: %v", err)
	}
}

func TestPartialArtifactPublicationRetainsExactDeliveryEvidence(t *testing.T) {
	artifact := usercommand.Artifact{Path: filepath.Join(t.TempDir(), "delivered"), Bytes: 4, SHA256: strings.Repeat("a", 64)}
	publicationErr := errors.New("user_command_artifact_publication_incomplete")
	result, err := businessArtifactPublicationResult(map[string]any{"ok": true}, []usercommand.Artifact{artifact}, publicationErr)
	var failure *CLIExecutionError
	if !errors.As(err, &failure) || !failure.Started || failure.Outcome != "unknown" || !errors.Is(err, publicationErr) || result["ok"] != false {
		t.Fatalf("partial publication reported as success or unstarted: %#v %v", result, err)
	}
	delivery, _ := result["artifactDelivery"].(map[string]any)
	if delivery["status"] != "partial" || delivery["upstreamPaths"] != "staging_removed" || delivery["canonicalPaths"] != "artifacts" {
		t.Fatalf("partial delivery metadata missing: %#v", result)
	}
	encoded, marshalErr := json.Marshal(result["artifacts"])
	var delivered []usercommand.Artifact
	if marshalErr != nil || json.Unmarshal(encoded, &delivered) != nil || len(delivered) != 1 || delivered[0] != artifact {
		t.Fatalf("exact published artifacts lost: %s %v", encoded, marshalErr)
	}
	if result["structuredCLI"] == nil || !CapabilityOutcomeUncertain(err) || !isUncertainExecutionError(err) {
		t.Fatalf("partial publication lost no-replay outcome: %#v %v", result, err)
	}
}

func TestBotCLIConfirmationFollowsExistingBoundaryAndPolicy(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "disabled"}[disabled], func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			runner.UserApproval = nil
			definition, _ := CapabilityByID("im.messages.delete")
			definition.Identity = "bot"
			input := map[string]any{"message-id": "om_fixture"}
			store := NewCapabilityPolicyStore(runner.DataRoot)
			policy, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			policy.CapabilityOverrides[definition.ID] = CapabilityConfirmEach
			if _, err := store.Save(policy, policy.Revision); err != nil {
				t.Fatal(err)
			}
			ctx, _ := reviewRunningBoundary(t, runner.DataRoot, definition.ID, input)
			if disabled {
				store := NewCapabilityPolicyStore(runner.DataRoot)
				policy, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				policy.CapabilityOverrides[definition.ID] = CapabilityDisabled
				if _, err := store.Save(policy, policy.Revision); err != nil {
					t.Fatal(err)
				}
			}
			script, err := os.ReadFile(runner.Binary)
			if err != nil {
				t.Fatal(err)
			}
			script = []byte(strings.Replace(string(script), "cat >/dev/null", "printf '%s\\n' \"$@\" > '"+logPath+".args'; cat >/dev/null", 1))
			if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
				t.Fatal(err)
			}
			_, err = runner.run(ctx, definition, []string{"im", "messages", "delete", "--message-id", "om_fixture"}, nil, nil, time.Second)
			if disabled {
				if err == nil || err.Error() != "approval_policy_denied" {
					t.Fatalf("disabled bot command: %v", err)
				}
				if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("disabled bot command executed")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				args, _ := os.ReadFile(logPath + ".args")
				if strings.Count(string(args), "--yes\n") != 1 || !strings.Contains(string(args), "--as\nbot\n") {
					t.Fatalf("confirmed bot argv: %q", args)
				}
			}
			if *requests != 0 {
				t.Fatal("bot command requested desktop approval")
			}
		})
	}
}
