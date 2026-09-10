package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestParseObservationPreservesDescriptionsWithoutRawState(t *testing.T) {
	state := map[string]any{
		"threadRuntimeStatus": map[string]any{"type": "active", "activeFlags": []any{"waitingOnUserInput"}},
		"requests":            []any{map[string]any{"method": "item/tool/requestUserInput"}},
		"source":              map[string]any{"vscode": map[string]any{}},
	}
	value, ok := parseObservation(taskKey{hostID: "local", threadID: "thread"}, state)
	if !ok || value.RuntimeStatus != "active" || len(value.ActiveFlags) != 1 || len(value.PendingRequestMethods) != 1 {
		t.Fatalf("unexpected observation: %#v", value)
	}
}

func TestIsolatedUserInputSessionUsesFrozenFeishuBridgeClientType(t *testing.T) {
	client := NewWithClientType("test", "feishu-bridge")
	if client.clientType != "feishu-bridge" {
		t.Fatalf("unexpected isolated client type: %q", client.clientType)
	}
}

func TestFollowerControlRequestsUseFrozenMethodsAndReturnTurnID(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "bridge-client"
	client.owners[taskKey{hostID: "local", threadID: "thread-123"}] = "desktop-owner"

	result := make(chan struct {
		id  string
		err error
	}, 1)
	go func() {
		id, err := client.StartBridgeTurn(context.Background(), "thread-123", "/tmp/project", "继续", nil)
		result <- struct {
			id  string
			err error
		}{id: id, err: err}
	}()

	request := readTestFrame(t, serverSide)
	if request["method"] != "thread-follower-start-turn" || request["version"].(json.Number).String() != "2" || request["targetClientId"] != "desktop-owner" {
		t.Fatalf("unexpected follower request: %#v", request)
	}
	params := request["params"].(map[string]any)
	turnStart := params["turnStart"].(map[string]any)
	turnRequest := turnStart["request"].(map[string]any)
	contextValue := turnStart["context"].(map[string]any)
	if turnRequest["cwd"] != "/tmp/project" || contextValue["inheritThreadSettings"] != true {
		t.Fatalf("thread settings were not inherited: %#v", turnStart)
	}
	for _, field := range []string{"approvalPolicy", "sandbox", "sandboxPolicy"} {
		if _, found := turnRequest[field]; found {
			t.Fatalf("Desktop follower overrode %s: %#v", field, turnRequest)
		}
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": request["requestId"], "result": map[string]any{"turn": map[string]any{"id": "turn-456"}}}))
	select {
	case value := <-result:
		if value.err != nil || value.id != "turn-456" {
			t.Fatalf("unexpected result: %#v", value)
		}
	case <-time.After(time.Second):
		t.Fatal("follower request did not complete")
	}
}

func TestReadConversationStateUsesHistoryRequestAndSnapshot(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "bridge-client"
	client.owners[taskKey{hostID: "local", threadID: "thread-123"}] = "desktop-owner"

	result := make(chan error, 1)
	go func() {
		state, err := client.ReadConversationState(context.Background(), "thread-123")
		if err == nil && state["marker"] != "snapshot" {
			err = errors.New("snapshot marker missing")
		}
		result <- err
	}()
	reader := bufio.NewReader(serverSide)
	_ = readTestFrameWithReader(t, reader) // following broadcast
	request := readTestFrameWithReader(t, reader)
	if request["method"] != "thread-follower-load-complete-history" {
		t.Fatalf("unexpected history request: %#v", request)
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": request["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "desktop-owner", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "conversationState": map[string]any{"marker": "snapshot"}}}}))
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
		state, ok := client.CachedConversationState("thread-123")
		if !ok || state["marker"] != "snapshot" {
			t.Fatalf("loaded state was not retained for pushed reconciliation: %#v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("state read did not complete")
	}
}

func TestUserInputStateRefreshesOwnerBeforeLoadingHistory(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "bridge-client"
	client.owners[taskKey{hostID: "local", threadID: "thread-123"}] = "stale-owner"

	result := make(chan error, 1)
	go func() {
		target, err := client.ReadConversationStateForUserInput(context.Background(), "thread-123")
		if err == nil && (target.State["marker"] != "fresh-snapshot" || target.OwnerClientID != "fresh-owner") {
			err = errors.New("fresh snapshot marker missing")
		}
		result <- err
	}()
	reader := bufio.NewReader(serverSide)
	discovery := readTestFrameWithReader(t, reader)
	if discovery["method"] != "thread-owner-discovery" {
		t.Fatalf("user-input preflight reused cached owner: %#v", discovery)
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": discovery["requestId"], "result": map[string]any{"handledByClientId": "fresh-owner"}}))
	_ = readTestFrameWithReader(t, reader) // following broadcast
	history := readTestFrameWithReader(t, reader)
	if history["method"] != "thread-follower-load-complete-history" || history["targetClientId"] != "fresh-owner" {
		t.Fatalf("history did not target rediscovered owner: %#v", history)
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "fresh-owner", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "conversationState": map[string]any{"marker": "fresh-snapshot"}}}}))
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("user-input state read did not complete")
	}
}

func TestUserInputSnapshotIgnoresAnotherOwner(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "bridge-client"

	result := make(chan UserInputTarget, 1)
	errorsResult := make(chan error, 1)
	go func() {
		target, err := client.ReadConversationStateForUserInput(context.Background(), "thread-123")
		result <- target
		errorsResult <- err
	}()
	reader := bufio.NewReader(serverSide)
	discovery := readTestFrameWithReader(t, reader)
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": discovery["requestId"], "result": map[string]any{"handledByClientId": "owner-a"}}))
	_ = readTestFrameWithReader(t, reader)
	history := readTestFrameWithReader(t, reader)
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "owner-b", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "conversationState": map[string]any{"marker": "wrong"}}}}))
	client.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "owner-a", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "conversationState": map[string]any{"marker": "right"}}}}))
	if err := <-errorsResult; err != nil {
		t.Fatal(err)
	}
	if target := <-result; target.OwnerClientID != "owner-a" || target.State["marker"] != "right" {
		t.Fatalf("snapshot was not bound to owner: %#v", target)
	}
}

func TestIsolatedUserInputSnapshotAcceptsOwnerProxySource(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "isolated-client"

	result := make(chan map[string]any, 1)
	errorsResult := make(chan error, 1)
	go func() {
		state, err := client.loadConversationStateFromOwner(context.Background(), "thread-123", "owner-a", false)
		result <- state
		errorsResult <- err
	}()
	reader := bufio.NewReader(serverSide)
	_ = readTestFrameWithReader(t, reader)
	history := readTestFrameWithReader(t, reader)
	if history["targetClientId"] != "owner-a" {
		t.Fatalf("history was not sent to discovered owner: %#v", history)
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "owner-proxy", "method": "thread-stream-state-changed", "params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "conversationState": map[string]any{"marker": "proxied"}}}}))
	if err := <-errorsResult; err != nil {
		t.Fatal(err)
	}
	if state := <-result; state["marker"] != "proxied" {
		t.Fatalf("proxy snapshot was not accepted: %#v", state)
	}
}

func TestUserInputSessionReadWaitsForExactRequestAndCapturesProxySource(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "isolated-client"
	session := &UserInputSession{client: client}

	targets := make(chan UserInputTarget, 1)
	errorsResult := make(chan error, 1)
	go func() {
		target, err := session.Read(context.Background(), "thread-123", mustRequestRef(t, `"request-exact"`))
		targets <- target
		errorsResult <- err
	}()
	reader := bufio.NewReader(serverSide)
	discovery := readTestFrameWithReader(t, reader)
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": discovery["requestId"], "result": map[string]any{"handledByClientId": "owner-a"}}))
	_ = readTestFrameWithReader(t, reader) // following broadcast
	history := readTestFrameWithReader(t, reader)
	if history["targetClientId"] != "owner-a" {
		t.Fatalf("history targeted wrong owner: %#v", history)
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{
		"type": "broadcast", "sourceClientId": "unrelated-proxy", "method": "thread-stream-state-changed",
		"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "conversationState": map[string]any{
			"requests": []any{map[string]any{"id": "request-other", "method": "item/tool/requestUserInput"}},
		}}},
	}))
	select {
	case target := <-targets:
		t.Fatalf("unrelated request satisfied preflight: %#v", target)
	case <-time.After(50 * time.Millisecond):
	}
	client.handle(mustJSON(t, map[string]any{
		"type": "broadcast", "sourceClientId": "owner-proxy", "method": "thread-stream-state-changed",
		"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "conversationState": map[string]any{
			"requests": []any{map[string]any{"id": "request-exact", "method": "item/tool/requestUserInput"}},
		}}},
	}))
	if err := <-errorsResult; err != nil {
		t.Fatal(err)
	}
	target := <-targets
	if target.OwnerClientID != "owner-a" || target.SnapshotSourceClientID != "owner-proxy" || target.RequestID.CompatibilityString() != "request-exact" {
		t.Fatalf("user-input target lost its bound identity: %#v", target)
	}
}

func TestUserInputSessionVerifyAcceptsNewerOwnerProxySnapshot(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "isolated-client"
	session := &UserInputSession{client: client}
	target := UserInputTarget{ThreadID: "thread-123", OwnerClientID: "owner-a", SnapshotSourceClientID: "owner-proxy", SnapshotRevision: "10", RequestID: mustRequestRef(t, `"request-exact"`)}

	states := make(chan UserInputVerification, 1)
	errorsResult := make(chan error, 1)
	go func() {
		state, err := session.Verify(context.Background(), target)
		states <- state
		errorsResult <- err
	}()
	reader := bufio.NewReader(serverSide)
	_ = readTestFrameWithReader(t, reader) // following broadcast
	history := readTestFrameWithReader(t, reader)
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{
		"type": "broadcast", "sourceClientId": "other-proxy", "method": "thread-stream-state-changed",
		"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": 10, "conversationState": map[string]any{"marker": "stale"}}},
	}))
	select {
	case state := <-states:
		t.Fatalf("verification accepted stale revision: %#v", state)
	case <-time.After(50 * time.Millisecond):
	}
	client.handle(mustJSON(t, map[string]any{
		"type": "broadcast", "sourceClientId": "new-owner-proxy", "method": "thread-stream-state-changed",
		"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": 11, "conversationState": map[string]any{"marker": "right"}}},
	}))
	if err := <-errorsResult; err != nil {
		t.Fatal(err)
	}
	if state := <-states; state.State["marker"] != "right" {
		t.Fatalf("verification returned wrong snapshot: %#v", state)
	}
}

func TestUserInputSessionVerifyRejectsRunningTurnWhileRequestRemains(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "isolated-client"
	session := &UserInputSession{client: client}
	target := UserInputTarget{
		ThreadID:         "thread-123",
		TurnID:           "turn-456",
		OwnerClientID:    "owner-a",
		SnapshotRevision: "10",
		RequestID:        mustRequestRef(t, `"request-exact"`),
	}

	states := make(chan UserInputVerification, 1)
	errorsResult := make(chan error, 1)
	go func() {
		state, err := session.Verify(context.Background(), target)
		states <- state
		errorsResult <- err
	}()
	reader := bufio.NewReader(serverSide)
	_ = readTestFrameWithReader(t, reader) // following broadcast
	history := readTestFrameWithReader(t, reader)
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{
		"type": "broadcast", "sourceClientId": "owner-proxy", "method": "thread-stream-state-changed",
		"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": 11, "conversationState": map[string]any{
			"turns":    []any{map[string]any{"id": "turn-456", "status": "running"}},
			"requests": []any{map[string]any{"id": "request-exact", "method": "item/tool/requestUserInput"}},
		}}},
	}))
	select {
	case state := <-states:
		t.Fatalf("running turn falsely proved request consumption: %#v", state)
	case <-time.After(50 * time.Millisecond):
	}
	client.handle(mustJSON(t, map[string]any{
		"type": "broadcast", "sourceClientId": "owner-proxy", "method": "thread-stream-state-changed",
		"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": 12, "conversationState": map[string]any{
			"turns": []any{map[string]any{"id": "turn-456", "status": "running"}},
		}}},
	}))
	if err := <-errorsResult; err != nil {
		t.Fatal(err)
	}
	if state := <-states; !state.RequestConsumed || state.SnapshotRevision != "12" {
		t.Fatalf("consumed request was not verified: %#v", state)
	}
}

func TestSubmitUserInputUsesResolvedOwnerAndRejectsResultTypeError(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "bridge-client"
	client.owners[taskKey{hostID: "local", threadID: "thread-123"}] = "owner-b"

	done := make(chan error, 1)
	go func() {
		done <- client.SubmitBridgeUserInput(context.Background(), UserInputTarget{ThreadID: "thread-123", OwnerClientID: "owner-a", RequestID: mustRequestRef(t, `"request-1"`)}, map[string]any{"answers": map[string]any{}})
	}()
	request := readTestFrame(t, serverSide)
	if request["targetClientId"] != "owner-a" {
		t.Fatalf("submission drifted to cached owner: %#v", request)
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": request["requestId"], "resultType": "error", "error": "wrong owner"}))
	if err := <-done; err == nil || err.Error() != "wrong owner" {
		t.Fatalf("IPC error was treated as success: %v", err)
	}
}

func TestSubmitUserInputPreservesLargeIntegerRequestID(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "bridge-client"

	done := make(chan error, 1)
	go func() {
		done <- client.SubmitBridgeUserInput(context.Background(), UserInputTarget{
			ThreadID:      "thread-123",
			OwnerClientID: "owner-a",
			RequestID:     mustRequestRef(t, `9007199254740993`),
		}, map[string]any{"answers": map[string]any{}})
	}()
	request := readTestFrame(t, serverSide)
	params := request["params"].(map[string]any)
	requestID, ok := params["requestId"].(json.Number)
	if !ok || requestID.String() != "9007199254740993" {
		t.Fatalf("large integer request id lost JSON type or precision: %#v", params["requestId"])
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": request["requestId"], "result": map[string]any{"ok": true}}))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUserInputSessionCarriesLargeIntegerFromSnapshotToSubmission(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "isolated-client"
	session := &UserInputSession{client: client}
	requestRef := mustRequestRef(t, `900719925474099312345`)

	targets := make(chan UserInputTarget, 1)
	errorsResult := make(chan error, 1)
	go func() {
		target, err := session.Read(context.Background(), "thread-123", requestRef)
		targets <- target
		errorsResult <- err
	}()
	reader := bufio.NewReader(serverSide)
	discovery := readTestFrameWithReader(t, reader)
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": discovery["requestId"], "result": map[string]any{"handledByClientId": "owner-a"}}))
	_ = readTestFrameWithReader(t, reader) // following broadcast
	history := readTestFrameWithReader(t, reader)
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": history["requestId"], "result": map[string]any{}}))
	client.handle(mustJSON(t, map[string]any{
		"type": "broadcast", "sourceClientId": "owner-proxy", "method": "thread-stream-state-changed",
		"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "change": map[string]any{"type": "snapshot", "revision": 10, "conversationState": map[string]any{
			"requests": []any{map[string]any{
				"id": json.Number("900719925474099312345"), "method": "item/tool/requestUserInput",
				"params": map[string]any{"turnId": "turn-456", "questions": []any{map[string]any{"id": "choice", "question": "请选择"}}},
			}},
		}}},
	}))
	if err := <-errorsResult; err != nil {
		t.Fatal(err)
	}
	target := <-targets
	if target.RequestID.Kind != "integer" || string(target.RequestID.Raw) != "900719925474099312345" {
		t.Fatalf("snapshot request identity changed: %#v", target.RequestID)
	}

	submitted := make(chan error, 1)
	go func() {
		submitted <- session.Submit(context.Background(), target, map[string]any{"answers": map[string]any{"choice": "one"}})
	}()
	request := readTestFrameWithReader(t, reader)
	params := request["params"].(map[string]any)
	requestID, ok := params["requestId"].(json.Number)
	if !ok || requestID.String() != "900719925474099312345" || request["targetClientId"] != "owner-a" {
		t.Fatalf("snapshot identity was not submitted unchanged: %#v", request)
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": request["requestId"], "result": map[string]any{"ok": true}}))
	if err := <-submitted; err != nil {
		t.Fatal(err)
	}
}

func mustRequestRef(t *testing.T, raw string) RequestRef {
	t.Helper()
	value, err := ParseRequestRef(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestFollowingNotificationDiscoversOwnerWithoutEchoingFollower(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "observer-client"

	done := make(chan struct{})
	go func() {
		client.handle(mustJSON(t, map[string]any{
			"type": "broadcast", "sourceClientId": "another-observer", "method": "thread-stream-following-changed", "version": 1,
			"params": map[string]any{"conversationId": "thread-123", "hostId": "local", "following": true},
		}))
		close(done)
	}()
	request := readTestFrame(t, serverSide)
	if request["method"] != "thread-owner-discovery" {
		t.Fatalf("following notification was echoed instead of discovering the owner: %#v", request)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("following notification did not finish")
	}
}

func readTestFrame(t *testing.T, connection net.Conn) map[string]any {
	t.Helper()
	return readTestFrameWithReader(t, bufio.NewReader(connection))
}

func readTestFrameWithReader(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	_ = reader
	var size uint32
	if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestPreparedTurnWaitsForOwnerAndSendsVisibleFirstMessageOnce(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(fmt.Sprint(rejected), func(t *testing.T) {
			clientSide, serverSide := net.Pipe()
			defer clientSide.Close()
			defer serverSide.Close()
			client := New("test")
			client.connection = clientSide
			client.started = true
			client.clientID = "assistant"
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- client.startPreparedTurn(ctx, "draft", "local", "/project", "第一句话\n保留原文")
			}()
			discovery := readTestFrame(t, serverSide)
			client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": discovery["requestId"], "result": map[string]any{}}))
			discovery = readTestFrame(t, serverSide)
			if discovery["method"] != "thread-owner-discovery" {
				t.Fatal("dispatched before Desktop was ready")
			}
			client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": discovery["requestId"], "result": map[string]any{"handledByClientId": "desktop"}}))
			request := readTestFrame(t, serverSide)
			if request["method"] != "thread-follower-start-turn" || request["targetClientId"] != "desktop" {
				t.Fatalf("incorrect dispatch: %#v", request)
			}
			turnStart := request["params"].(map[string]any)["turnStart"].(map[string]any)
			input := turnStart["request"].(map[string]any)
			items := input["input"].([]any)
			if len(input) != 3 || len(items) != 1 || items[0].(map[string]any)["type"] != "text" || items[0].(map[string]any)["text"] != "第一句话\n保留原文" {
				t.Fatalf("first message missing, changed, repeated, or settings overridden: %#v", input)
			}
			if turnStart["context"].(map[string]any)["inheritThreadSettings"] != true {
				t.Fatal("did not inherit Desktop settings")
			}
			reply := map[string]any{"type": "response", "requestId": request["requestId"], "result": map[string]any{}}
			if rejected {
				reply["resultType"] = "error"
				reply["error"] = "no-client-found"
			}
			client.handle(mustJSON(t, reply))
			if err := <-result; (err != nil) != rejected {
				t.Fatalf("result: %v", err)
			}
			if len(client.ownerWaiters) != 0 || len(client.requestWaiters) != 0 {
				t.Fatal("request waiters leaked")
			}
		})
	}
}

func TestPreparedTurnCancellationCleansOwnerWaiter(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "assistant"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- client.startPreparedTurn(ctx, "draft", "local", "/project", "第一句话\n保留原文")
	}()
	readTestFrame(t, serverSide)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if len(client.ownerWaiters) != 0 {
		t.Fatal("owner waiter leaked")
	}
}

func TestTaskControlIgnoresObservationBroadcasts(t *testing.T) {
	client := New("test")
	client.controlOnly = true
	client.handle(mustJSON(t, map[string]any{"type": "broadcast", "sourceClientId": "observer", "method": "thread-stream-following-changed", "params": map[string]any{"conversationId": "draft", "hostId": "local", "following": true}}))
	if len(client.pendingOwners) != 0 || len(client.followedBy) != 0 {
		t.Fatal("control channel started observation discovery")
	}
}

func TestPreparedTurnRetriesUnansweredDiscoveryBeforeDispatch(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	client := New("test")
	client.connection = clientSide
	client.started = true
	client.clientID = "assistant"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- client.startPreparedTurn(ctx, "draft", "local", "/project", "第一句话\n保留原文")
	}()
	first := readTestFrame(t, serverSide) // The first query gets no reply during navigation.
	second := readTestFrame(t, serverSide)
	if first["method"] != "thread-owner-discovery" || second["method"] != first["method"] || second["requestId"] == first["requestId"] {
		t.Fatal("did not retry only the read-only query")
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": second["requestId"], "result": map[string]any{"handledByClientId": "desktop"}}))
	start := readTestFrame(t, serverSide)
	if start["method"] != "thread-follower-start-turn" {
		t.Fatal("did not start after ownership resolved")
	}
	client.handle(mustJSON(t, map[string]any{"type": "response", "requestId": start["requestId"], "result": map[string]any{}}))
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if len(client.ownerWaiters) != 0 {
		t.Fatal("timed-out query left a waiter")
	}
}
