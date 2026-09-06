package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/userapproval"
)

type messageCLIFixture struct {
	request MessageCLIRequest
	result  map[string]any
	err     error
}

func (fixture *messageCLIFixture) CallMessage(_ context.Context, request MessageCLIRequest) (map[string]any, error) {
	fixture.request = request
	return fixture.result, fixture.err
}

func TestMessageCLIShapesAndIdempotency(t *testing.T) {
	fixture := &messageCLIFixture{result: map[string]any{"message_id": "om_result"}}
	client, err := NewOfficialMessageClient("cli_fixture", fixture)
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := client.Send(context.Background(), MessageTarget{Type: "open_id", ID: "ou_fixture"}, "text", "private body", "operation-1")
	if err != nil || messageID != "om_result" || fixture.request.Method != "create" || fixture.request.Body["uuid"] != "operation-1" || fixture.request.Params["receive_id_type"] != "open_id" {
		t.Fatalf("send=%s %v %#v", messageID, err, fixture.request)
	}
	_, err = client.Reply(context.Background(), "om_parent", "markdown", "continue", "operation-2")
	if err != nil || fixture.request.Method != "reply" || fixture.request.Body["uuid"] != "operation-2" || fixture.request.Body["msg_type"] != "interactive" {
		t.Fatalf("reply %v %#v", err, fixture.request)
	}
	if err := client.PatchCard(context.Background(), "om_parent", `{"schema":"2.0"}`); err != nil || fixture.request.Method != "patch" {
		t.Fatal(err)
	}
	if err := client.PatchCard(context.Background(), "om_parent", "null"); err == nil {
		t.Fatal("null card accepted")
	}
}

func fakeMessageCLI(t *testing.T, response string, exitCode string) CapabilityExecutor {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "lark-cli")
	script := "#!/bin/sh\ncase \"$*\" in\n *'auth status'*) printf '%s\\n' '{\"appId\":\"cli_fixture\",\"brand\":\"feishu\",\"identities\":{\"user\":{\"available\":false},\"bot\":{\"available\":true}}}'; exit 0 ;;\nesac\nprintf 'executed\\n' >> '" + filepath.Join(root, "calls") + "'\nprintf '%s\\n' \"$@\" > '" + filepath.Join(root, "args") + "'\ncat > '" + filepath.Join(root, "input") + "'\nprintf '%s\\n' '" + response + "'\nexit " + exitCode + "\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return CapabilityExecutor{Binary: binary, Profile: "default", DataRoot: root, WorkingDirectory: root}
}

func TestMessageCLIUsesStdinAndExplicitBot(t *testing.T) {
	runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{"message_id":"om_result"}}`, "0")
	allowMessageSendFixture(t, runner)
	_, err := runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "messages", Method: "create", Params: map[string]string{"receive_id_type": "chat_id"}, Body: map[string]any{"receive_id": "oc_fixture", "msg_type": "text", "content": `{"text":"private-body-fixture"}`}})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(runner.DataRoot, "args"))
	input, _ := os.ReadFile(filepath.Join(runner.DataRoot, "input"))
	if strings.Contains(string(args), "private-body-fixture") || !strings.Contains(string(args), "--as\nbot\n") || !strings.Contains(string(args), "--data\n-\n") || !strings.Contains(string(input), "private-body-fixture") {
		t.Fatalf("unsafe argv or missing body: %q", args)
	}
}

func allowMessageSendFixture(t *testing.T, runner CapabilityExecutor) {
	t.Helper()
	store := NewCapabilityPolicyStore(runner.DataRoot)
	policy, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides["im.sdk.message.send"] = CapabilityAllowed
	if _, err := store.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestMessageCLISendPreservesLegacyConfirmation(t *testing.T) {
	runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{}}`, "0")
	_, err := runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "messages", Method: "create", Params: map[string]string{"receive_id_type": "chat_id"}, Body: map[string]any{"receive_id": "oc_fixture", "msg_type": "text", "content": `{"text":"hello"}`}})
	if err == nil || err.Error() != "user_command_confirmation_required" {
		t.Fatalf("legacy confirmation bypassed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runner.DataRoot, "calls")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unconfirmed send reached process")
	}
}

func TestMessageCLIRejectsUnverifiedSuccessAndConfirmation(t *testing.T) {
	for _, response := range []string{`{"code":0,"data":{}}`, `{"ok":true,"identity":"user","data":{}}`, `{"ok":true,"identity":"bot","dry_run":true,"data":{}}`, `{"ok":false,"data":{}}`, `{"ok":true,"identity":"bot","error":{"message":"private-detail"},"data":{}}`, "not-json"} {
		runner := fakeMessageCLI(t, response, "0")
		result, err := runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "messages", Method: "get", Params: map[string]string{"message_id": "om_fixture"}})
		if err == nil || !CapabilityOutcomeUncertain(err) {
			t.Fatalf("accepted %s", response)
		}
		encoded, _ := json.Marshal(result)
		calls, _ := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
		if strings.Contains(string(encoded), "private-detail") || string(calls) != "executed\n" {
			t.Fatalf("unverified response leaked details or replayed: %s %q", encoded, calls)
		}
	}
	runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{}}`, "10")
	_, err := runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "messages", Method: "patch", Params: map[string]string{"message_id": "om_fixture"}, Body: map[string]any{"content": `{"schema":"2.0"}`}})
	var failure *CLICommandError
	if !errors.As(err, &failure) || !failure.Confirmation {
		t.Fatalf("confirmation bypassed %v", err)
	}
}

func TestMessageCLIFixedCommandsRespectDisabledPolicyAndLease(t *testing.T) {
	for _, test := range []struct {
		request MessageCLIRequest
		ids     []string
	}{
		{MessageCLIRequest{Resource: "messages", Method: "create", Params: map[string]string{"receive_id_type": "chat_id"}, Body: map[string]any{"receive_id": "oc_fixture", "msg_type": "text", "content": `{"text":"hello"}`}}, []string{"im.sdk.message.send", "im.shortcut.messages.send"}},
		{MessageCLIRequest{Resource: "messages", Method: "reply", Params: map[string]string{"message_id": "om_fixture"}, Body: map[string]any{"msg_type": "text", "content": `{"text":"hello"}`}}, []string{"im.message.reply", "im.shortcut.messages.reply"}},
		{MessageCLIRequest{Resource: "messages", Method: "patch", Params: map[string]string{"message_id": "om_fixture"}, Body: map[string]any{"content": `{"schema":"2.0"}`}}, []string{"im.message.edit", "im.messages.patch"}},
		{MessageCLIRequest{Resource: "messages", Method: "get", Params: map[string]string{"message_id": "om_fixture"}}, []string{"im.message.batch-get", "im.shortcut.messages.mget"}},
		{MessageCLIRequest{Resource: "images", Method: "create", Body: map[string]any{"image_type": "message"}}, []string{"im.images.create"}},
		{MessageCLIRequest{Resource: "files", Method: "create", Body: map[string]any{"file_type": "stream"}}, []string{"im.files.create"}},
	} {
		for _, disabled := range append([]string{"", "lease_busy"}, test.ids...) {
			t.Run(test.request.Resource+"."+test.request.Method+"/"+disabled, func(t *testing.T) {
				runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{"message_id":"om_result"}}`, "0")
				allowMessageSendFixture(t, runner)
				request := test.request
				if request.Resource != "messages" {
					request.File = filepath.Join(runner.DataRoot, "media")
					if err := os.WriteFile(request.File, []byte("frozen-media-fixture"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if disabled == "lease_busy" {
					release, err := userapproval.TryExecutionLease(runner.DataRoot)
					if err != nil {
						t.Fatal(err)
					}
					defer release()
				} else if disabled != "" {
					store := NewCapabilityPolicyStore(runner.DataRoot)
					policy, err := store.Load()
					if err != nil {
						t.Fatal(err)
					}
					policy.CapabilityOverrides[disabled] = CapabilityDisabled
					if _, err := store.Save(policy, policy.Revision); err != nil {
						t.Fatal(err)
					}
				}
				result, err := runner.CallMessage(context.Background(), request)
				calls, _ := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
				if disabled == "" {
					if err != nil || result["message_id"] != "om_result" || string(calls) != "executed\n" {
						t.Fatalf("fixed command did not execute once without desktop: %#v %v %q", result, err, calls)
					}
				} else {
					expected := "approval_policy_denied"
					if disabled == "lease_busy" {
						expected = "approval_authorization_busy"
					}
					if err == nil || err.Error() != expected || len(calls) != 0 || CapabilityOutcomeUncertain(err) {
						t.Fatalf("disabled/busy fixed command bypassed gate: %v %q", err, calls)
					}
				}
			})
		}
	}
}

func TestMessageCLIErrorPreservesStatusAndExecutionWithoutReplay(t *testing.T) {
	for _, status := range []string{"429", "503"} {
		runner := fakeMessageCLI(t, `{"ok":false,"error":{"http_status":`+status+`,"message":"private-failure-detail"}}`, "3")
		result, err := runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "messages", Method: "get", Params: map[string]string{"message_id": "om_fixture"}})
		var legacy *CLICommandError
		var execution *CLIExecutionError
		if !errors.As(err, &legacy) || !errors.As(err, &execution) || legacy.HTTPStatus < 400 || legacy.ExitCode != 3 || !execution.Started || !CapabilityOutcomeUncertain(err) || result["structuredCLI"] == nil {
			t.Fatalf("execution/status lost: %#v %v", result, err)
		}
		encoded, _ := json.Marshal(result)
		calls, _ := os.ReadFile(filepath.Join(runner.DataRoot, "calls"))
		if strings.Contains(string(encoded), "private-failure-detail") || string(calls) != "executed\n" {
			t.Fatalf("detail leaked or message replayed: %s %q", encoded, calls)
		}
		client, _ := NewOfficialMessageClient("cli_fixture", runner)
		verifyErr := client.VerifyBotMessage(context.Background(), MessageTarget{Type: "chat_id", ID: "oc_fixture"}, "om_fixture")
		if verifyErr == nil || !strings.Contains(verifyErr.Error(), "restoration_service_temporarily_unavailable") {
			t.Fatalf("restoration status classification lost: %v", verifyErr)
		}
	}
}

func TestMessageCLIUsesLeaseUntilProcessCompletes(t *testing.T) {
	runner := fakeMessageCLI(t, `{"ok":true,"identity":"bot","data":{}}`, "0")
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	resume := filepath.Join(runner.DataRoot, "resume")
	defer os.WriteFile(resume, nil, 0600)
	body := "while [ ! -e '" + resume + "' ]; do sleep 0.01; done; cat >"
	if err := os.WriteFile(runner.Binary, []byte(strings.Replace(string(script), "cat >", body, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runner.CallMessage(ctx, MessageCLIRequest{Resource: "messages", Method: "get", Params: map[string]string{"message_id": "om_fixture"}})
		done <- err
	}()
	for {
		if _, err := os.Stat(filepath.Join(runner.DataRoot, "calls")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("message exited before fixture barrier: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := runner.RunAuthJSON(ctx, []string{"auth", "logout", "--json"}, nil, time.Second); err == nil || err.Error() != "approval_authorization_busy" {
		t.Fatalf("message execution did not hold lease: %v", err)
	}
	if err := os.WriteFile(resume, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatalf("message leaked lease: %v", err)
	}
	release()
}

func TestMessageCLIDirectRunnerOnlyAllowsFixedEventLifecycle(t *testing.T) {
	for _, args := range [][]string{
		{"im", "messages", "create", "--as", "bot", "--json"},
		{"api", "POST", "/open-apis/im/v1/messages"},
		{"auth", "logout"},
		{"event", "consume", "im.message.receive_v1"},
		{"event", "status", "--current", "--json"},
		{"event", "stop", "--json", "--all"},
		{"event", "stop", "--json", "--as", "bot"},
	} {
		runner := fakeMessageCLI(t, `{"ok":true}`, "0")
		_, err := runner.runCLIJSON(context.Background(), args, nil, runner.DataRoot, time.Second)
		if err == nil || err.Error() != "unsupported_internal_cli_lifecycle_command" {
			t.Fatalf("direct runner accepted non-lifecycle invocation: %v %v", args, err)
		}
		if _, err := os.Stat(filepath.Join(runner.DataRoot, "calls")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rejected direct invocation started process")
		}
	}
	for _, args := range [][]string{{"event", "status", "--current", "--json", "--fail-on-orphan"}, {"event", "stop", "--json"}} {
		runner := fakeMessageCLI(t, `{"ok":true}`, "0")
		if _, err := runner.runCLIJSON(context.Background(), args, []byte("unexpected"), runner.DataRoot, time.Second); err == nil {
			t.Fatal("lifecycle invocation accepted arbitrary stdin")
		}
		if _, err := os.Stat(filepath.Join(runner.DataRoot, "calls")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid lifecycle input started process")
		}
		if _, err := runner.runCLIJSON(context.Background(), args, nil, runner.DataRoot, time.Second); err != nil {
			t.Fatalf("fixed lifecycle rejected: %v %v", args, err)
		}
	}
}

func TestCLIEventNormalizationPreservesBindingsAndDropsToken(t *testing.T) {
	var received map[string]any
	inbound, err := NewOfficialInbound(CapabilityExecutor{Binary: "fixture"}, nil, func(_ context.Context, _ string, data []byte) error { return json.Unmarshal(data, &received) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"type":"im.message.receive_v1","event_id":"evt_1","message_id":"om_1","sender_id":"ou_1","chat_id":"oc_1","chat_type":"p2p","message_type":"text","content":"hello","reply_to":"om_parent"}`)
	if err := inbound.HandleCLIEvent(context.Background(), "im.message.receive_v1", payload); err != nil {
		t.Fatal(err)
	}
	message, err := normalizeInboundMessage(received)
	if err != nil || message.Text != "hello" || message.ParentID != "om_parent" {
		t.Fatalf("message=%#v err=%v", message, err)
	}
	payload = []byte(`{"type":"card.action.trigger","event_id":"evt_2","message_id":"om_card","operator_id":"ou_1","chat_id":"oc_1","token":"private-token-fixture","action_tag":"button","action_value":"{\"requestId\":7,\"operation\":\"input\"}","form_value":"{\"answer\":\"A\"}"}`)
	if err := inbound.HandleCLIEvent(context.Background(), "card.action.trigger", payload); err != nil {
		t.Fatal(err)
	}
	card, err := normalizeInboundCard(received)
	if err != nil || card.Value["requestId"] != float64(7) || card.FormValue["answer"] != "A" || card.Token != "" {
		t.Fatalf("card=%#v err=%v", card, err)
	}
	data, _ := json.Marshal(received)
	if strings.Contains(string(data), "private-token-fixture") {
		t.Fatal("callback token persisted")
	}
}

func TestCLIEventRejectsMissingFieldsWithoutGuessing(t *testing.T) {
	delivered := false
	inbound, _ := NewOfficialInbound(CapabilityExecutor{Binary: "fixture"}, nil, func(context.Context, string, []byte) error { delivered = true; return nil }, nil)
	for _, payload := range []string{
		`{"type":"im.message.receive_v1","message_id":"om_1","chat_id":"oc_1","sender_id":"ou_1"}`,
		`{"type":"im.message.receive_v1","event_id":"evt_1","message_id":"om_1","chat_id":"oc_1","sender_id":"ou_1","message_type":"image","content":"[image]"}`,
	} {
		if err := inbound.HandleCLIEvent(context.Background(), "im.message.receive_v1", []byte(payload)); err == nil {
			t.Fatal("unrecoverable event accepted")
		}
	}
	if delivered {
		t.Fatal("invalid event delivered")
	}
}
