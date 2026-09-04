package desktop

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	if request["method"] != "thread-follower-start-turn" || int(request["version"].(float64)) != 2 || request["targetClientId"] != "desktop-owner" {
		t.Fatalf("unexpected follower request: %#v", request)
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
	case <-time.After(time.Second):
		t.Fatal("state read did not complete")
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
	if err := json.Unmarshal(payload, &value); err != nil {
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
