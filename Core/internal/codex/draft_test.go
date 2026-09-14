package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// This test executable doubles as an isolated stdio App Server. No credentials,
// network, model calls, or user sessions are used by protocol regression tests.
func init() {
	if os.Getenv("KSFA_DRAFT_RPC_FIXTURE") != "1" {
		return
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var request map[string]any
		if json.Unmarshal(scan.Bytes(), &request) != nil {
			os.Exit(2)
		}
		f, err := os.OpenFile(os.Getenv("KSFA_DRAFT_RPC_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(3)
		}
		fmt.Fprintln(f, string(scan.Bytes()))
		f.Close()
		if request["id"] == nil {
			continue
		}
		result := any(map[string]any{})
		method, _ := request["method"].(string)
		if method == "thread/start" {
			thread := map[string]any{"id": "draft-test"}
			if cwd := os.Getenv("KSFA_DRAFT_THREAD_CWD"); cwd != "" {
				thread["cwd"] = cwd
			}
			result = map[string]any{"thread": thread}
		} else if method == "turn/start" {
			result = map[string]any{"turn": map[string]any{"id": "turn-test"}}
		} else if method == "thread/list" && os.Getenv("KSFA_ARCHIVE_RPC_FIXTURE") == "1" {
			params, _ := request["params"].(map[string]any)
			if params["archived"] != true {
				os.Exit(4)
			}
			if params["cursor"] == "next" {
				result = map[string]any{"data": []any{map[string]any{"id": "archived-2"}}}
			} else {
				result = map[string]any{"data": []any{map[string]any{"id": "archived-1"}}, "nextCursor": "next"}
			}
		}
		response := map[string]any{"id": request["id"], "result": result}
		if method == "thread/inject_items" && os.Getenv("KSFA_DRAFT_RPC_REJECT") == "1" {
			delete(response, "result")
			response["error"] = map[string]any{"code": -32600, "message": "injection rejected"}
		}
		json.NewEncoder(os.Stdout).Encode(response)
		if method == "turn/start" && os.Getenv("KSFA_DRAFT_APPROVAL_METHOD") != "" {
			json.NewEncoder(os.Stdout).Encode(map[string]any{
				"id": "approval-1", "method": os.Getenv("KSFA_DRAFT_APPROVAL_METHOD"),
				"params": map[string]any{"threadId": "draft-test", "turnId": "turn-test", "command": "must-not-leak"},
			})
			if os.Getenv("KSFA_DRAFT_RESOLVE_APPROVAL") == "1" {
				time.Sleep(20 * time.Millisecond)
				json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "draft-test", "requestId": "approval-1"}})
			}
		}
	}
	os.Exit(0)
}

func TestDraftPersistsOnlyContextWithoutStartingTurnAndReleasesWriter(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			t.Setenv("KSFA_DRAFT_RPC_FIXTURE", "1")
			path := t.TempDir() + "/rpc.jsonl"
			t.Setenv("KSFA_DRAFT_RPC_LOG", path)
			if reject {
				t.Setenv("KSFA_DRAFT_RPC_REJECT", "1")
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			client := &Client{Executable: binary, Timeout: time.Second}
			defer client.Close()
			if err := client.Call(context.Background(), "account/rateLimits/read", nil, nil); err != nil {
				t.Fatal(err)
			}
			originalProcess := client.cmd
			prompt := "项目上下文\n保留原文 & <文本>；不要修改文件。"
			cwd := t.TempDir()
			id, err := client.CreateDraftThread(context.Background(), cwd, "项目 · 新任务")
			if reject {
				if err == nil || id != "" || !strings.Contains(err.Error(), "injection rejected") {
					t.Fatalf("failure lost: %q %v", id, err)
				}
			} else if err != nil || id != "draft-test" {
				t.Fatalf("draft: %q %v", id, err)
			}
			if client.cmd != originalProcess {
				t.Fatal("draft disrupted long-lived account/bridge process")
			}
			if err := client.Call(context.Background(), "account/rateLimits/read", nil, nil); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			injections := 0
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var request struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &request); err != nil {
					t.Fatal(err)
				}
				if request.Method == "turn/start" {
					t.Fatal("background process started the model")
				}
				if request.Method == "thread/inject_items" {
					injections++
					item := request.Params["items"].([]any)[0].(map[string]any)
					content := item["content"].([]any)[0].(map[string]any)
					var metadata map[string]string
					text, _ := content["text"].(string)
					decodeErr := json.Unmarshal([]byte(text), &metadata)
					if item["role"] != "user" || decodeErr != nil || len(metadata) != 2 || metadata["taskName"] != "项目 · 新任务" || metadata["workingDirectory"] != cwd || strings.Contains(text, prompt) {
						t.Fatalf("input changed: %#v", item)
					}
				}
			}
			if injections != 1 {
				t.Fatalf("input injected %d times", injections)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.exitDone:
			default:
				t.Fatal("Close returned before process exit")
			}
		})
	}
}

func TestBridgeProjectlessThreadOmitsWorkingDirectoryOverrides(t *testing.T) {
	t.Setenv("KSFA_DRAFT_RPC_FIXTURE", "1")
	path := t.TempDir() + "/rpc.jsonl"
	t.Setenv("KSFA_DRAFT_RPC_LOG", path)
	t.Setenv("KSFA_DRAFT_THREAD_CWD", "/Users/example")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{Executable: binary, Timeout: time.Second}
	defer client.Close()
	thread, err := client.StartBridgeThread(context.Background(), "", "无项目任务")
	if err != nil || thread.ID != "draft-test" || thread.CWD != "/Users/example" || thread.ProjectID != "" {
		t.Fatalf("start thread: %#v %v", thread, err)
	}
	if _, err := client.StartBridgeTurn(context.Background(), thread.ID, "", "测试"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var request struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "thread/start" && request.Method != "turn/start" {
			continue
		}
		if _, found := request.Params["cwd"]; found {
			t.Fatalf("%s sent a cwd override for a projectless task: %#v", request.Method, request.Params)
		}
		if _, found := request.Params["projectId"]; found {
			t.Fatalf("%s assigned a project to a projectless task: %#v", request.Method, request.Params)
		}
		for _, field := range []string{"approvalPolicy", "sandbox", "sandboxPolicy"} {
			if _, found := request.Params[field]; found {
				t.Fatalf("%s overrode Codex permission field %s: %#v", request.Method, field, request.Params)
			}
		}
	}
}

func TestBridgeApprovalIsCancelledAndInterruptedWithoutPayloadProjection(t *testing.T) {
	for _, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval"} {
		t.Run(method, func(t *testing.T) {
			t.Setenv("KSFA_DRAFT_RPC_FIXTURE", "1")
			t.Setenv("KSFA_DRAFT_APPROVAL_METHOD", method)
			path := t.TempDir() + "/rpc.jsonl"
			t.Setenv("KSFA_DRAFT_RPC_LOG", path)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			client := &Client{Executable: binary, Timeout: time.Second}
			defer client.Close()
			if _, err := client.StartBridgeTurn(context.Background(), "draft-test", "", "测试"); err != nil {
				t.Fatal(err)
			}
			var pending ServerRequest
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
				if value, found := client.PendingBridgeUserInput("draft-test"); found {
					pending = value
					break
				}
			}
			if pending.Method != method || len(pending.Questions) != 0 {
				t.Fatalf("approval classification lost: %#v", pending)
			}
			if err := client.CancelBridgeApproval(context.Background(), "draft-test", "turn-test", pending.ID, pending.Method); err != nil {
				t.Fatal(err)
			}
			if _, found := client.PendingBridgeUserInput("draft-test"); found {
				t.Fatal("cancelled approval remained pending")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var cancellation, interruption map[string]any
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var request map[string]any
				if json.Unmarshal([]byte(line), &request) != nil {
					continue
				}
				if request["id"] == "approval-1" {
					cancellation = request
				}
				if request["method"] == "turn/interrupt" {
					interruption = request
				}
			}
			result, _ := cancellation["result"].(map[string]any)
			if method == "item/permissions/requestApproval" {
				permissions, ok := result["permissions"].(map[string]any)
				if !ok || len(permissions) != 0 {
					t.Fatalf("permissions were granted: %#v", cancellation)
				}
			} else if result["decision"] != "cancel" {
				t.Fatalf("approval was not cancelled: %#v", cancellation)
			}
			encoded, _ := json.Marshal(cancellation)
			if interruption == nil || strings.Contains(string(encoded), "must-not-leak") {
				t.Fatalf("turn was not interrupted or payload leaked: %#v %#v", cancellation, interruption)
			}
		})
	}
}

func TestServerRequestResolvedClearsCancelledRequestCache(t *testing.T) {
	t.Setenv("KSFA_DRAFT_RPC_FIXTURE", "1")
	t.Setenv("KSFA_DRAFT_APPROVAL_METHOD", "item/commandExecution/requestApproval")
	t.Setenv("KSFA_DRAFT_RESOLVE_APPROVAL", "1")
	t.Setenv("KSFA_DRAFT_RPC_LOG", t.TempDir()+"/rpc.jsonl")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{Executable: binary, Timeout: time.Second}
	defer client.Close()
	if _, err := client.StartBridgeTurn(context.Background(), "draft-test", "", "测试"); err != nil {
		t.Fatal(err)
	}
	seen := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		_, found := client.PendingBridgeUserInput("draft-test")
		seen = seen || found
		if seen && !found {
			return
		}
	}
	t.Fatalf("resolved server request lifecycle invalid; observed pending=%v", seen)
}
