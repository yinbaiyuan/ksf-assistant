package localipc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/privateipc"
)

func TestOfflineCallDoesNotWriteOrCreateRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "missing", "data")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Call(ctx, root, "cli/execute", nil, nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("expected not running, got %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("offline call wrote files: %v %v", entries, err)
	}
}

func TestGatewayMultiClientTypedRPCAndUniqueListener(t *testing.T) {
	root := t.TempDir()
	handler := privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == "fail" {
			return nil, privateipc.NewError(-32042, "test failure")
		}
		if method != "cli/execute" {
			return nil, privateipc.ErrMethodNotFound
		}
		var request struct {
			Value int `json:"value"`
		}
		if err := privateipc.DecodeStrict(params, &request, true); err != nil {
			return nil, privateipc.NewError(-32602, "invalid request")
		}
		return request, nil
	})
	server, err := Listen(root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	duplicate, err := Listen(filepath.Join(root, "."), handler)
	if err == nil {
		duplicate.Close()
		t.Fatal("duplicate listener acquired same data root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var clients sync.WaitGroup
	for index := 0; index < 16; index++ {
		clients.Add(1)
		go func(value int) {
			defer clients.Done()
			var result struct {
				Value int `json:"value"`
			}
			if err := Call(ctx, root, "cli/execute", map[string]int{"value": value}, &result); err != nil || result.Value != value {
				t.Errorf("client %d: result=%v err=%v", value, result, err)
			}
		}(index)
	}
	clients.Wait()
	var rpcErr *privateipc.RPCError
	if err := Call(ctx, root, "fail", nil, nil); !errors.As(err, &rpcErr) || rpcErr.Code != -32042 {
		t.Fatalf("typed error lost: %v", err)
	}
	if err := Call(ctx, root, "cli/execute", map[string]bool{"extra": true}, nil); !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
		t.Fatalf("strict request lost: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Call(ctx, root, "cli/execute", nil, nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("call after close: %v", err)
	}
	reopened, err := Listen(root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}

func TestGatewayRequiresMatchingDataRootBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	var calls atomic.Int32
	server, err := Listen(root, privateipc.HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) { calls.Add(1); return true, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	root, err = canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connection, err := dialEndpoint(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	peer := privateipc.NewPeer(connection, connection, nil)
	defer peer.Close()
	go peer.Serve(ctx)
	for _, method := range []string{"cli/execute", handshakeMethod} {
		var remote *privateipc.RPCError
		err := peer.Call(ctx, method, identity{Protocol: Protocol, DataRoot: root + "-other"}, nil)
		if !errors.As(err, &remote) || remote.Code != ErrIdentity.Code {
			t.Fatalf("identity was accepted: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unverified client reached handler")
	}
}

func TestGatewayCloseCancelsHandlers(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	finished := make(chan struct{})
	server, err := Listen(root, privateipc.HandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Call(ctx, root, "wait", nil, nil) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("handler not cancelled")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("pending call succeeded after close")
		}
	case <-ctx.Done():
		t.Fatal("pending client stuck")
	}
}
