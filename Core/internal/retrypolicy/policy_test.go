package retrypolicy

import (
	"errors"
	"testing"
	"time"
)

func TestBoundedScheduleAndPermanentFailures(t *testing.T) {
	base := []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}
	for i, b := range base {
		d := Delay(i+1, "item")
		if d < b*80/100 || d > b*120/100 {
			t.Fatal(d)
		}
	}
	now := time.Now()
	if !Exhausted(20, now, now) || !Exhausted(1, now.Add(-time.Hour), now) || Exhausted(19, now, now) {
		t.Fatal("invalid budget")
	}
	for _, s := range []string{"operation_request_mismatch", "unbound Feishu message", "lark_cli_failed: status=403", "transport_patch_outcome_unconfirmed"} {
		if _, permanent := Class(errors.New(s)); !permanent {
			t.Fatal(s)
		}
	}
}
