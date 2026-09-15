package rpc

import (
	"context"
	"encoding/json"
	"io"
	"ksfassistant/core/internal/service"
	"strings"
	"testing"
)

func TestCardOnlyHostRejectsRetiredMiddleware(t *testing.T) {
	server := New(service.New(), strings.NewReader(""), io.Discard)
	for _, method := range []string{"toolchain/status", "toolchain/install", "userApproval/poll", "userApproval/decide"} {
		_, err := server.dispatch(context.Background(), method, json.RawMessage(`{"interactive":true,"confirm":true,"id":"test","approve":true}`))
		if err == nil || !strings.Contains(err.Error(), "method") {
			t.Errorf("retired endpoint %s remains available: %v", method, err)
		}
	}
}
