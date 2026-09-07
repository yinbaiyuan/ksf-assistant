package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ksfassistant/core/internal/service"
)

func TestConfigurationRejectsLegacyMutationBypasses(t *testing.T) {
	server := &Server{service: &service.Service{}}
	for _, method := range []string{"feishu/auth/configure", "feishu/auth/start", "feishu/auth/finish", "feishu/auth/logout", "feishu/setup/begin", "feishu/setup/continue", "feishu/setup/activate", "feishu/setup/cancel", "feishu/settings/update", "feishu/features/update", "feishu/supervisor/restart", "feishu/service/control", "feishu/test"} {
		_, err := server.dispatch(context.Background(), method, json.RawMessage(`{"confirm":true}`))
		if err == nil || !strings.Contains(err.Error(), "configuration_legacy_mutation_disabled") {
			t.Fatalf("legacy mutation remained reachable: %s %v", method, err)
		}
	}
}

func TestConfigurationRPCRejectsUnknownDuplicateAndSecretControlFields(t *testing.T) {
	server := &Server{service: &service.Service{}}
	for _, params := range []string{`null`, `[]`, `{"action":"restart","action":"logout"}`, `{"approved":true}`, `{"refresh":true,"deviceCode":"private-secret"}`} {
		_, err := server.dispatch(context.Background(), "feishu/configuration/action", json.RawMessage(params))
		if err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatalf("invalid action accepted: %s %v", params, err)
		}
	}
}

func TestConfigurationRPCOptionalFieldsAreActuallyOptional(t *testing.T) {
	var request service.ConfigurationActionRequest
	err := decodeConfigurationParams(json.RawMessage(`{"action":"restart","requestId":"fixture","epoch":"fixture","revision":1,"contextRevision":"fixture","confirm":true}`), &request, "action", "requestId", "epoch", "revision", "contextRevision", "confirm", "appId", "appSecret", "targetAlias", "feature", "mode", "flowId")
	if err != nil || request.Action != "restart" || !request.Confirm {
		t.Fatalf("valid optional request rejected: %+v %v", request, err)
	}
}
