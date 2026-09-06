package feishu

import (
	"errors"
	"testing"
)

func TestCLIHTTPStatusPreservesOnlyExplicitNumericStatus(t *testing.T) {
	for _, fixture := range []struct {
		body   string
		status int
	}{
		{`{"ok":false,"error":{"http_status":429,"message":"secret"}}`, 429},
		{`{"ok":false,"error":{"status_code":503,"type":"api","subtype":"unavailable","message":"secret"}}`, 503},
		{`{"ok":false,"error":{"code":429}}`, 0},
		{`{"ok":false,"error":{"http_status":"secret"}}`, 0},
		{`{"ok":false,"error":{"http_status":429.5}}`, 0},
		{`{"ok":false,"error":{"http_status":999}}`, 0},
	} {
		failure := commandExecutionError("lark_cli_failed", 3, true, errors.New("private cause"), []byte(fixture.body))
		if fixture.status != 0 && failure.Structured["http_status"] != fixture.status {
			t.Fatalf("missing status: %+v", failure.Structured)
		}
		if fixture.status == 0 && failure.Structured["http_status"] != nil {
			t.Fatal("status guessed from non-status/error data")
		}
		if failure.Structured["message"] != nil || !failure.Started || failure.Outcome != "unknown" {
			t.Fatal("error leaked details or changed execution outcome")
		}
	}
}
