package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/service"
)

func TestNativeApprovalStrictParams(t *testing.T) {
	server := New(service.New(), strings.NewReader(""), io.Discard)
	for _, raw := range []string{`{}`, `{"id":"test","approve":null}`, `{"id":"test","approve":false,"approve":true}`, `{"id":"test","approve":true,"approved":true}`} {
		if _, err := server.dispatch(context.Background(), "userApproval/decide", json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := server.dispatch(context.Background(), "userApproval/request", json.RawMessage(`{}`)); err == nil {
		t.Fatal("host exposed unbound execution")
	}
}

func TestNativeControlAndShutdownDoNotWaitForOpenInput(t *testing.T) {
	input, send := io.Pipe()
	receive, output := io.Pipe()
	defer send.Close()
	defer receive.Close()
	server := New(service.New(), input, output)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx); output.Close() }()
	go func() {
		_, _ = io.WriteString(send, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"userApproval/poll\",\"params\":{\"interactive\":false}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"shutdown\",\"params\":{}}\n")
	}()
	scanner := bufio.NewScanner(receive)
	count := 0
	for scanner.Scan() {
		count++
	}
	if count != 2 {
		t.Fatalf("responses %d", count)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown blocked")
	}
}
