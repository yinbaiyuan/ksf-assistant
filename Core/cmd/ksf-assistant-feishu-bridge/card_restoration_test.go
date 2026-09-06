package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
)

type retryRestorationClient struct {
	attempts   int
	failure    error
	alwaysFail bool
}

func (client *retryRestorationClient) VerifyBotMessage(context.Context, feishu.MessageTarget, string) error {
	client.attempts++
	if client.attempts == 1 || client.alwaysFail {
		return client.failure
	}
	return nil
}

func TestCardRestorationIsBoundedAndCancelled(t *testing.T) {
	root := t.TempDir()
	config := feishu.DefaultClientConfig()
	config.MessageTargets["fixture"] = feishu.MessageTarget{Type: "open_id", ID: "fixture-user"}
	config.DirectAllowedAliases = []string{"fixture"}
	if err := feishu.NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	client := &retryRestorationClient{failure: errors.New("temporary network failure"), alwaysFail: true}
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	server.restorationDelays = []time.Duration{0, time.Millisecond, time.Millisecond}
	bindings := []feishuprotocol.CardBinding{{TargetType: "open_id", TargetID: "fixture-user", MessageID: "old-card"}}
	transport := feishu.NewServiceTransport(root, client)
	server.restoreCardBindings(context.Background(), transport, bindings)
	if client.attempts != 3 || server.bindingState != "degraded" || server.bindingDetail == "" {
		t.Fatal("restoration retries were unbounded or invisible")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server.restorationDelays = []time.Duration{time.Hour}
	server.restoreCardBindings(ctx, transport, bindings)
	if client.attempts != 3 {
		t.Fatal("cancelled restoration attempted SDK")
	}
}
func (client *retryRestorationClient) Send(context.Context, feishu.MessageTarget, string, string, string) (string, error) {
	return "fixture", nil
}
func (client *retryRestorationClient) Reply(context.Context, string, string, string, string) (string, error) {
	return "fixture", nil
}
func (client *retryRestorationClient) PatchCard(context.Context, string, string) error { return nil }

func TestCardRestorationRetriesOnlyTemporaryFailure(t *testing.T) {
	for _, test := range []struct {
		name, failure, state string
		attempts             int
	}{
		{"temporary", "temporary network failure", "ready", 2},
		{"ownership", "restoration_not_bot_card", "degraded", 1},
		{"permission", "restoration_message_get_failed: code=403", "degraded", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			config := feishu.DefaultClientConfig()
			config.MessageTargets["fixture"] = feishu.MessageTarget{Type: "open_id", ID: "fixture-user"}
			config.DirectAllowedAliases = []string{"fixture"}
			if err := feishu.NewClientConfigStore(root).Save(config); err != nil {
				t.Fatal(err)
			}
			client := &retryRestorationClient{failure: errors.New(test.failure)}
			server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
			server.restorationDelays = []time.Duration{0, time.Millisecond}
			server.restoreCardBindings(context.Background(), feishu.NewServiceTransport(root, client), []feishuprotocol.CardBinding{{TargetType: "open_id", TargetID: "fixture-user", MessageID: "old-card"}})
			if client.attempts != test.attempts || server.bindingState != test.state {
				t.Fatalf("attempts=%d state=%s", client.attempts, server.bindingState)
			}
		})
	}
}
