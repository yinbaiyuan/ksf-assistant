package feishucli

import (
	"encoding/json"
	"testing"
)

func TestRecoveryCommandsRequireSingleExplicitIdentity(t *testing.T) {
	for _, args := range [][]string{{"events", "review"}, {"events", "retry", "--id", "0123456789abcdef0123456789abcdef"}, {"task-link", "sync-review"}, {"task-link", "sync-retry", "--id", "LINK-123"}, {"task-link", "diagnostics"}} {
		r, e := Parse(args, nil)
		if e != nil {
			t.Fatal(args, e)
		}
		b, _ := json.Marshal(r)
		if _, e := DecodeRequest(b); e != nil {
			t.Fatal(e)
		}
	}
	for _, args := range [][]string{{"events", "retry"}, {"task-link", "sync-retry"}, {"events", "retry", "--all"}} {
		if _, e := Parse(args, nil); e == nil {
			t.Fatal("unscoped retry accepted", args)
		}
	}
}
