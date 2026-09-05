package feishucommands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
)

type recordingExecutor struct {
	calls   int
	context context.Context
}

func (executor *recordingExecutor) ExecuteWithOptions(ctx context.Context, capability string, input map[string]any, options feishu.CapabilityExecutionOptions) (map[string]any, error) {
	executor.calls++
	executor.context = ctx
	return map[string]any{"status": "ok", "response": map[string]any{"items": []any{}}}, nil
}
func (executor *recordingExecutor) ReadPreflight(ctx context.Context, capability string, input map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}
func (executor *recordingExecutor) ReadVerification(ctx context.Context, capability string, input, result map[string]any) (feishu.VerificationAssessment, error) {
	return feishu.VerificationAssessment{State: feishu.VerificationConfirmed}, nil
}

func TestExecuteUsesInjectedCapabilityServiceAndContext(t *testing.T) {
	root := t.TempDir()
	runner := &recordingExecutor{}
	service := feishu.NewCapabilityService(root, runner, nil)
	request, err := feishucli.Parse([]string{"capability", "read", "task.shortcut.get.my.tasks", "--payload-file", "-"}, strings.NewReader(`{"page-limit":1,"complete":false}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := Execute(ctx, root, service, request)
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 || runner.context != ctx {
		t.Fatalf("injected executor not used: calls=%d result=%#v", runner.calls, result)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), `"status":"ok"`) || !strings.Contains(string(encoded), `"operation"`) {
		t.Fatalf("envelope=%s err=%v", encoded, err)
	}
}

func TestTaskLinkAndInvalidWireCannotTouchDaemonState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	requests := []feishucli.Request{
		{Command: "task-link", Action: "list"},
		{Command: "policy", Action: "update", Options: map[string]string{"payload-file": "/secret", "expected-revision": "1"}},
		{Command: "arbitrary-api"},
	}
	for _, request := range requests {
		if _, err := Execute(context.Background(), root, nil, request); err == nil {
			t.Fatalf("accepted request=%#v", request)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("rejected request touched daemon root: %v", err)
	}
}

func TestInvocationPayloadsAreIsolatedAndNeverReadPaths(t *testing.T) {
	var group sync.WaitGroup
	for _, value := range []string{"first", "second"} {
		group.Add(1)
		go func(value string) {
			defer group.Done()
			call := &invocation{ctx: context.Background(), request: feishucli.Request{Payloads: map[string][]byte{"content-file": []byte(value)}}}
			payload, err := call.clientPrivateValue([]string{"--content-file", "/never/read/this"}, "--content-file")
			if err != nil || string(payload) != value {
				t.Errorf("payload=%s err=%v", payload, err)
			}
			payload[0] = 'X'
			if string(call.request.Payloads["content-file"]) != value {
				t.Error("private payload was aliased")
			}
		}(value)
	}
	group.Wait()
}

func TestCancellationDoesNotLeaveCommandPolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	call := &invocation{ctx: ctx}
	if err := call.wait(time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait=%v", err)
	}
	root := filepath.Join(t.TempDir(), "absent")
	if _, err := Execute(ctx, root, nil, feishucli.Request{Command: "targets", Action: "init"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("execute=%v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("cancelled call touched root: %v", err)
	}
}

func TestTargetSetUsesTransferredValueAndPreservesEnvelope(t *testing.T) {
	root := t.TempDir()
	service := feishu.NewCapabilityService(root, &recordingExecutor{}, nil)
	request, err := feishucli.Parse([]string{"targets", "set", "message", "team", "--type", "chat_id", "--value-file", "-"}, strings.NewReader("oc_test_chat"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Execute(context.Background(), root, service, request)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.(map[string]any)
	if !ok || value["status"] != "saved" || value["alias"] != "team" {
		t.Fatalf("result=%#v", result)
	}
	config, err := feishu.NewClientConfigStore(root).Load()
	if err != nil || config.MessageTargets["team"].ID != "oc_test_chat" {
		t.Fatalf("config=%#v err=%v", config, err)
	}
}

func TestSnapshotAndDoctorDoNotDependOnTaskLinks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LARK_CLI_BIN", filepath.Join(root, "missing-cli"))
	call := &invocation{ctx: context.Background(), dataRoot: root}
	snapshot, err := call.clientAggregateSnapshot(root, feishu.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RuntimeKind != "go" || snapshot.TargetAliases == nil || snapshot.Queues == nil {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	for _, key := range []string{"codexAppServer", "desktopIPC", "ksfContext"} {
		if _, ok := snapshot.Capabilities[key]; ok {
			t.Fatalf("non-Feishu capability %s", key)
		}
	}
	doctor := call.nativeDoctor(root, feishu.Settings{Version: 1, Profile: feishu.ProfileManualOnly, Codex: feishu.CodexSettings{DefaultThreadTitle: "test"}}, feishu.CapabilityExecutor{Binary: filepath.Join(root, "missing-cli")})
	encoded, _ := json.Marshal([]any{snapshot, doctor})
	for _, term := range []string{"taskLink", "task_links", "\"links\""} {
		if strings.Contains(string(encoded), term) {
			t.Fatalf("task state in Feishu snapshot or doctor: %s", encoded)
		}
	}
}

func TestStaticCatalogMatchesRuntimeManifest(t *testing.T) {
	manifest, err := feishu.LoadCapabilityManifest()
	if err != nil {
		t.Fatal(err)
	}
	expected := []any{}
	for _, definition := range manifest.Capabilities {
		if feishu.CapabilityPublished(definition) {
			expected = append(expected, publicCapability(definition))
		}
	}
	value, local, err := feishucli.Static(feishucli.Request{Command: "capability", Action: "catalog"})
	if err != nil || !local {
		t.Fatalf("local=%v err=%v", local, err)
	}
	actualJSON, _ := json.Marshal(value)
	expectedJSON, _ := json.Marshal(map[string]any{"status": "ok", "count": len(expected), "capabilities": expected})
	var actual, expectedValue any
	_ = json.Unmarshal(actualJSON, &actual)
	_ = json.Unmarshal(expectedJSON, &expectedValue)
	if !reflect.DeepEqual(actual, expectedValue) {
		t.Fatal("embedded CLI catalog drifted; regenerate with cataloggen")
	}
}
