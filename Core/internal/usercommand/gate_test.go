package usercommand

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"ksfassistant/core/internal/privateipc"
)

func TestGateConnectionLossNeverGrantsExecution(t *testing.T) {
	command := frozenTest(t, "user", "im", "+messages-send", "--chat-id", "oc_fixture", "--text", "hello")
	for _, phase := range []string{"userApproval/request", "userApproval/status", "userApproval/consume"} {
		t.Run(phase, func(t *testing.T) {
			for _, cause := range []error{privateipc.ErrPeerClosed, io.EOF, io.ErrClosedPipe, net.ErrClosed, syscall.EPIPE, syscall.ECONNRESET} {
				calls := map[string]int{}
				caller := func(ctx context.Context, method string, params, target any) error {
					calls[method]++
					if method == phase {
						return fmt.Errorf("transport closed: %w", cause)
					}
					switch method {
					case "userApproval/request":
						target.(*RequestResult).ID = "request"
					case "userApproval/status":
						target.(*StatusResult).State = "approved"
					case "userApproval/cancel":
					default:
						t.Fatalf("unexpected call after connection loss: %s", method)
					}
					return nil
				}
				id, err := CallGate(context.Background(), caller, command)
				if id != "" || err == nil || err.Error() != "user_approval_desktop_unavailable" || calls[phase] != 1 {
					t.Fatalf("connection loss %v: id=%q err=%v calls=%v", cause, id, err, calls)
				}
				if phase != "userApproval/consume" && calls["userApproval/consume"] != 0 {
					t.Fatal("connection loss attempted consumption")
				}
			}
		})
	}
}

func TestApprovalCallErrorPreservesDistinctFailures(t *testing.T) {
	for cause, expected := range map[error]string{context.Canceled: "user_approval_cancelled", context.DeadlineExceeded: "user_approval_expired"} {
		if got := approvalCallError(cause); got.Error() != expected {
			t.Fatalf("%v: %v", cause, got)
		}
	}
	for _, cause := range []error{privateipc.NewError(-32000, "user_approval_denied"), privateipc.ErrFrameTooLarge, errors.New("private IPC peer closed")} {
		if got := approvalCallError(cause); got != cause {
			t.Fatalf("unrelated or string-only error reclassified: %v", got)
		}
	}
}
