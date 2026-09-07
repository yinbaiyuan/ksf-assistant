package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"ksfassistant/core/internal/taskruntime"
)

func TestCoreClosedCLIAndCrossProcessCAS(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("preserved root governance"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	privateHome := t.TempDir()
	binary := filepath.Join(t.TempDir(), "ksf-assistant-task")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone CLI: %v %s", err, output)
	}
	identity := "synthetic-private-thread-core-closed"
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "CODEX_THREAD_ID":
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "HOME="+privateHome, "USERPROFILE="+privateHome, "APPDATA="+filepath.Join(privateHome, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(privateHome, "AppData", "Local"), "XDG_CONFIG_HOME="+filepath.Join(privateHome, ".config"), "CODEX_THREAD_ID="+identity)
	run := func(operation string, input []byte) (taskruntime.Response, error) {
		arguments := []string{operation, "--root", root, "--host", "codex"}
		if operation == "--version" {
			arguments = []string{operation}
		}
		command := exec.Command(binary, arguments...)
		command.Env = environment
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		var response taskruntime.Response
		if parseErr := json.Unmarshal(output, &response); parseErr != nil {
			return response, parseErr
		}
		return response, err
	}
	version, err := run("--version", nil)
	if err != nil || !version.OK || version.SoftwareVersion != "0.11.0-preview.4" || version.Version != 1 {
		t.Fatal("version probe failed", version, err)
	}
	input := []byte(`{"protocol":"ksfassistant-task-runtime-v1","version":1,"event_id":"create","expected_revision":0,"state":{"scope":"unresolved","reported_status":"running"}}`)
	created, err := run("report", input)
	if err != nil || !created.OK || created.Task == nil || created.Task.Revision != 1 {
		t.Fatal("Core-closed report failed", created, err)
	}
	readInput := []byte(`{"protocol":"ksfassistant-task-runtime-v1","version":1}`)
	for _, operation := range []string{"get", "list", "doctor"} {
		response, err := run(operation, readInput)
		if err != nil || !response.OK {
			t.Fatalf("Core-closed %s: %+v %v", operation, response, err)
		}
	}
	var workers sync.WaitGroup
	responses := make(chan taskruntime.Response, 8)
	for index := 0; index < 8; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			request := map[string]any{"protocol": taskruntime.Protocol, "version": 1, "event_id": string(rune('a' + index)), "expected_revision": 1, "state": map[string]any{"scope": "unresolved", "reported_status": "waiting"}}
			encoded, _ := json.Marshal(request)
			response, _ := run("report", encoded)
			responses <- response
		}(index)
	}
	workers.Wait()
	close(responses)
	winners := 0
	for response := range responses {
		if response.OK {
			winners++
		} else if response.Error == nil || response.Error.Code != "revision_conflict" {
			t.Fatalf("unexpected CAS response: %+v", response)
		}
	}
	if winners != 1 {
		t.Fatalf("cross-process CAS had %d winners", winners)
	}
	current, err := run("get", readInput)
	if err != nil || current.Task.Revision != 2 || len(current.History) != 2 {
		t.Fatal("cross-process history lost", err)
	}
	config := filepath.Join(privateHome, ".config")
	if runtime.GOOS == "darwin" {
		config = filepath.Join(privateHome, "Library", "Application Support")
	}
	if runtime.GOOS == "windows" {
		config = filepath.Join(privateHome, "AppData", "Roaming")
	}
	keyPath := filepath.Join(config, "KSFAssistant", "task-runtime-v1", "identity.key")
	key, err := os.ReadFile(keyPath)
	if err != nil || len(key) != 32 {
		t.Fatal("default per-user key location mismatch", err)
	}
	adapter, err := taskruntime.Open(root, taskruntime.Options{KeyDirectory: filepath.Dir(keyPath)})
	if err != nil {
		t.Fatal(err)
	}
	adapterID, err := adapter.TaskID(context.Background(), "codex", identity)
	if err != nil || adapterID != created.Task.TaskID {
		t.Fatal("CLI and read adapter HMAC identity mismatch", err)
	}
	dataRoot := filepath.Join(root, ".agents", "runtime-data", "ksfassistant", "tasks-v1")
	if err := filepath.WalkDir(dataRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte(identity)) || bytes.Contains(data, key) || strings.Contains(path, identity) {
				t.Error("raw identity or key leaked to task store")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
