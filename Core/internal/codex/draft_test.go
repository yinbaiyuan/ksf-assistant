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
			result = map[string]any{"thread": map[string]any{"id": "draft-test"}}
		}
		response := map[string]any{"id": request["id"], "result": result}
		if method == "thread/inject_items" && os.Getenv("KSFA_DRAFT_RPC_REJECT") == "1" {
			delete(response, "result")
			response["error"] = map[string]any{"code": -32600, "message": "injection rejected"}
		}
		json.NewEncoder(os.Stdout).Encode(response)
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
