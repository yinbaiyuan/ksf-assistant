package privateipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPeerSupportsConcurrentBidirectionalCalls(t *testing.T) {
	leftConn, rightConn := net.Pipe()
	defer leftConn.Close()
	defer rightConn.Close()

	left := NewPeer(leftConn, leftConn, HandlerFunc(func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "left/echo" {
			return nil, ErrMethodNotFound
		}
		var value string
		_ = json.Unmarshal(params, &value)
		return "left:" + value, nil
	}))
	right := NewPeer(rightConn, rightConn, HandlerFunc(func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "right/echo" {
			return nil, ErrMethodNotFound
		}
		var value string
		_ = json.Unmarshal(params, &value)
		return "right:" + value, nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = left.Serve(ctx) }()
	go func() { _ = right.Serve(ctx) }()

	var wg sync.WaitGroup
	wg.Add(2)
	var leftResult, rightResult string
	var leftErr, rightErr error
	go func() {
		defer wg.Done()
		leftErr = left.Call(ctx, "right/echo", "one", &leftResult)
	}()
	go func() {
		defer wg.Done()
		rightErr = right.Call(ctx, "left/echo", "two", &rightResult)
	}()
	wg.Wait()
	if leftErr != nil || rightErr != nil {
		t.Fatalf("bidirectional calls failed: left=%v right=%v", leftErr, rightErr)
	}
	if leftResult != "right:one" || rightResult != "left:two" {
		t.Fatalf("unexpected results: %q %q", leftResult, rightResult)
	}
}

func TestPeerCallReturnsRemoteAndContextErrors(t *testing.T) {
	leftConn, rightConn := net.Pipe()
	defer leftConn.Close()
	defer rightConn.Close()
	left := NewPeer(leftConn, leftConn, nil)
	right := NewPeer(rightConn, rightConn, HandlerFunc(func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		if method == "fail" {
			return nil, NewError(-32041, "capability unavailable")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = left.Serve(ctx) }()
	go func() { _ = right.Serve(ctx) }()

	var result any
	err := left.Call(ctx, "fail", nil, &result)
	var remote *RPCError
	if !errors.As(err, &remote) || remote.Code != -32041 {
		t.Fatalf("expected typed remote error, got %v", err)
	}

	timeout, stop := context.WithTimeout(ctx, 25*time.Millisecond)
	defer stop()
	err = left.Call(timeout, "wait", nil, &result)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestPeerRejectsOversizedAndMalformedFrames(t *testing.T) {
	var output bytes.Buffer
	peer := NewPeer(strings.NewReader(strings.Repeat("x", MaxFrameBytes+1)+"\n"), &output, nil)
	err := peer.Serve(context.Background())
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected frame-too-large error, got %v", err)
	}

	output.Reset()
	peer = NewPeer(strings.NewReader("not-json\n"), &output, nil)
	if err := peer.Serve(context.Background()); err != nil {
		t.Fatalf("malformed request should produce a protocol response, got %v", err)
	}
	if !strings.Contains(output.String(), `"code":-32700`) {
		t.Fatalf("expected JSON parse error response, got %s", output.String())
	}
}

func TestPeerEOFFailsPendingCalls(t *testing.T) {
	leftConn, rightConn := net.Pipe()
	left := NewPeer(leftConn, leftConn, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- left.Serve(ctx) }()
	callDone := make(chan error, 1)
	go func() {
		var result any
		callDone <- left.Call(ctx, "never/replies", nil, &result)
	}()
	var request [1024]byte
	_, _ = rightConn.Read(request[:])
	_ = rightConn.Close()
	if err := <-done; err != nil {
		t.Fatalf("clean EOF should stop serve without error, got %v", err)
	}
	if err := <-callDone; !errors.Is(err, ErrPeerClosed) {
		t.Fatalf("pending call should fail with peer closed, got %v", err)
	}
}
