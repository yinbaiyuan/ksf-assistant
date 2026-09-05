package privateipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func receiveWithin[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("operation did not finish")
		var zero T
		return zero
	}
}

func TestPeerSlowWriterDeadlineAndCancel(t *testing.T) {
	for _, operation := range []string{"call", "notify", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			local, remote := net.Pipe()
			defer remote.Close()
			peer := NewPeer(local, local, nil)
			defer peer.Close()
			go peer.Serve(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			if operation == "notify" {
				go func() { done <- peer.Notify(ctx, "wait", nil) }()
			} else {
				go func() { done <- peer.Call(ctx, "wait", nil, nil) }()
				if operation == "cancel" {
					if _, err := bufio.NewReader(remote).ReadBytes('\n'); err != nil {
						t.Fatal(err)
					}
					cancel()
				}
			}
			err := receiveWithin(t, done)
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected context error, got %v", err)
			}
		})
	}
}

func TestPeerHandlerBoundStillProcessesCancellationAndResponses(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	started := make(chan struct{}, MaxConcurrentHandlers)
	finished := make(chan struct{}, MaxConcurrentHandlers)
	peer := NewPeer(local, local, HandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		finished <- struct{}{}
		return nil, ctx.Err()
	}))
	defer peer.Close()
	go peer.Serve(context.Background())
	responses := make(chan frame, 8)
	go func() {
		scanner := bufio.NewScanner(remote)
		for scanner.Scan() {
			var response frame
			if json.Unmarshal(scanner.Bytes(), &response) == nil {
				responses <- response
			}
		}
	}()
	for index := 0; index < MaxConcurrentHandlers; index++ {
		_, err := fmt.Fprintf(remote, "{\"jsonrpc\":\"2.0\",\"id\":\"in-%d\",\"method\":\"wait\"}\n", index)
		if err != nil {
			t.Fatal(err)
		}
		receiveWithin(t, started)
	}
	_, _ = io.WriteString(remote, "{\"jsonrpc\":\"2.0\",\"id\":\"overflow\",\"method\":\"wait\"}\n")
	reply := receiveWithin(t, responses)
	if reply.Error == nil || reply.Error.Code != ErrBusy.Code {
		t.Fatalf("missing bounded overload: %#v", reply)
	}
	callDone := make(chan error, 1)
	go func() { callDone <- peer.Call(context.Background(), "reverse", nil, nil) }()
	request := receiveWithin(t, responses)
	_, _ = fmt.Fprintf(remote, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":true}\n", request.ID)
	if err := receiveWithin(t, callDone); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(remote, "{\"jsonrpc\":\"2.0\",\"method\":\"$/cancelRequest\",\"params\":{\"id\":\"in-0\"}}\n")
	receiveWithin(t, finished)
	_ = remote.Close()
	for index := 1; index < MaxConcurrentHandlers; index++ {
		receiveWithin(t, finished)
	}
}

func TestPeerEOFCancelsNotificationsAndRequests(t *testing.T) {
	for _, id := range []string{"", `,"id":"request"`} {
		t.Run(id, func(t *testing.T) {
			local, remote := net.Pipe()
			started := make(chan struct{})
			finished := make(chan error, 1)
			peer := NewPeer(local, local, HandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
				close(started)
				<-ctx.Done()
				finished <- ctx.Err()
				return nil, ctx.Err()
			}))
			defer peer.Close()
			go peer.Serve(context.Background())
			_, _ = fmt.Fprintf(remote, "{\"jsonrpc\":\"2.0\",\"method\":\"wait\"%s}\n", id)
			receiveWithin(t, started)
			_ = remote.Close()
			if err := receiveWithin(t, finished); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestPeerPendingCallsBounded(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	peer := NewPeer(reader, io.Discard, nil)
	defer peer.Close()
	go peer.Serve(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls sync.WaitGroup
	calls.Add(MaxPendingCalls)
	for index := 0; index < MaxPendingCalls; index++ {
		go func() { defer calls.Done(); _ = peer.Call(ctx, "wait", nil, nil) }()
	}
	deadline := time.Now().Add(time.Second)
	for {
		peer.mu.Lock()
		count := len(peer.pending)
		peer.mu.Unlock()
		if count == MaxPendingCalls {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending calls did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := peer.Call(ctx, "overflow", nil, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected overload, got %v", err)
	}
	cancel()
	calls.Wait()
}

func TestPeerEnvelopeAndResultRemainStrict(t *testing.T) {
	for _, value := range []string{
		`{"jsonrpc":"2.0","method":"bad","extra":true}`,
		`{"jsonrpc":"2.0","method":"bad"} {}`,
	} {
		var calls atomic.Int32
		local, remote := net.Pipe()
		defer remote.Close()
		peer := NewPeer(local, local, HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) { calls.Add(1); return nil, nil }))
		defer peer.Close()
		go peer.Serve(context.Background())
		_ = remote.SetDeadline(time.Now().Add(time.Second))
		if _, err := io.WriteString(remote, value+"\n"); err != nil {
			t.Fatal(err)
		}
		reply, err := bufio.NewReader(remote).ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 0 || !strings.Contains(string(reply), `"code":-32700`) {
			t.Fatalf("non-strict envelope accepted: %s", reply)
		}
	}
	local, remote := net.Pipe()
	peer := NewPeer(local, local, nil)
	other := NewPeer(remote, remote, HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]any{"known": true, "unknown": true}, nil
	}))
	defer peer.Close()
	defer other.Close()
	go peer.Serve(context.Background())
	go other.Serve(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var result struct {
		Known bool `json:"known"`
	}
	if err := peer.Call(ctx, "strict", nil, &result); err == nil {
		t.Fatal("unknown result field accepted")
	}
}

func TestPeerServeCancellationInterruptsRead(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	peer := NewPeer(local, local, nil)
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- peer.Serve(ctx) }()
	cancel()
	receiveWithin(t, done)
}

type stalledWriter struct{ release chan struct{} }

func (writer stalledWriter) Write(data []byte) (int, error) {
	<-writer.release
	return len(data), nil
}

func TestPeerMalformedReplyCannotDelayEOFCancellation(t *testing.T) {
	reader, input := io.Pipe()
	defer input.Close()
	writer := stalledWriter{release: make(chan struct{})}
	defer close(writer.release)
	started := make(chan struct{})
	finished := make(chan struct{})
	peer := NewPeer(reader, writer, HandlerFunc(func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}))
	defer peer.Close()
	go peer.Serve(context.Background())
	_, _ = io.WriteString(input, "{\"jsonrpc\":\"2.0\",\"method\":\"wait\"}\n")
	receiveWithin(t, started)
	_, _ = io.WriteString(input, "not-json\n")
	_ = input.Close()
	receiveWithin(t, finished)
}
