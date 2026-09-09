package desktop

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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

	"ksfassistant/core/internal/domain"
)

const maxFrameBytes = 64 * 1024 * 1024

type taskKey struct{ hostID, threadID string }
type snapshotKey struct {
	task  taskKey
	owner string
}
type ipcCallResponse struct {
	result map[string]any
	err    error
}
type snapshotResponse struct {
	state    map[string]any
	source   string
	revision string
	err      error
}
type snapshotWaiter struct {
	response chan snapshotResponse
	accept   func(map[string]any, string) bool
}

type UserInputTarget struct {
	ObservationEpoch       uint64
	ThreadID               string
	TurnID                 string
	OwnerClientID          string
	SnapshotSourceClientID string
	SnapshotRevision       string
	RequestID              RequestRef
	State                  map[string]any
}

type UserInputVerification struct {
	State            map[string]any
	SnapshotRevision string
	RequestConsumed  bool
	TurnRunning      bool
}

type PendingUserInputDefinition struct {
	TurnID    string
	Questions []map[string]any
}

// UserInputSession is an isolated IPC connection for a one-shot Desktop
// requestUserInput exchange. Isolation mirrors the proven Node bridge: the
// connection follows only one task, so proxy-authored snapshots cannot be
// confused with unrelated long-lived subscriptions.
type UserInputSession struct{ client *ActivityClient }

type ActivityClient struct {
	endpoint    string
	clientType  string
	controlOnly bool

	mu                   sync.Mutex
	writeMu              sync.Mutex
	connection           net.Conn
	clientID             string
	started              bool
	sequence             int
	availability         string
	followedBy           map[taskKey]map[string]bool
	connectionGeneration uint64
	observationCache     map[taskKey]*observationCache
	observationCounters  ObservationCounters
	owners               map[taskKey]string
	observations         map[taskKey]domain.TaskObservation
	pendingOwners        map[string]taskKey
	ownerWaiters         map[string]chan string
	requestWaiters       map[string]chan error
	responseWaiters      map[string]chan ipcCallResponse
	candidateKeys        map[taskKey]bool
	states               map[taskKey]map[string]any
	snapshotWaiters      map[snapshotKey][]snapshotWaiter
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
	return NewWithClientType(endpoint, "ksf-assistant")
}

func NewWithClientType(endpoint, clientType string) *ActivityClient {
	if strings.TrimSpace(clientType) == "" {
		clientType = "ksf-assistant"
	}
	return &ActivityClient{endpoint: endpoint, clientType: clientType, availability: "loading", followedBy: map[taskKey]map[string]bool{}, owners: map[taskKey]string{}, observations: map[taskKey]domain.TaskObservation{}, pendingOwners: map[string]taskKey{}, ownerWaiters: map[string]chan string{}, requestWaiters: map[string]chan error{}, responseWaiters: map[string]chan ipcCallResponse{}, candidateKeys: map[taskKey]bool{}, states: map[taskKey]map[string]any{}, snapshotWaiters: map[snapshotKey][]snapshotWaiter{}}
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
	if err := client.send(map[string]any{"type": "request", "requestId": client.nextID("initialize"), "method": "initialize", "params": map[string]any{"clientType": client.clientType}}); err != nil {
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

// StartPreparedTurn submits the first instruction through Desktop so its normal
// userMessage event is both displayed live and retained in turn history.
func (client *ActivityClient) StartPreparedTurn(ctx context.Context, threadID, hostID, cwd, prompt string) error {
	// Keep task control independent of dashboard subscriptions, whose following
	// broadcasts can trigger additional discovery while a task is opening.
	control := NewWithClientType(client.endpoint, client.clientType)
	control.controlOnly = true
	defer control.Close()
	return control.startPreparedTurn(ctx, threadID, hostID, cwd, prompt)
}

func (client *ActivityClient) startPreparedTurn(ctx context.Context, threadID, hostID, cwd, prompt string) error {
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
	// Opening a URL only starts navigation. Retry read-only discovery until
	// Desktop claims the thread, never the effectful start-turn request.
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var owner string
	for owner == "" {
		attemptCtx, stopAttempt := context.WithTimeout(readyCtx, time.Second)
		var err error
		owner, err = client.discoverOwnerOnHost(attemptCtx, threadID, hostID, clientID)
		stopAttempt()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if owner == "" {
			select {
			case <-readyCtx.Done():
				return errors.New("Codex Desktop did not claim the new task")
			case <-time.After(250 * time.Millisecond):
			}
		}
	}

	requestID := client.nextID("start-turn")
	waiter := make(chan error, 1)
	client.mu.Lock()
	client.requestWaiters[requestID] = waiter
	client.mu.Unlock()
	defer func() { client.mu.Lock(); delete(client.requestWaiters, requestID); client.mu.Unlock() }()
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

// CachedConversationState returns the most recent snapshot received through
// the persistent Desktop IPC subscription. ReadConversationState establishes
// that subscription and performs the initial full-history load; callers can
// then reconcile cheaply from pushed snapshots instead of reloading history.
func (client *ActivityClient) CachedConversationState(threadID string) (map[string]any, bool) {
	client.mu.Lock()
	state := client.states[taskKey{hostID: "local", threadID: threadID}]
	client.mu.Unlock()
	return state, state != nil
}

func (client *ActivityClient) Close() {
	client.mu.Lock()
	connection := client.connection
	client.connection = nil
	client.started = false
	client.states = map[taskKey]map[string]any{}
	client.owners = map[taskKey]string{}
	client.connectionGeneration++
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
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil {
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
		responseErr := desktopIPCResponseError(envelope)
		client.mu.Lock()
		if waiter := client.responseWaiters[requestID]; waiter != nil {
			delete(client.responseWaiters, requestID)
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
	if typeName != "broadcast" || client.controlOnly {
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
		shouldDiscover := false
		client.mu.Lock()
		if client.followedBy[key] == nil {
			client.followedBy[key] = map[string]bool{}
		}
		if following {
			alreadyFollowing := client.followedBy[key][source]
			client.followedBy[key][source] = true
			if !alreadyFollowing {
				pending := false
				for _, candidate := range client.pendingOwners {
					if candidate == key {
						pending = true
						break
					}
				}
				shouldDiscover = !pending
			}
		} else {
			delete(client.followedBy[key], source)
		}
		client.mu.Unlock()
		if shouldDiscover {
			// A following notification identifies an interested peer, not the
			// task owner. Discover the owner before subscribing. Following the
			// peer directly makes two observer clients echo subscriptions forever.
			client.discoverOwner(key)
		}
	case "thread-stream-state-changed":
		change, _ := params["change"].(map[string]any)
		changeType, _ := change["type"].(string)
		if changeType == "snapshot" {
			state, _ := change["conversationState"].(map[string]any)
			revision := desktopScalarString(change["revision"])
			if revision == "" {
				revision = desktopScalarString(params["revision"])
			}
			if revision == "" {
				revision = desktopSnapshotRevision(state)
			}
			client.mu.Lock()
			if owner := client.owners[key]; owner == "" {
				client.states[key] = state
			} else if owner == source && client.cachePushedObservation(key, state, source, revision) {
				client.states[key] = state
			}
			matched := []snapshotWaiter{}
			waiterKeys := []snapshotKey{{task: key}}
			if source != "" {
				waiterKeys = append(waiterKeys, snapshotKey{task: key, owner: source})
			}
			for _, waiterKey := range waiterKeys {
				waiters := client.snapshotWaiters[waiterKey]
				remaining := waiters[:0]
				for _, waiter := range waiters {
					if waiter.accept == nil || waiter.accept(state, revision) {
						matched = append(matched, waiter)
					} else {
						remaining = append(remaining, waiter)
					}
				}
				if len(remaining) == 0 {
					delete(client.snapshotWaiters, waiterKey)
				} else {
					client.snapshotWaiters[waiterKey] = remaining
				}
			}
			client.mu.Unlock()
			for _, waiter := range matched {
				waiter.response <- snapshotResponse{state: state, source: source, revision: revision}
			}
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

func desktopSnapshotRevision(state map[string]any) string {
	payload, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return "snapshot:" + hex.EncodeToString(digest[:10])
}

func (client *ActivityClient) requestFollower(ctx context.Context, threadID, method string, version int, params map[string]any) (map[string]any, error) {
	if err := client.Start(ctx); err != nil {
		return nil, err
	}
	owner, err := client.owner(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return client.requestFollowerToOwner(ctx, threadID, owner, method, version, params)
}

func (client *ActivityClient) requestFollowerToOwner(ctx context.Context, threadID, owner, method string, version int, params map[string]any) (map[string]any, error) {
	client.mu.Lock()
	source := client.clientID
	client.mu.Unlock()
	if source == "" || owner == "" {
		return nil, errors.New("Codex Desktop IPC has no request owner")
	}
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

func desktopIPCResponseError(envelope map[string]any) error {
	if envelope["resultType"] != "error" && envelope["error"] == nil {
		return nil
	}
	message := ""
	switch value := envelope["error"].(type) {
	case map[string]any:
		message = firstString(value, "message", "code")
	case string:
		message = value
	default:
		if value != nil {
			message = fmt.Sprint(value)
		}
	}
	if strings.TrimSpace(message) == "" {
		message = "Codex Desktop rejected the request"
	}
	return errors.New(message)
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

// refreshOwner deliberately bypasses the long-lived owner cache.  Control
// requests that complete a Desktop-owned user-input prompt must be addressed
// to the current task owner: an old owner can acknowledge an IPC request after
// a Desktop reconnect without consuming the answer.  The former Node bridge
// used a fresh IPC session for this path, which had the same effect.
func (client *ActivityClient) refreshOwner(ctx context.Context, threadID string) (string, error) {
	if err := client.Start(ctx); err != nil {
		return "", err
	}
	client.mu.Lock()
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
		for attempt := 0; attempt < 40 && owner == ""; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
			}
			owner, err = client.discoverOwnerSync(ctx, threadID, source)
			if err != nil {
				return "", err
			}
		}
	}
	if owner == "" {
		return "", errors.New("Codex Desktop does not own the linked task")
	}
	client.mu.Lock()
	client.owners[taskKey{hostID: "local", threadID: threadID}] = owner
	client.mu.Unlock()
	return owner, nil
}

func (client *ActivityClient) discoverOwnerSync(ctx context.Context, threadID, source string) (string, error) {
	return client.discoverOwnerOnHost(ctx, threadID, "local", source)
}

func (client *ActivityClient) discoverOwnerOnHost(ctx context.Context, threadID, hostID, source string) (string, error) {
	requestID := client.nextID("owner")
	waiter := make(chan string, 1)
	client.mu.Lock()
	client.ownerWaiters[requestID] = waiter
	client.mu.Unlock()
	defer func() { client.mu.Lock(); delete(client.ownerWaiters, requestID); client.mu.Unlock() }()
	if err := client.send(map[string]any{"type": "request", "requestId": requestID, "sourceClientId": source, "method": "thread-owner-discovery", "version": 1, "params": map[string]any{"conversationId": threadID, "hostId": hostID}}); err != nil {
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
	owner, err := client.owner(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return client.loadConversationStateFromOwner(ctx, threadID, owner, true)
}

// ReadConversationStateForUserInput binds the authoritative snapshot to the
// owner that produced it. The returned owner must be reused for submission;
// consulting the shared owner cache again introduces a race when more than one
// Desktop/App Server client is present.
func (client *ActivityClient) ReadConversationStateForUserInput(ctx context.Context, threadID string) (UserInputTarget, error) {
	owner, err := client.refreshOwner(ctx, threadID)
	if err != nil {
		return UserInputTarget{}, err
	}
	state, snapshotSource, revision, err := client.loadConversationStateFromOwnerMatchingRevision(ctx, threadID, owner, owner, nil)
	if err != nil {
		return UserInputTarget{}, err
	}
	return UserInputTarget{ThreadID: threadID, OwnerClientID: owner, SnapshotSourceClientID: snapshotSource, SnapshotRevision: revision, State: state}, nil
}

func (client *ActivityClient) loadConversationStateFromOwner(ctx context.Context, threadID, owner string, requireOwnerSource bool) (map[string]any, error) {
	snapshotSource := ""
	if requireOwnerSource {
		snapshotSource = owner
	}
	state, _, err := client.loadConversationStateFromOwnerMatching(ctx, threadID, owner, snapshotSource, nil)
	return state, err
}

func (client *ActivityClient) loadConversationStateFromOwnerMatching(ctx context.Context, threadID, owner, snapshotSource string, accept func(map[string]any) bool) (map[string]any, string, error) {
	wrapped := func(state map[string]any, _ string) bool {
		return accept == nil || accept(state)
	}
	state, source, _, err := client.loadConversationStateFromOwnerMatchingRevision(ctx, threadID, owner, snapshotSource, wrapped)
	return state, source, err
}

func (client *ActivityClient) loadConversationStateFromOwnerMatchingRevision(ctx context.Context, threadID, owner, snapshotSource string, accept func(map[string]any, string) bool) (map[string]any, string, string, error) {
	if err := client.Start(ctx); err != nil {
		return nil, "", "", err
	}
	key := taskKey{hostID: "local", threadID: threadID}
	waiterKey := snapshotKey{task: key, owner: snapshotSource}
	response := make(chan snapshotResponse, 1)
	waiter := snapshotWaiter{response: response, accept: accept}
	client.mu.Lock()
	source := client.clientID
	client.snapshotWaiters[waiterKey] = append(client.snapshotWaiters[waiterKey], waiter)
	client.mu.Unlock()
	_ = client.send(map[string]any{"type": "broadcast", "sourceClientId": source, "method": "thread-stream-following-changed", "version": 1, "params": map[string]any{"conversationId": threadID, "hostId": "local", "following": true}})
	if _, err := client.requestFollowerToOwner(ctx, threadID, owner, "thread-follower-load-complete-history", 1, map[string]any{"conversationId": threadID}); err != nil {
		client.removeSnapshotWaiter(waiterKey, response)
		return nil, "", "", err
	}
	select {
	case snapshot := <-response:
		return snapshot.state, snapshot.source, snapshot.revision, snapshot.err
	case <-ctx.Done():
		client.removeSnapshotWaiter(waiterKey, response)
		return nil, "", "", ctx.Err()
	case <-time.After(5 * time.Second):
		client.removeSnapshotWaiter(waiterKey, response)
		return nil, "", "", errors.New("Codex Desktop task snapshot timed out")
	}
}

func (client *ActivityClient) removeSnapshotWaiter(key snapshotKey, target chan snapshotResponse) {
	client.mu.Lock()
	defer client.mu.Unlock()
	waiters := client.snapshotWaiters[key]
	kept := waiters[:0]
	for _, waiter := range waiters {
		if waiter.response != target {
			kept = append(kept, waiter)
		}
	}
	if len(kept) == 0 {
		delete(client.snapshotWaiters, key)
	} else {
		client.snapshotWaiters[key] = kept
	}
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
func (client *ActivityClient) SubmitBridgeUserInput(ctx context.Context, target UserInputTarget, response map[string]any) error {
	requestID, err := target.RequestID.Value()
	if target.ThreadID == "" || target.OwnerClientID == "" || err != nil {
		return errors.New("invalid Codex Desktop user-input target")
	}
	_, err = client.requestFollowerToOwner(ctx, target.ThreadID, target.OwnerClientID, "thread-follower-submit-user-input", 1, map[string]any{"conversationId": target.ThreadID, "requestId": requestID, "response": response})
	return err
}

func (client *ActivityClient) ReadConversationStateFromUserInputOwner(ctx context.Context, target UserInputTarget) (map[string]any, error) {
	return client.loadConversationStateFromOwner(ctx, target.ThreadID, target.OwnerClientID, true)
}

func (client *ActivityClient) OpenUserInputSession(ctx context.Context) (*UserInputSession, error) {
	// Match the frozen Node bridge identity. Desktop uses this client type when
	// deciding which temporary followers receive task snapshots.
	isolated := NewWithClientType(client.endpoint, "feishu-bridge")
	if err := isolated.Start(ctx); err != nil {
		return nil, err
	}
	return &UserInputSession{client: isolated}, nil
}

func (session *UserInputSession) Close() { session.client.Close() }

func (session *UserInputSession) Read(ctx context.Context, threadID string, requestID RequestRef) (UserInputTarget, error) {
	if _, err := requestID.Value(); err != nil {
		return UserInputTarget{}, errors.New("invalid Codex Desktop user-input request id")
	}
	owner, err := session.client.refreshOwner(ctx, threadID)
	if err != nil {
		return UserInputTarget{}, err
	}
	state, snapshotSource, revision, err := session.client.loadConversationStateFromOwnerMatchingRevision(ctx, threadID, owner, "", func(state map[string]any, _ string) bool {
		return containsDesktopUserInputRequestID(state, requestID)
	})
	if err != nil {
		return UserInputTarget{}, err
	}
	definition, found := FindPendingUserInput(state, requestID)
	if !found {
		return UserInputTarget{}, errors.New("Codex Desktop user-input request is stale")
	}
	return UserInputTarget{ThreadID: threadID, TurnID: definition.TurnID, OwnerClientID: owner, SnapshotSourceClientID: snapshotSource, SnapshotRevision: revision, RequestID: requestID, State: state}, nil
}

func (session *UserInputSession) Submit(ctx context.Context, target UserInputTarget, response map[string]any) error {
	return session.client.SubmitBridgeUserInput(ctx, target, response)
}

func (session *UserInputSession) Verify(ctx context.Context, target UserInputTarget) (UserInputVerification, error) {
	state, _, revision, err := session.client.loadConversationStateFromOwnerMatchingRevision(ctx, target.ThreadID, target.OwnerClientID, "", func(state map[string]any, revision string) bool {
		consumed := !containsDesktopUserInputRequestID(state, target.RequestID)
		// A turn commonly remains "running" while requestUserInput is pending.
		// The follower response therefore is not proven effective until the exact
		// typed request ID disappears from a strictly newer authoritative snapshot.
		if !consumed {
			return false
		}
		return revision != "" && revision != target.SnapshotRevision
	})
	if err != nil {
		return UserInputVerification{}, err
	}
	return UserInputVerification{State: state, SnapshotRevision: revision, RequestConsumed: !containsDesktopUserInputRequestID(state, target.RequestID), TurnRunning: desktopTurnRunning(state, target.TurnID)}, nil
}

func FindPendingUserInput(value any, requestID RequestRef) (PendingUserInputDefinition, bool) {
	matches := []PendingUserInputDefinition{}
	collectPendingUserInputDefinitions(value, requestID, &matches)
	if len(matches) != 1 {
		return PendingUserInputDefinition{}, false
	}
	return matches[0], true
}

func collectPendingUserInputDefinitions(value any, requestID RequestRef, matches *[]PendingUserInputDefinition) {
	switch item := value.(type) {
	case map[string]any:
		if item["method"] == "item/tool/requestUserInput" {
			if candidate, err := RequestRefFromValue(item["id"]); err == nil && candidate.Equal(requestID) {
				params, _ := item["params"].(map[string]any)
				definition := PendingUserInputDefinition{TurnID: desktopScalarString(params["turnId"])}
				if values, ok := params["questions"].([]any); ok {
					for _, value := range values {
						question, ok := value.(map[string]any)
						if !ok {
							continue
						}
						if secret, _ := question["isSecret"].(bool); !secret {
							definition.Questions = append(definition.Questions, question)
						}
					}
				}
				*matches = append(*matches, definition)
			}
		}
		for _, child := range item {
			collectPendingUserInputDefinitions(child, requestID, matches)
		}
	case []any:
		for _, child := range item {
			collectPendingUserInputDefinitions(child, requestID, matches)
		}
	}
}

func desktopStateContainsTurn(value any, turnID string) bool {
	if turnID == "" {
		return true
	}
	switch item := value.(type) {
	case map[string]any:
		if desktopScalarString(item["id"]) == turnID || desktopScalarString(item["turnId"]) == turnID {
			return true
		}
		for _, child := range item {
			if desktopStateContainsTurn(child, turnID) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if desktopStateContainsTurn(child, turnID) {
				return true
			}
		}
	}
	return false
}

func desktopTurnRunning(value any, turnID string) bool {
	if turnID == "" {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		if desktopScalarString(item["id"]) == turnID || desktopScalarString(item["turnId"]) == turnID {
			status := item["status"]
			if object, ok := status.(map[string]any); ok {
				status = object["type"]
			}
			text := strings.ToLower(desktopScalarString(status))
			if text == "running" || text == "in_progress" || text == "active" {
				return true
			}
		}
		for _, child := range item {
			if desktopTurnRunning(child, turnID) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if desktopTurnRunning(child, turnID) {
				return true
			}
		}
	}
	return false
}

func containsDesktopUserInputRequestID(value any, requestID RequestRef) bool {
	switch item := value.(type) {
	case map[string]any:
		if item["method"] == "item/tool/requestUserInput" {
			if candidate, err := RequestRefFromValue(item["id"]); err == nil && candidate.Equal(requestID) {
				return true
			}
		}
		for _, child := range item {
			if containsDesktopUserInputRequestID(child, requestID) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if containsDesktopUserInputRequestID(child, requestID) {
				return true
			}
		}
	}
	return false
}

func desktopScalarString(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case json.Number:
		return item.String()
	default:
		return ""
	}
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
	return fmt.Sprintf("ksf-assistant-core-%s-%d", prefix, client.sequence)
}

func (client *ActivityClient) connectionEnded(connection net.Conn) {
	client.mu.Lock()
	if client.connection == connection {
		client.connection = nil
		client.clientID = ""
		client.availability = "offline"
		client.observations = map[taskKey]domain.TaskObservation{}
		client.owners = map[taskKey]string{}
		client.states = map[taskKey]map[string]any{}
		client.connectionGeneration++
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
		for key, waiters := range client.snapshotWaiters {
			delete(client.snapshotWaiters, key)
			for _, waiter := range waiters {
				waiter.response <- snapshotResponse{err: errors.New("Codex Desktop IPC disconnected")}
			}
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
