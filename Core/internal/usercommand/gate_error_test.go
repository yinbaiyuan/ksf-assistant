package usercommand

import (
	"context"
	"errors"
	"testing"
)

func TestApprovalCancellationAndExpiryHaveDistinctCodes(t *testing.T) {
	for _, scenario := range []struct {
		err  error
		code string
	}{{context.Canceled, "user_approval_cancelled"}, {context.DeadlineExceeded, "user_approval_expired"}, {errors.New("approval_busy"), "approval_busy"}} {
		if actual := approvalCallError(scenario.err); actual == nil || actual.Error() != scenario.code {
			t.Fatalf("%v", actual)
		}
	}
}
