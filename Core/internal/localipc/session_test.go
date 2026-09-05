package localipc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ksfassistant/core/internal/privateipc"
)

func TestSessionReusesAuthenticatedConnectionAcrossCalls(t *testing.T) {
	root := t.TempDir()
	server, err := Listen(root, privateipc.HandlerFunc(func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "chunk" {
			return nil, privateipc.ErrMethodNotFound
		}
		var value int
		if err := json.Unmarshal(params, &value); err != nil {
			return nil, err
		}
		return value, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	openCtx, cancelOpen := context.WithTimeout(context.Background(), time.Second)
	defer cancelOpen()
	session, err := Open(openCtx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	cancelOpen()
	server.mu.Lock()
	var connection *privateipc.Peer
	for peer := range server.peers {
		connection = peer
	}
	server.mu.Unlock()
	if connection == nil {
		t.Fatal("authenticated connection missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var calls sync.WaitGroup
	for index := 0; index < 16; index++ {
		calls.Add(1)
		go func(value int) {
			defer calls.Done()
			var result int
			if err := session.Call(ctx, "chunk", value, &result); err != nil || result != value {
				t.Errorf("chunk %d: result=%d error=%v", value, result, err)
			}
		}(index)
	}
	calls.Wait()
	server.mu.Lock()
	_, retained := server.peers[connection]
	count := len(server.peers)
	server.mu.Unlock()
	if !retained || count != 1 {
		t.Fatal("session changed connection")
	}
	var remote *privateipc.RPCError
	if err := session.peer.Call(ctx, handshakeMethod, identity{Protocol: Protocol, DataRoot: server.root}, nil); !errors.As(err, &remote) || remote.Code != -32600 {
		t.Fatalf("connection allowed repeated authentication: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Call(ctx, "chunk", 17, nil); !errors.Is(err, privateipc.ErrPeerClosed) {
		t.Fatalf("closed session call: %v", err)
	}
}

func TestSessionRequestCancellationDoesNotCloseConnection(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	finished := make(chan struct{})
	server, err := Listen(root, privateipc.HandlerFunc(func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		if method == "wait" {
			close(started)
			<-ctx.Done()
			close(finished)
			return nil, ctx.Err()
		}
		return true, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	done := make(chan error, 1)
	go func() { done <- session.Call(requestCtx, "wait", nil, nil) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelRequest()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("remote request did not cancel")
	}
	var result bool
	if err := session.Call(ctx, "next", nil, &result); err != nil || !result {
		t.Fatalf("request cancellation retired session: %v", err)
	}
}

func TestSessionDoesNotReconnectAfterServerRestart(t *testing.T) {
	root := t.TempDir()
	server, err := Listen(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Listen(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	select {
	case <-session.peer.Done():
	case <-ctx.Done():
		t.Fatal("session did not observe EOF")
	}
	if err := session.Call(ctx, "chunk", nil, nil); !errors.Is(err, privateipc.ErrPeerClosed) {
		t.Fatalf("stale session reconnected: %v", err)
	}
	restarted.mu.Lock()
	defer restarted.mu.Unlock()
	if len(restarted.peers) != 0 {
		t.Fatal("old session opened a new connection")
	}
}

func TestSessionOfflineOpenDoesNotCreateFiles(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "missing-root")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if session, err := Open(ctx, root); !errors.Is(err, ErrNotRunning) || session != nil {
		t.Fatalf("offline Open: %v %v", session, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("offline Open wrote files: %v %v", entries, err)
	}
	cancel()
	if session, err := Open(ctx, root); !errors.Is(err, context.Canceled) || session != nil {
		t.Fatalf("cancelled Open: %v %v", session, err)
	}
}
