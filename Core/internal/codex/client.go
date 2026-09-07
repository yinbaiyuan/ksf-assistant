package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/domain"
)

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (err *RPCError) Error() string { return err.Message }

type response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

type Client struct {
	Executable string
	Timeout    time.Duration

	stateMu        sync.Mutex
	writeMu        sync.Mutex
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	pending        map[int]chan response
	serverRequests map[string]ServerRequest
	nextID         int
	closed         bool
}

type ServerRequest struct {
	ID        json.RawMessage
	Method    string
	ThreadID  string
	TurnID    string
	Questions []map[string]any
}

func LocateExecutable(home string) (string, error) {
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}
	candidates := []string{}
	if value := strings.TrimSpace(os.Getenv("CODEX_BIN")); value != "" {
		candidates = append(candidates, value)
	}
	// Match the installed Desktop runtime before falling back to standalone
	// CLIs. A stale PATH shim can accept turns but reject Desktop's newer model.
	if runtime.GOOS == "windows" {
		candidates = appendUnder(candidates, os.Getenv("LOCALAPPDATA"),
			filepath.Join("Programs", "Codex", "resources", "codex.exe"),
			filepath.Join("Programs", "Codex", "codex.exe"),
			filepath.Join("Codex", "codex.exe"),
		)
		candidates = appendUnder(candidates, os.Getenv("ProgramFiles"), filepath.Join("Codex", "resources", "codex.exe"), filepath.Join("Codex", "codex.exe"))
	} else if runtime.GOOS == "darwin" {
		for _, root := range []string{filepath.Join(home, "Applications"), "/Applications"} {
			for _, app := range []string{"Codex.app", "ChatGPT.app"} {
				candidates = append(candidates, filepath.Join(root, app, "Contents", "Resources", "codex"))
			}
		}
	}
	if found, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, found)
	}
	candidates = append(candidates, filepath.Join(home, ".local", "bin", name))
	if runtime.GOOS == "windows" {
		candidates = appendUnder(candidates, os.Getenv("LOCALAPPDATA"), filepath.Join("Microsoft", "WindowsApps", "codex.exe"))
	} else {
		candidates = append(candidates,
			"/opt/homebrew/bin/codex",
			"/usr/local/bin/codex",
		)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil || seen[strings.ToLower(absolute)] {
			continue
		}
		seen[strings.ToLower(absolute)] = true
		if info, err := os.Stat(absolute); err == nil && !info.IsDir() {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("Codex executable was not found")
}

func appendUnder(candidates []string, root string, relativePaths ...string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return candidates
	}
	for _, relativePath := range relativePaths {
		candidates = append(candidates, filepath.Join(root, relativePath))
	}
	return candidates
}

func (client *Client) Start(ctx context.Context) error {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	if client.cmd != nil && client.cmd.Process != nil {
		return nil
	}
	if client.closed {
		return errors.New("Codex App Server client is closed")
	}
	timeout := client.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
		client.Timeout = timeout
	}
	command := exec.Command(client.Executable, "app-server", "--listen", "stdio://")
	command.Dir, _ = os.UserHomeDir()
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		setHiddenWindow(command)
	}
	if err := command.Start(); err != nil {
		return err
	}
	client.cmd = command
	client.stdin = stdin
	client.pending = map[int]chan response{}
	client.serverRequests = map[string]ServerRequest{}
	go io.Copy(io.Discard, stderr)
	go client.readLoop(stdout, command)
	client.stateMu.Unlock()
	_, initErr := client.callStarted(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "ksf_assistant_core", "title": "KSFAssistant Core", "version": "0.11.0-preview.4"}})
	if initErr == nil {
		initErr = client.notify("initialized", map[string]any{})
	}
	client.stateMu.Lock()
	if initErr != nil {
		client.stopLocked(initErr)
	}
	return initErr
}

func (client *Client) Call(ctx context.Context, method string, params any, target any) error {
	if err := client.Start(ctx); err != nil {
		return err
	}
	raw, err := client.callStarted(ctx, method, params)
	if err != nil {
		return err
	}
	if target == nil || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("Codex App Server returned invalid %s data: %w", method, err)
	}
	return nil
}

func (client *Client) FetchRateLimits(ctx context.Context) (domain.RateLimitsResponse, error) {
	var result domain.RateLimitsResponse
	err := client.Call(ctx, "account/rateLimits/read", nil, &result)
	return result, err
}

func (client *Client) FetchTokenUsage(ctx context.Context) (domain.TokenUsageResponse, error) {
	var result domain.TokenUsageResponse
	err := client.Call(ctx, "account/usage/read", nil, &result)
	return result, err
}

func (client *Client) FetchThreads(ctx context.Context) ([]domain.CodexThread, error) {
	result := []domain.CodexThread{}
	var cursor *string
	for len(result) < 10000 {
		params := map[string]any{
			"limit": 100, "archived": false, "sortKey": "updated_at", "sortDirection": "desc",
			"sourceKinds": []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"},
		}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var page struct {
			Data       []domain.CodexThread `json:"data"`
			NextCursor *string              `json:"nextCursor"`
		}
		if err := client.Call(ctx, "thread/list", params, &page); err != nil {
			return nil, err
		}
		result = append(result, page.Data...)
		cursor = page.NextCursor
		if cursor == nil || *cursor == "" {
			break
		}
	}
	return result, nil
}

func (client *Client) CreateDraftThread(ctx context.Context, cwd, name string) (string, error) {
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := client.Call(ctx, "thread/start", map[string]any{"cwd": cwd, "serviceName": "ksf_assistant"}, &started); err != nil {
		return "", err
	}
	if started.Thread.ID == "" {
		return "", errors.New("thread/start returned an empty thread id")
	}
	if err := client.Call(ctx, "thread/name/set", map[string]any{"threadId": started.Thread.ID, "name": name}, nil); err != nil {
		return "", err
	}
	return started.Thread.ID, nil
}

func (client *Client) StartBridgeThread(ctx context.Context, cwd, name string) (string, error) {
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	params := map[string]any{
		"cwd": cwd, "approvalPolicy": "never", "sandbox": "danger-full-access",
		"personality": "pragmatic", "serviceName": "codex_feishu_bridge_go", "threadSource": "user", "ephemeral": false,
		"developerInstructions": "The user is interacting through an authorized Feishu bridge. Never expose credentials or hidden identifiers. Ask for desktop interaction when secret input or approval is required.",
	}
	if err := client.Call(ctx, "thread/start", params, &started); err != nil {
		return "", err
	}
	if started.Thread.ID == "" {
		return "", errors.New("thread/start returned an empty thread id")
	}
	if strings.TrimSpace(name) != "" {
		_ = client.Call(ctx, "thread/name/set", map[string]any{"threadId": started.Thread.ID, "name": name}, nil)
	}
	return started.Thread.ID, nil
}

func (client *Client) StartBridgeTurn(ctx context.Context, threadID, cwd, text string) (string, error) {
	return client.StartBridgeTurnWithMode(ctx, threadID, cwd, text, nil)
}

func (client *Client) StartBridgeTurnWithMode(ctx context.Context, threadID, cwd, text string, collaborationMode map[string]any) (string, error) {
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{
		"threadId": threadID, "input": []map[string]any{{"type": "text", "text": text}}, "cwd": cwd,
		"approvalPolicy": "never", "sandboxPolicy": map[string]any{"type": "dangerFullAccess"}, "summary": "auto",
	}
	if collaborationMode != nil {
		params["collaborationMode"] = collaborationMode
	}
	if err := client.Call(ctx, "turn/start", params, &started); err != nil {
		return "", err
	}
	if started.Turn.ID == "" {
		return "", errors.New("turn/start returned an empty turn id")
	}
	return started.Turn.ID, nil
}

func (client *Client) SteerBridgeTurn(ctx context.Context, threadID, turnID, text string) (string, error) {
	var result struct {
		TurnID string `json:"turnId"`
		Turn   struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	err := client.Call(ctx, "turn/steer", map[string]any{"threadId": threadID, "expectedTurnId": turnID, "input": []map[string]any{{"type": "text", "text": text}}}, &result)
	if err != nil {
		return "", err
	}
	if result.TurnID != "" {
		return result.TurnID, nil
	}
	if result.Turn.ID != "" {
		return result.Turn.ID, nil
	}
	return turnID, nil
}

func (client *Client) ReadBridgeThread(ctx context.Context, threadID string) (map[string]any, error) {
	var value map[string]any
	if err := client.Call(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": true}, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (client *Client) InterruptBridgeTurn(ctx context.Context, threadID, turnID string) error {
	return client.Call(ctx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID}, nil)
}

func (client *Client) PendingBridgeUserInput(threadID string) (ServerRequest, bool) {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	value, ok := client.serverRequests[threadID]
	return value, ok
}

func (client *Client) AnswerBridgeUserInput(threadID string, answers map[string]any) error {
	client.stateMu.Lock()
	request, ok := client.serverRequests[threadID]
	if ok {
		delete(client.serverRequests, threadID)
	}
	client.stateMu.Unlock()
	if !ok {
		return errors.New("Codex user input request is no longer pending")
	}
	return client.write(map[string]any{"id": request.ID, "result": map[string]any{"answers": answers}})
}

func Observations(threads []domain.CodexThread) []domain.TaskObservation {
	result := make([]domain.TaskObservation, 0, len(threads))
	for _, thread := range threads {
		status := thread.Status.Type
		if status == "" {
			status = "notLoaded"
		}
		source := sourceKind(thread.Source)
		observation := domain.TaskObservation{ID: thread.ID, HostID: "local", AgentNickname: thread.AgentNickname, SourceKind: source, RuntimeStatus: status, ActiveFlags: unique(thread.Status.ActiveFlags)}
		methods := []string{}
		pendingPlan := false
		for _, turn := range thread.Turns {
			walk(turn, func(key string, value any) {
				lower := strings.ToLower(key)
				if lower == "method" {
					if method, ok := value.(string); ok && strings.Contains(strings.ToLower(method), "request") {
						methods = append(methods, method)
					}
				}
				if strings.Contains(lower, "planimplementation") {
					if flag, ok := value.(bool); ok && flag {
						pendingPlan = true
					}
				}
			})
		}
		observation.PendingRequestMethods = unique(methods)
		observation.HasPendingPlanImplementation = pendingPlan
		result = append(result, observation)
	}
	return result
}

func (client *Client) Close() error {
	client.stateMu.Lock()
	defer client.stateMu.Unlock()
	client.closed = true
	client.stopLocked(errors.New("Codex App Server client closed"))
	return nil
}

func (client *Client) callStarted(ctx context.Context, method string, params any) (json.RawMessage, error) {
	client.stateMu.Lock()
	client.nextID++
	id := client.nextID
	channel := make(chan response, 1)
	client.pending[id] = channel
	client.stateMu.Unlock()

	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	if err := client.write(request); err != nil {
		client.removePending(id)
		return nil, err
	}
	timeout := client.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case value := <-channel:
		if value.Error != nil {
			return nil, value.Error
		}
		return value.Result, nil
	case <-ctx.Done():
		client.removePending(id)
		return nil, ctx.Err()
	case <-timer.C:
		client.removePending(id)
		return nil, fmt.Errorf("Codex App Server did not respond to %s within %s", method, timeout)
	}
}

func (client *Client) notify(method string, params any) error {
	return client.write(map[string]any{"method": method, "params": params})
}

func (client *Client) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	client.stateMu.Lock()
	stdin := client.stdin
	client.stateMu.Unlock()
	if stdin == nil {
		return errors.New("Codex App Server is not running")
	}
	_, err = stdin.Write(append(data, '\n'))
	return err
}

func (client *Client) readLoop(stdout io.Reader, command *exec.Cmd) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var requestEnvelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(line, &requestEnvelope) == nil && requestEnvelope.Method != "" && len(requestEnvelope.ID) > 0 && string(requestEnvelope.ID) != "null" {
			threadID := recursiveString(requestEnvelope.Params, "threadId", "thread_id")
			turnID := recursiveString(requestEnvelope.Params, "turnId", "turn_id")
			questions := recursiveQuestions(requestEnvelope.Params)
			if threadID != "" {
				client.stateMu.Lock()
				client.serverRequests[threadID] = ServerRequest{ID: append(json.RawMessage(nil), requestEnvelope.ID...), Method: requestEnvelope.Method, ThreadID: threadID, TurnID: turnID, Questions: questions}
				client.stateMu.Unlock()
			}
			continue
		}
		var envelope struct {
			ID *int `json:"id"`
		}
		if json.Unmarshal(line, &envelope) != nil || envelope.ID == nil {
			continue
		}
		var value response
		if json.Unmarshal(line, &value) != nil {
			continue
		}
		client.stateMu.Lock()
		channel := client.pending[value.ID]
		delete(client.pending, value.ID)
		client.stateMu.Unlock()
		if channel != nil {
			channel <- value
		}
	}
	_ = command.Wait()
	client.stateMu.Lock()
	if client.cmd == command {
		client.stopLocked(errors.New("Codex App Server stopped"))
	}
	client.stateMu.Unlock()
}

func recursiveString(value any, names ...string) string {
	switch item := value.(type) {
	case map[string]any:
		for _, name := range names {
			if text, ok := item[name].(string); ok && text != "" {
				return text
			}
		}
		for _, child := range item {
			if text := recursiveString(child, names...); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range item {
			if text := recursiveString(child, names...); text != "" {
				return text
			}
		}
	}
	return ""
}
func recursiveQuestions(value any) []map[string]any {
	switch item := value.(type) {
	case map[string]any:
		if raw, ok := item["questions"].([]any); ok {
			result := []map[string]any{}
			for _, entry := range raw {
				if question, ok := entry.(map[string]any); ok {
					result = append(result, question)
				}
			}
			if len(result) > 0 {
				return result
			}
		}
		for _, child := range item {
			if result := recursiveQuestions(child); len(result) > 0 {
				return result
			}
		}
	case []any:
		for _, child := range item {
			if result := recursiveQuestions(child); len(result) > 0 {
				return result
			}
		}
	}
	return nil
}

func (client *Client) removePending(id int) {
	client.stateMu.Lock()
	delete(client.pending, id)
	client.stateMu.Unlock()
}

func (client *Client) stopLocked(reason error) {
	if client.stdin != nil {
		_ = client.stdin.Close()
	}
	if client.cmd != nil && client.cmd.Process != nil {
		_ = client.cmd.Process.Kill()
	}
	for id, channel := range client.pending {
		delete(client.pending, id)
		channel <- response{ID: id, Error: &RPCError{Message: reason.Error()}}
	}
	client.cmd = nil
	client.stdin = nil
	client.serverRequests = map[string]ServerRequest{}
}

func sourceKind(value any) *string {
	switch typed := value.(type) {
	case string:
		return &typed
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return &keys[0]
		}
	}
	return nil
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func walk(value any, visit func(string, any)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			visit(key, child)
			walk(child, visit)
		}
	case []any:
		for _, child := range typed {
			walk(child, visit)
		}
	}
}
