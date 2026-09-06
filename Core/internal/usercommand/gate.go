package usercommand

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"ksfassistant/core/internal/privateipc"
)

func CallGate(ctx context.Context, caller CallerFunc, command Command) (string, error) {
	review, err := Evaluate(command)
	if err != nil {
		return "", err
	}
	if !review.NeedsApproval {
		return "", nil
	}
	if caller == nil {
		return "", errors.New("user_approval_desktop_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var request RequestResult
	if err := caller(ctx, "userApproval/request", command, &request); err != nil {
		return "", approvalCallError(err)
	}
	if request.ID == "" {
		return "", errors.New("user_approval_invalid_response")
	}
	consumed := false
	defer func() {
		if !consumed {
			cancelContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var result any
			_ = caller(cancelContext, "userApproval/cancel", StatusRequest{ID: request.ID}, &result)
		}
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status StatusResult
		if err := caller(ctx, "userApproval/status", StatusRequest{ID: request.ID}, &status); err != nil {
			return "", approvalCallError(err)
		}
		switch status.State {
		case "approved":
			var result ConsumeResult
			if err := caller(ctx, "userApproval/consume", ConsumeRequest{ID: request.ID, Digest: review.Digest}, &result); err != nil {
				return "", approvalCallError(err)
			}
			if !result.Allowed {
				return "", errors.New("user_approval_not_allowed")
			}
			consumed = true
			return request.ID, nil
		case "pending":
		case "denied", "rejected":
			return "", errors.New("user_approval_denied")
		case "expired":
			return "", errors.New("user_approval_expired")
		case "cancelled":
			return "", errors.New("user_approval_cancelled")
		case "desktop_unavailable", "audit_unavailable", "request_changed", "closed":
			return "", errors.New("user_approval_" + status.State)
		default:
			return "", errors.New("user_approval_invalid_state")
		}
		select {
		case <-ctx.Done():
			return "", approvalCallError(ctx.Err())
		case <-ticker.C:
		}
	}
}

func approvalCallError(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.New("user_approval_cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("user_approval_expired")
	}
	for _, closed := range []error{privateipc.ErrPeerClosed, io.EOF, io.ErrClosedPipe, net.ErrClosed, syscall.EPIPE, syscall.ECONNRESET} {
		if errors.Is(err, closed) {
			return errors.New("user_approval_desktop_unavailable")
		}
	}
	return err
}

func ReportResult(ctx context.Context, caller CallerFunc, id, outcome string) error {
	if id == "" {
		return nil
	}
	switch outcome {
	case "succeeded", "failed", "unknown", "not_started":
	default:
		return errors.New("user_approval_invalid_outcome")
	}
	if caller == nil {
		return errors.New("user_approval_desktop_unavailable")
	}
	var result any
	return caller(ctx, "userApproval/result", ResultRequest{ID: id, Outcome: outcome}, &result)
}
