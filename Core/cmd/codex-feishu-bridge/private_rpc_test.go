package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"codexusagebar/core/internal/corebridge"
	"codexusagebar/core/internal/feishu"
	"codexusagebar/core/internal/privateipc"
)

type bridgeCapabilityExecutor struct {
	mu    sync.Mutex
	calls int
}

func (executor *bridgeCapabilityExecutor) ExecuteWithOptions(_ context.Context, capabilityID string, _ map[string]any, _ feishu.CapabilityExecutionOptions) (map[string]any, error) {
	executor.mu.Lock()
	executor.calls++
	executor.mu.Unlock()
	return map[string]any{"capabilityId": capabilityID}, nil
}

func (executor *bridgeCapabilityExecutor) ReadPreflight(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	return nil, nil
}

func (executor *bridgeCapabilityExecutor) ReadVerification(_ context.Context, _ string, _ map[string]any, _ map[string]any) (feishu.VerificationAssessment, error) {
	return feishu.VerificationAssessment{State: feishu.VerificationInconclusive}, nil
}

func (executor *bridgeCapabilityExecutor) callCount() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.calls
}

func newTestBridgeRPCServer(root string, settings feishu.Settings) *bridgeRPCServer {
	runner := feishu.UnifiedCapabilityExecutor{DataRoot: root, LongTail: feishu.CapabilityExecutor{DataRoot: root, WorkingDirectory: root}}
	service := feishu.NewCapabilityService(root, runner, nil)
	return newBridgeRPCServer(root, settings, service)
}

func TestBridgeSnapshotDegradesCapabilitiesIndependently(t *testing.T) {
	settings := feishu.DefaultSettings()
	settings.Outbound.Enabled = true
	settings.Actionbox.Enabled = true
	server := newTestBridgeRPCServer(t.TempDir(), settings)
	snapshot := server.snapshot(context.Background())
	if snapshot.ProcessState != "running" {
		t.Fatalf("bridge process must remain running: %#v", snapshot)
	}
	if snapshot.Capabilities["feishuOutbound"].State != "unavailable" {
		t.Fatalf("outbound state = %#v", snapshot.Capabilities["feishuOutbound"])
	}
	if snapshot.Capabilities["actionbox"].State != "degraded" {
		t.Fatalf("actionbox state = %#v", snapshot.Capabilities["actionbox"])
	}
	if snapshot.Capabilities["codexAppServer"].State != "unavailable" || snapshot.Capabilities["ksfContext"].State != "unavailable" {
		t.Fatalf("Core capabilities = %#v", snapshot.Capabilities)
	}
}

func TestBridgeHandshakeRejectsWrongProtocol(t *testing.T) {
	server := newTestBridgeRPCServer(t.TempDir(), feishu.DefaultSettings())
	params, _ := json.Marshal(corebridge.InitializeRequest{Protocol: "wrong"})
	if _, err := server.HandlePrivateRPC(context.Background(), corebridge.MethodBridgeInitialize, params); err == nil {
		t.Fatal("protocol mismatch must be rejected")
	}
}

func TestBridgePrivateRPCRejectsUnknownAndUnexpectedParams(t *testing.T) {
	server := newTestBridgeRPCServer(t.TempDir(), feishu.DefaultSettings())
	tests := []struct {
		name   string
		method string
		params json.RawMessage
	}{
		{name: "typed request", method: corebridge.MethodBridgeInitialize, params: json.RawMessage(`{"protocol":"codexassistant-core-bridge-v1","unexpected":true}`)},
		{name: "parameterless method", method: corebridge.MethodBridgeSnapshotRead, params: json.RawMessage(`{"unexpected":true}`)},
		{name: "malformed optional auth value", method: corebridge.MethodAuthFinish, params: json.RawMessage(`{"deviceCode":"code","unexpected":true}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.HandlePrivateRPC(context.Background(), test.method, test.params)
			var rpcErr *privateipc.RPCError
			if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
				t.Fatalf("expected -32602, got %T %v", err, err)
			}
		})
	}
}

func TestBridgePrivateRPCExposesGovernedOperationAndPolicy(t *testing.T) {
	root := t.TempDir()
	settings := feishu.DefaultSettings()
	settings.Actionbox.Enabled = true
	settings.Actionbox.DryRun = false
	if err := feishu.NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	server := newTestBridgeRPCServer(root, settings)
	policyValue, err := server.HandlePrivateRPC(context.Background(), corebridge.MethodPolicyRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy, ok := policyValue.(feishu.CapabilityPolicy)
	if !ok || policy.RiskDefaults["destructive"] != feishu.CapabilityDisabled {
		t.Fatalf("unexpected policy: %#v", policyValue)
	}
	params, _ := json.Marshal(map[string]any{
		"capabilityId": "im.chat.create", "input": map[string]any{"name": "rpc test"}, "source": "core",
	})
	value, err := server.HandlePrivateRPC(context.Background(), corebridge.MethodOperationPrepare, params)
	if err != nil {
		t.Fatal(err)
	}
	prepared, ok := value.(feishu.PreparedOperation)
	if !ok || prepared.Operation.Status != feishu.OperationQueued || !prepared.Submitted {
		t.Fatalf("unexpected prepared operation: %#v", value)
	}
}

func TestBridgePrivateRPCReturnsStableAdviceForGovernanceRejection(t *testing.T) {
	root := t.TempDir()
	settings := feishu.DefaultSettings()
	settings.Actionbox.Enabled = true
	settings.Actionbox.DryRun = false
	if err := feishu.NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	server := newTestBridgeRPCServer(root, settings)
	params, _ := json.Marshal(map[string]any{
		"capabilityId": "im.sdk.message.send",
		"input":        map[string]any{"request-id": "OUT-rpc-advice", "target-type": "open_id", "target-id": "ou_private", "format": "text", "text": "private", "source": "test"},
		"source":       "core",
	})
	value, err := server.HandlePrivateRPC(context.Background(), corebridge.MethodOperationPrepare, params)
	if err != nil {
		t.Fatal(err)
	}
	prepared, ok := value.(feishu.PreparedOperation)
	if !ok || prepared.ErrorCode != "outbound_disabled" || prepared.NextAction != "enable_outbound" {
		t.Fatalf("unexpected rejection: %#v", value)
	}
}

func TestBridgeRPCAndConcurrentActionboxTriggersShareOneCapabilityService(t *testing.T) {
	root := t.TempDir()
	settings := feishu.DefaultSettings()
	settings.Actionbox.Enabled = true
	settings.Actionbox.DryRun = false
	if err := feishu.NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	executor := &bridgeCapabilityExecutor{}
	service := feishu.NewCapabilityService(root, executor, nil)
	server := newBridgeRPCServer(root, settings, service)
	if server.capability != service {
		t.Fatal("RPC server did not retain the process capability service")
	}
	params, _ := json.Marshal(map[string]any{
		"capabilityId": "im.chat.create", "input": map[string]any{"name": "single service"}, "source": "core",
	})
	value, err := server.HandlePrivateRPC(context.Background(), corebridge.MethodOperationPrepare, params)
	if err != nil {
		t.Fatal(err)
	}
	prepared := value.(feishu.PreparedOperation)

	start := make(chan struct{})
	var wait sync.WaitGroup
	triggers := []func(){
		actionboxWakeHandler(context.Background(), service),
		func() { _ = service.ProcessActions(context.Background()) },
	}
	for _, trigger := range triggers {
		trigger := trigger
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			trigger()
		}()
	}
	close(start)
	wait.Wait()

	status, err := service.Status(prepared.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != feishu.OperationSucceeded || executor.callCount() != 1 {
		t.Fatalf("status=%#v calls=%d", status, executor.callCount())
	}
}

func TestBridgeRPCSDKAndDocumentCapabilitiesUseTheInjectedService(t *testing.T) {
	tests := []struct {
		name         string
		capabilityID string
		input        map[string]any
		allow        bool
	}{
		{
			name: "sdk message", capabilityID: "im.sdk.message.send", allow: true,
			input: map[string]any{
				"request-id": "OUT-rpc-route", "target-type": "open_id", "target-id": "ou_private",
				"format": "text", "text": "private", "source": "test",
			},
		},
		{
			name: "document service", capabilityID: "docs.service.document.append",
			input: map[string]any{
				"target-kind": "docx_token", "target-value": "doc_private", "content": "private",
				"format": "markdown", "source": "test",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			settings := feishu.DefaultSettings()
			settings.Actionbox.Enabled = true
			settings.Actionbox.DryRun = false
			settings.Outbound.Enabled = true
			settings.Outbound.DryRun = false
			settings.Docbox.Enabled = true
			settings.Docbox.DryRun = false
			if err := feishu.NewSettingsStore(root).Save(settings); err != nil {
				t.Fatal(err)
			}
			executor := &bridgeCapabilityExecutor{}
			service := feishu.NewCapabilityService(root, executor, nil)
			if test.allow {
				policy, err := service.ReadPolicy()
				if err != nil {
					t.Fatal(err)
				}
				policy.CapabilityOverrides[test.capabilityID] = feishu.CapabilityAllowed
				if _, err := service.UpdatePolicy(policy, policy.Revision); err != nil {
					t.Fatal(err)
				}
			}
			server := newBridgeRPCServer(root, settings, service)
			params, _ := json.Marshal(map[string]any{"capabilityId": test.capabilityID, "input": test.input, "source": "core"})
			value, err := server.HandlePrivateRPC(context.Background(), corebridge.MethodOperationPrepare, params)
			if err != nil {
				t.Fatal(err)
			}
			prepared := value.(feishu.PreparedOperation)
			if !prepared.Submitted {
				t.Fatalf("operation was not submitted: %#v", prepared)
			}
			if err := service.ProcessActions(context.Background()); err != nil {
				t.Fatal(err)
			}
			view, err := service.Status(prepared.Operation.ID)
			if err != nil || view.Status != feishu.OperationSucceeded || executor.callCount() != 1 {
				t.Fatalf("view=%#v calls=%d err=%v", view, executor.callCount(), err)
			}
		})
	}
}
