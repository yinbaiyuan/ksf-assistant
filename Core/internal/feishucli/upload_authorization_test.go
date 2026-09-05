package feishucli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
)

func TestRunPreservesAuthorizationChallengeAndReturnsFailure(t *testing.T) {
	root := t.TempDir()
	confirmation := json.RawMessage(`{"status":"authorization_required","operation":{"id":"OP-test","status":"awaiting_confirmation"},"challenge":"challenge-retained","submitted":false,"nextAction":"confirm_then_retry_same_request"}`)
	var executions atomic.Int32
	server, err := localipc.Listen(root, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != MethodExecute {
			return nil, privateipc.ErrMethodNotFound
		}
		executions.Add(1)
		return nil, &privateipc.RPCError{Code: -32063, Message: "authorization required", Data: confirmation}
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	var output bytes.Buffer
	err = Run(context.Background(), root, []string{"send", "--target", "alias", "--text-file", "-"}, strings.NewReader("hello"), &output)
	var rpcError *privateipc.RPCError
	if !errors.As(err, &rpcError) || rpcError.Code != -32063 {
		t.Fatalf("authorization must remain a nonzero failure: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(output.Bytes()), confirmation) {
		t.Fatalf("authorization data changed or missing: %s", output.String())
	}
	if executions.Load() != 1 {
		t.Fatalf("request was replayed: executions=%d", executions.Load())
	}
}
