package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/service"
)

func TestDesktopAuthRejectsSecretContinuationAndUnknownControlFields(t *testing.T) {
	server := &Server{service: &service.Service{}}
	for _, method := range []string{"feishu/auth/status", "feishu/auth/start", "feishu/auth/finish", "feishu/auth/logout"} {
		_, err := server.dispatch(context.Background(), method, json.RawMessage(`{"deviceCode":"private-secret"}`))
		if err == nil || strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "服务不可用") {
			t.Fatalf("%s accepted secret fields: %v", method, err)
		}
	}
	for _, params := range []string{`{"confirm":false}`, `{"confirm":true,"component":"arbitrary"}`} {
		if _, err := server.dispatch(context.Background(), "toolchain/install", json.RawMessage(params)); err == nil {
			t.Fatal("unsafe toolchain installation admitted")
		}
	}
}

func TestDesktopSetupRPCBudgetIsLimitedToThreeLongOperations(t *testing.T) {
	for _, method := range []string{"feishu/setup/activate", "feishu/setup/verify", "feishu/setup/continue"} {
		if desktopRequestTimeout(method) != 120*time.Second {
			t.Fatalf("missing setup budget: %s", method)
		}
	}
	for _, method := range []string{"dashboard/read", "initialize", "feishu/setup/cancel", "feishu/setup/begin", "feishu/auth/status", "toolchain/install"} {
		if desktopRequestTimeout(method) != 45*time.Second {
			t.Fatalf("unrelated timeout changed: %s", method)
		}
	}
}
