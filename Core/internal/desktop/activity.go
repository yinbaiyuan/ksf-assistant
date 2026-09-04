package desktop

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codexusagebar/core/internal/domain"
)

const maxFrameBytes = 64 * 1024 * 1024

type taskKey struct{ hostID, threadID string }
type ipcCallResponse struct {
	result map[string]any
	err    error
}

type ActivityClient struct {
	endpoint string

	mu              sync.Mutex
	writeMu         sync.Mutex
	connection      net.Conn
	clientID        string
	started         bool
	sequence        int
	availability    string
	followedBy      map[taskKey]map[string]bool
	owners          map[taskKey]string
	observations    map[taskKey]domain.TaskObservation
	pendingOwners   map[string]taskKey
	ownerWaiters    map[string]chan string
	requestWaiters  map[string]chan error
	responseWaiters map[string]chan ipcCallResponse
	candidateKeys   map[taskKey]bool
	states          map[taskKey]map[string]any
}

func DefaultEndpoint(home string) string {
	if value := strings.TrimSpace(os.Getenv("CODEX_DESKTOP_IPC_PATH")); value != "" {
		return value
	}
	if runtime.GOOS == "darwin" {
		root := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		return filepath.Join(root, "ipc", "ipc.sock")
	}
	return ""
}

func New(endpoint string) *ActivityClient {
	return &ActivityClient{endpoint: endpoint, availability: "loading", followedBy: map[taskKey]map[string]bool{}, owners: map[taskKey]string{}, observations: map[taskKey]domain.TaskObservation{}, pendingOwners: map[string]taskKey{}, ownerWaiters: map[string]chan string{}, requestWaiters: map[string]chan error{}, responseWaiters: map[string]chan ipcCallResponse{}, candidateKeys: map[taskKey]bool{}, states: map[taskKey]map[string]any{}}
}

func (client *ActivityClient) Start(ctx context.Context) error {
	client.mu.Lock()
	if client.started && client.connection != nil {
		client.mu.Unlock()
		return nil
	}
	if client.endpoint == "" {
		client.availability = "unsupportedProtocol"
		client.mu.Unlock()
		return errors.New("Codex Desktop IPC endpoint is not configured")
	}
	client.mu.Unlock()
	connectCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	connection, err := dial(connectCtx, client.endpoint)
	if err != nil {
		client.mu.Lock()
		client.availability = "offline"
		client.mu.Unlock()
		return err
	}
	client.mu.Lock()
	client.connection = connection
	client.started = true
	client.availability = "loading"
	client.mu.Unlock()
	go client.readLoop(connection)
	if err := client.send(map[string]any{"type": "request", "requestId": client.nextID("initialize"), "method": "initialize", "params": map[string]any{"clientType": "codex-usage-bar"}}); err != nil {
		client.Close()
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		ready := client.clientID != ""
		client.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			client.Close()
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	client.Close()
	return errors.New("Codex Desktop IPC initialization timed out")
}

func (client *ActivityClient) ReconcileCandidates(threadIDs []string) {
	client.mu.Lock()
	next := map[taskKey]bool{}
	for _, id := range threadIDs {
		if id != "" {
			next[taskKey{hostID: "local", threadID: id}] = true
		}
	}
	client.candidateKeys = next
	ready := client.clientID != ""
	keys := []taskKey{}
	if ready {
		for key := range next {
			if client.owners[key] == "" {
				keys = append(keys, key)
			}
		}
	}
	client.mu.Unlock()
	for _, key := range keys {
		client.discoverOwner(key)
	}
}

func (client *ActivityClient) StartTurn(ctx context.Context, threadID, hostID, cwd, prompt string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(cwd) == "" || strings.TrimSpace(prompt) == "" {
		return errors.New("task submission requires a thread, working directory, and prompt")
	}
	if hostID == "" {
		hostID = "local"
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	client.mu.Lock()
	clientID := client.clientID
	client.mu.Unlock()
	if clientID == "" {
		return errors.New("Codex Desktop IPC is still initializing")
	}
	ownerRequestID := client.nextID("submit-owner")
	ownerWaiter := make(chan string, 1)
	client.mu.Lock()
	client.ownerWaiters[ownerRequestID] = ownerWaiter
	client.mu.Unlock()
	if err := client.send(map[string]any{"type": "request", "requestId": ownerRequestID, "sourceClientId": clientID, "method": "thread-owner-discovery", "version": 1, "params": map[string]any{"conversationId": threadID, "hostId": hostID}}); err != nil {
		client.mu.Lock()
		delete(client.ownerWaiters, ownerRequestID)
		client.mu.Unlock()
		return err
	}
	var owner string
	select {
	case owner = <-ownerWaiter:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("Codex Desktop did not claim the new task")
	}
	if owner == "" {
		return errors.New("Codex Desktop has no visible owner for the new task")
	}
	requestID := client.nextID("start-turn")
	waiter := make(chan error, 1)
	client.mu.Lock()
	client.requestWaiters[requestID] = waiter
	client.mu.Unlock()
	message := map[string]any{
		"type": "request", "requestId": requestID, "sourceClientId": clientID, "targetClientId": owner,
		"timeoutMs": 15000, "method": "thread-follower-start-turn", "version": 2,
		"params": map[string]any{"conversationId": threadID, "turnStart": map[string]any{
			"request": map[string]any{"threadId": threadID, "cwd": cwd, "input": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}},
			"context": map[string]any{"inheritThreadSettings": true},
		}},
	}
	if err := client.send(message); err != nil {
		client.mu.Lock()
		delete(client.requestWaiters, requestID)
		client.mu.Unlock()
		return err
	}
	select {
	case err := <-waiter:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return errors.New("Codex Desktop did not acknowledge the first turn")
	}
}

func (client *ActivityClient) Snapshot(now time.Time) domain.TaskActivitySnapshot {
	client.mu.Lock()
	values := make([]domain.TaskObservation, 0, len(client.observations))
	for _, value := range client.observations {
		values = append(values, value)
	}
	availability := client.availability
	client.mu.Unlock()
	result := domain.SummarizeActivity(values, now)
	result.Availability = availability
	return result
}

func (client *ActivityClient) Close() {
	client.mu.Lock()
	connection := client.connection
	client.connection = nil
	client.started = false
	client.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
}

func (client *ActivityClient) readLoop(connection net.Conn) {
	reader := bufio.NewReader(connection)
	for {
		var length uint32
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
			client.connectionEnded(connection)
			return
		}
		if length == 0 || length > maxFrameBytes {
			client.connectionEnded(connection)
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			client.connectionEnded(connection)
			return
		}
		client.handle(payload)
	}
}

func (client *ActivityClient) handle(payload []byte) {
	var envelope map[string]any
	if json.Unmarshal(payload, &envelope) != nil {
		return
	}
	typeName, _ := envelope["type"].(string)
	requestID := firstString(envelope, "requestId", "requestID")
	if typeName == "client-discovery-request" && requestID != "" {
		_ = client.send(map[string]any{"type": "client-discovery-response", "requestId": requestID, "response": map[string]any{"canHandle": false}})
		return
	}
	if typeName == "response" {
		result, _ := envelope["result"].(map[string]any)
		client.mu.Lock()
		if waiter := client.responseWaiters[requestID]; waiter != nil {
			delete(client.responseWaiters, requestID)
			var responseErr error
			if payload, ok := envelope["error"].(map[string]any); ok {
				responseErr = errors.New(firstString(payload, "message"))
			}
			client.mu.Unlock()
			waiter <- ipcCallResponse{result: result, err: responseErr}
			return
		}
		if waiter := client.ownerWaiters[requestID]; waiter != nil {
			delete(client.ownerWaiters, requestID)
			owner := firstString(result, "handledByClientId", "handledByClientID")
			if owner == "" {
				owner = firstString(envelope, "handledByClientId", "handledByClientID")
			}
			client.mu.Unlock()
			waiter <- owner
			return
		}
		if waiter := client.requestWaiters[requestID]; waiter != nil {
			delete(client.requestWaiters, requestID)
			var responseErr error
			if payload, ok := envelope["error"].(map[string]any); ok {
				message := firstString(payload, "message")
				if message == "" {
					message = "Codex Desktop rejected the request"
				}
				responseErr = errors.New(message)
			}
			client.mu.Unlock()
			waiter <- responseErr
			return
		}
		client.mu.Unlock()
		clientID := firstString(result, "clientId", "clientID")
		if clientID == "" {
			clientID = firstString(envelope, "clientId", "clientID")
		}
		if clientID != "" {
			client.mu.Lock()
			client.clientID = clientID
			client.availability = "available"
			keys := make([]taskKey, 0, len(client.candidateKeys))
			for key := range client.candidateKeys {
				keys = append(keys, key)
			}
			client.mu.Unlock()
			for _, key := range keys {
				client.discoverOwner(key)
			}
			return
		}
		owner := firstString(result, "handledByClientId", "handledByClientID")
		if owner == "" {
			owner = firstString(envelope, "handledByClientId", "handledByClientID")
		}
		client.mu.Lock()
		key, exists := client.pendingOwners[requestID]
		delete(client.pendingOwners, requestID)
		if exists && owner != "" {
			client.owners[key] = owner
		}
		client.mu.Unlock()
		if exists && owner != "" {
			client.follow(key, owner, true)
		}
		return
	}
	if typeName != "broadcast" {
		return
	}
	method, _ := envelope["method"].(string)
	params, _ := envelope["params"].(map[string]any)
	threadID := firstString(params, "conversationId", "conversationID")
	hostID := firstString(params, "hostId", "hostID")
	if hostID == "" {
		hostID = "local"
	}
	key := taskKey{hostID: hostID, threadID: threadID}
	source := firstString(envelope, "sourceClientId", "sourceClientID")
	switch method {
	case "thread-stream-following-changed":
		following, ok := params["following"].(bool)
		if !ok || source == "" || threadID == "" {
			return
		}
		client.mu.Lock()
		if client.followedBy[key] == nil {
			client.followedBy[key] = map[string]bool{}
		}
		if following {
			client.followedBy[key][source] = true
		} else {
			delete(client.followedBy[key], source)
		}
		client.mu.Unlock()
		if following {
			client.follow(key, source, true)
		}
	case "thread-stream-state-changed":
		change, _ := params["change"].(map[string]any)
		changeType, _ := change["type"].(string)
		if changeType == "snapshot" {
			state, _ := change["conversationState"].(map[string]any)
			client.mu.Lock()
			client.states[key] = state
			client.mu.Unlock()
			if observation, ok := parseObservation(key, state); ok {
				client.mu.Lock()
				client.observations[key] = observation
				client.mu.Unlock()
			}
		} else if source != "" {
			client.follow(key, source, true)
		}
	}
}

func (client *ActivityClient) requestFollower(ctx context.Context, threadID, method string, version int, params map[string]any) (map[string]any, error) {
	if err := client.Start(ctx); err != nil {
		return nil, err
	}
	owner, err := client.owner(ctx, threadID)
	if err != nil {
		return nil, err
	}
	client.mu.Lock()
	source := client.clientID
	client.mu.Unlock()
	requestID := client.nextID("follower")
	waiter := make(chan ipcCallResponse, 1)
	client.mu.Lock()
	client.responseWaiters[requestID] = waiter
	client.mu.Unlock()
	message := map[string]any{"type": "request", "requestId": requestID, "sourceClientId": source, "targetClientId": owner, "timeoutMs": 15000, "method": method, "version": version, "params": params}
	if err := client.send(message); err != nil {
		client.mu.Lock()
		delete(client.responseWaiters, requestID)
		client.mu.Unlock()
		return nil, err
	}
	select {
	case response := <-waiter:
		return response.result, response.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
		client.mu.Lock()
		delete(client.responseWaiters, requestID)
		client.mu.Unlock()
		return nil, errors.New("Codex Desktop follower request timed out")
	}
}

func (client *ActivityClient) owner(ctx context.Context, threadID string) (string, error) {
	key := taskKey{hostID: "local", threadID: threadID}
	client.mu.Lock()
	if owner := client.owners[key]; owner != "" {
		client.mu.Unlock()
		return owner, nil
	}
	source := client.clientID
	client.mu.Unlock()
	if source == "" {
		return "", errors.New("Codex Desktop IPC is still initializing")
	}
	owner, err := client.discoverOwnerSync(ctx, threadID, source)
	if err != nil {
		return "", err
	}
	if owner == "" && runtime.GOOS == "darwin" {
		if err := openDesktopTask(threadID); err != nil {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		owner, err = client.discoverOwnerSync(ctx, threadID, source)
		if err != nil {
			return "", err
		}
	}
	if owner == "" {
		return "", errors.New("Codex Desktop does not own the linked task")
	}
	client.mu.Lock()
	client.owners[key] = owner
	client.mu.Unlock()
	return owner, nil
}

func (client *ActivityClient) discoverOwnerSync(ctx context.Context, threadID, source string) (string, error) {
	requestID := client.nextID("owner")
	waiter := make(chan string, 1)
	client.mu.Lock()
	client.ownerWaiters[requestID] = waiter
	client.mu.Unlock()
	if err := client.send(map[string]any{"type": "request", "requestId": requestID, "sourceClientId": source, "method": "thread-owner-discovery", "version": 1, "params": map[string]any{"conversationId": threadID, "hostId": "local"}}); err != nil {
		return "", err
	}
	select {
	case owner := <-waiter:
		return owner, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Second):
		return "", errors.New("Codex Desktop owner discovery timed out")
	}
}

func openDesktopTask(threadID string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`).MatchString(threadID) {
		return errors.New("invalid Codex task id")
	}
	command := exec.Command("/usr/bin/open", "codex://threads/"+threadID)
	command.Stdin, command.Stdout, command.Stderr = nil, io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("open linked Codex task: %w", err)
	}
	return nil
}

func (client *ActivityClient) ReadConversationState(ctx context.Context, threadID string) (map[string]any, error) {
	key := taskKey{hostID: "local", threadID: threadID}
	client.mu.Lock()
	delete(client.states, key)
	source := client.clientID
	client.mu.Unlock()
	owner, err := client.owner(ctx, threadID)
	if err != nil {
		return nil, err
	}
	_ = client.send(map[string]any{"type": "broadcast", "sourceClientId": source, "method": "thread-stream-following-changed", "version": 1, "params": map[string]any{"conversationId": threadID, "hostId": "local", "following": true}})
	if _, err := client.requestFollower(ctx, threadID, "thread-follower-load-complete-history", 1, map[string]any{"conversationId": threadID}); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	_ = owner
	for time.Now().Before(deadline) {
		client.mu.Lock()
		state := client.states[key]
		client.mu.Unlock()
		if state != nil {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return nil, errors.New("Codex Desktop task snapshot timed out")
}

func (client *ActivityClient) StartBridgeTurn(ctx context.Context, threadID, cwd, text string, collaborationMode map[string]any) (string, error) {
	request := map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}, "cwd": cwd, "approvalPolicy": "never", "sandboxPolicy": map[string]any{"type": "dangerFullAccess"}}
	if collaborationMode != nil {
		request["collaborationMode"] = collaborationMode
	}
	result, err := client.requestFollower(ctx, threadID, "thread-follower-start-turn", 2, map[string]any{"conversationId": threadID, "turnStart": map[string]any{"request": request, "context": map[string]any{"inheritThreadSettings": true}}})
	if err != nil {
		return "", err
	}
	turnID := recursiveFirstString(result, "turnId", "id")
	if turnID == "" {
		return "", errors.New("Codex Desktop returned no turn id")
	}
	return turnID, nil
}
func (client *ActivityClient) SteerBridgeTurn(ctx context.Context, threadID, cwd, turnID, text string) (string, error) {
	result, err := client.requestFollower(ctx, threadID, "thread-follower-steer-turn", 1, map[string]any{"conversationId": threadID, "input": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}, "restoreMessage": map[string]any{"cwd": cwd, "context": map[string]any{"workspaceRoots": []string{cwd}, "collaborationMode": nil}, "responsesapiClientMetadata": map[string]any{}}, "attachments": []any{}, "clientUserMessageId": client.nextID("message")})
	if err != nil {
		return "", err
	}
	if value := recursiveFirstString(result, "turnId", "id"); value != "" {
		return value, nil
	}
	return turnID, nil
}
func (client *ActivityClient) InterruptBridgeTurn(ctx context.Context, threadID, turnID string) error {
	_, err := client.requestFollower(ctx, threadID, "thread-follower-interrupt-turn", 4, map[string]any{"conversationId": threadID, "mode": "user-stop", "expectedTurnId": turnID})
	return err
}
func (client *ActivityClient) SubmitBridgeUserInput(ctx context.Context, threadID, requestID string, response map[string]any) error {
	_, err := client.requestFollower(ctx, threadID, "thread-follower-submit-user-input", 1, map[string]any{"conversationId": threadID, "requestId": requestID, "response": response})
	return err
}
func recursiveFirstString(value any, names ...string) string {
	switch item := value.(type) {
	case map[string]any:
		for _, name := range names {
			if text, ok := item[name].(string); ok && text != "" {
				return text
			}
		}
		for _, child := range item {
			if text := recursiveFirstString(child, names...); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range item {
			if text := recursiveFirstString(child, names...); text != "" {
				return text
			}
		}
	}
	return ""
}

func parseObservation(key taskKey, state map[string]any) (domain.TaskObservation, bool) {
	runtimeStatus, _ := state["threadRuntimeStatus"].(map[string]any)
	status, _ := runtimeStatus["type"].(string)
	if status == "" {
		return domain.TaskObservation{}, false
	}
	flags := stringsFrom(runtimeStatus["activeFlags"])
	methods := []string{}
	pendingPlan := false
	walk(state, func(name string, value any) {
		lower := strings.ToLower(name)
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
	var nickname *string
	if value, ok := state["agentNickname"].(string); ok && value != "" {
		nickname = &value
	}
	var sourceKind *string
	switch source := state["source"].(type) {
	case string:
		sourceKind = &source
	case map[string]any:
		keys := make([]string, 0, len(source))
		for name := range source {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			sourceKind = &keys[0]
		}
	}
	return domain.TaskObservation{ID: key.threadID, HostID: key.hostID, AgentNickname: nickname, SourceKind: sourceKind, RuntimeStatus: status, ActiveFlags: unique(flags), PendingRequestMethods: unique(methods), HasPendingPlanImplementation: pendingPlan}, true
}

func (client *ActivityClient) discoverOwner(key taskKey) {
	requestID := client.nextID("owner")
	client.mu.Lock()
	client.pendingOwners[requestID] = key
	client.mu.Unlock()
	_ = client.send(map[string]any{"type": "request", "requestId": requestID, "method": "thread-owner-discovery", "version": 1, "params": map[string]any{"conversationId": key.threadID, "hostId": key.hostID}})
}

func (client *ActivityClient) follow(key taskKey, target string, following bool) {
	_ = client.send(map[string]any{"type": "broadcast", "method": "thread-stream-following-changed", "version": 1, "targetClientIds": []string{target}, "params": map[string]any{"conversationId": key.threadID, "hostId": key.hostID, "following": following}})
}

func (client *ActivityClient) send(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > maxFrameBytes {
		return fmt.Errorf("invalid Desktop IPC frame size")
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	client.mu.Lock()
	connection := client.connection
	client.mu.Unlock()
	if connection == nil {
		return errors.New("Codex Desktop IPC is not connected")
	}
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(payload)))
	if _, err := connection.Write(header); err != nil {
		return err
	}
	_, err = connection.Write(payload)
	return err
}

func (client *ActivityClient) nextID(prefix string) string {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.sequence++
	return fmt.Sprintf("codex-usage-core-%s-%d", prefix, client.sequence)
}

func (client *ActivityClient) connectionEnded(connection net.Conn) {
	client.mu.Lock()
	if client.connection == connection {
		client.connection = nil
		client.clientID = ""
		client.availability = "offline"
		client.observations = map[taskKey]domain.TaskObservation{}
		client.owners = map[taskKey]string{}
		client.pendingOwners = map[string]taskKey{}
		for id, waiter := range client.ownerWaiters {
			delete(client.ownerWaiters, id)
			waiter <- ""
		}
		for id, waiter := range client.requestWaiters {
			delete(client.requestWaiters, id)
			waiter <- errors.New("Codex Desktop IPC disconnected")
		}
		for id, waiter := range client.responseWaiters {
			delete(client.responseWaiters, id)
			waiter <- ipcCallResponse{err: errors.New("Codex Desktop IPC disconnected")}
		}
	}
	client.mu.Unlock()
	_ = connection.Close()
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if result, ok := value[key].(string); ok {
			return result
		}
	}
	return ""
}

func stringsFrom(value any) []string {
	result := []string{}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
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
