// Package retrypolicy defines bounded background retry budgets.
package retrypolicy

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"time"
)

func Exhausted(attempts int, start, now time.Time) bool {
	return attempts >= 20 || (!start.IsZero() && now.Sub(start) >= time.Hour)
}
func Delay(attempt int, key string) time.Duration {
	values := []time.Duration{time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}
	d := 5 * time.Minute
	if attempt > 0 && attempt <= len(values) {
		d = values[attempt-1]
	}
	h := fnv.New32a()
	h.Write([]byte(key))
	h.Write([]byte{byte(attempt)})
	return d * time.Duration(80+h.Sum32()%41) / 100
}

// Class never exposes arbitrary remote text in diagnostics.
func Class(err error) (string, bool) {
	if errors.Is(err, context.Canceled) {
		return "cancelled", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "invalid_remote_response") {
		return "remote_response_invalid", false
	}
	for _, x := range []struct{ match, code string }{
		{"feishu_login_required", "login_required"}, {"class=authentication", "authentication_failed"}, {"class=authorization", "permission_denied"}, {"outcome_unknown", "outcome_unknown"}, {"outcome_unconfirmed", "outcome_unknown"}, {"mismatch", "binding_conflict"}, {"conflict", "binding_conflict"}, {"unbound", "binding_missing"}, {"not a governed", "binding_missing"}, {"not authorized", "permission_denied"}, {"not_authorized", "permission_denied"}, {"status=403", "permission_denied"}, {"status=404", "message_missing"}, {"status=401", "authentication_failed"}, {"invalid", "invalid_payload"}, {"unsafe", "unsafe_state"}, {"disabled", "disabled"}, {"dry-run", "disabled"}, {"confirmation", "confirmation_required"}} {
		if strings.Contains(s, x.match) {
			return x.code, true
		}
	}
	if strings.Contains(s, "timeout") || strings.Contains(s, "deadline") {
		return "timeout", false
	}
	if strings.Contains(s, "connection") || strings.Contains(s, "eof") {
		return "connection_lost", false
	}
	return "temporary_failure", false
}
