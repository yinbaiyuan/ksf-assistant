package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privateipc"
)

func TestLegacyEventRolesNeverDisableInboundReadiness(t *testing.T) {
	for _, legacy := range []string{"", "primary", "manual-only", "retired-role"} {
		for _, connected := range []bool{false, true} {
			settings := feishu.DefaultSettings()
			settings.Profile = legacy
			settings.Outbound.Enabled = true
			settings.Outbound.DryRun = false
			root := t.TempDir()
			t.Setenv("LARK_CLI_BIN", filepath.Join(root, "missing-cli"))
			server := newTestBridgeRPCServer(root, settings)
			server.messages = &feishu.OfficialMessageClient{}
			if connected {
				if err := feishu.NewEventConsumerStateStore(root).UpdateConnection("connected"); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := server.snapshot(context.Background())
			expected := "degraded"
			if connected {
				expected = "ready"
			}
			if snapshot.Profile != "managed" || !snapshot.ProfileValid || snapshot.Capabilities["feishuInbound"].State != expected || snapshot.InboundConnection != connected {
				t.Fatalf("legacy %q connected=%v: %+v", legacy, connected, snapshot)
			}
			if slices.Contains(snapshot.ReadinessBlockers, "feishuInbound") == connected || !slices.Contains(snapshot.ReadinessBlockers, "larkCLI") || snapshot.Availability == "ready" {
				t.Fatalf("readiness gate bypassed: %+v", snapshot)
			}
			server.messages = nil
			snapshot = server.snapshot(context.Background())
			if snapshot.InboundConnection || snapshot.Configured || !slices.Contains(snapshot.ReadinessBlockers, "feishuInbound") {
				t.Fatalf("cached event connection bypassed identity gate: %+v", snapshot)
			}
		}
	}
}

func TestRetiredEventProfileRPCNeverWritesSettings(t *testing.T) {
	root := t.TempDir()
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	_, err := server.HandlePrivateRPC(context.Background(), "bridge/profile/set", json.RawMessage(`{"profile":"manual-only"}`))
	if !errors.Is(err, privateipc.ErrMethodNotFound) {
		t.Fatalf("retired RPC accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, feishu.SettingsFilename)); !os.IsNotExist(err) {
		t.Fatalf("retired RPC wrote settings: %v", err)
	}
}

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

func TestBridgeSettingsWritePreservesLegacyRoleAndReturnsStoredState(t *testing.T) {
	root := t.TempDir()
	settings := feishu.DefaultSettings()
	settings.Profile = "manual-only"
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, feishu.SettingsFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	server := newTestBridgeRPCServer(root, settings)
	settings.Profile = "primary"
	settings.Group.Enabled = true
	request, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.SettingsWrite, request)
	if err != nil {
		t.Fatal(err)
	}
	stored := result.(feishu.Settings)
	if stored.Profile != "manual-only" || !stored.Group.Enabled || server.settings != stored {
		t.Fatalf("write promoted the retired role: %+v", result)
	}
	loaded, err := feishu.NewSettingsStore(root).Load()
	if err != nil || loaded != stored {
		t.Fatalf("persisted settings diverged: %+v %v", loaded, err)
	}
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
	if snapshot.Capabilities["codexAppServer"].State != "" || snapshot.Capabilities["ksfContext"].State != "" {
		t.Fatalf("Core capabilities = %#v", snapshot.Capabilities)
	}
}

func TestBridgeHandshakeRejectsWrongProtocol(t *testing.T) {
	server := newTestBridgeRPCServer(t.TempDir(), feishu.DefaultSettings())
	params, _ := json.Marshal(feishuprotocol.InitializeRequest{Protocol: "wrong"})
	if _, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodBridgeInitialize, params); err == nil {
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
		{name: "typed request", method: feishuprotocol.MethodBridgeInitialize, params: json.RawMessage(`{"protocol":"ksfassistant-feishu-v2","unexpected":true}`)},
		{name: "parameterless method", method: feishuprotocol.MethodBridgeSnapshotRead, params: json.RawMessage(`{"unexpected":true}`)},
		{name: "malformed optional auth value", method: feishuprotocol.MethodAuthFinish, params: json.RawMessage(`{"deviceCode":"code","unexpected":true}`)},
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
	policyValue, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodPolicyRead, nil)
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
	value, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodOperationPrepare, params)
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
	value, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodOperationPrepare, params)
	if err != nil {
		t.Fatal(err)
	}
	prepared, ok := value.(feishu.PreparedOperation)
	if !ok || prepared.Operation.Status != feishu.OperationAwaitingConfirmation || prepared.Challenge == "" {
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
	value, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodOperationPrepare, params)
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			settings := feishu.DefaultSettings()
			settings.Actionbox.Enabled = true
			settings.Actionbox.DryRun = false
			settings.Outbound.Enabled = true
			settings.Outbound.DryRun = false
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
			value, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodOperationPrepare, params)
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

func actionboxWakeHandler(ctx context.Context, service *feishu.CapabilityService) func() {
	return func() { _ = service.ProcessActions(ctx) }
}
func TestBridgeV2HandshakeUsesSharedStrictDTO(t *testing.T) {
	server := newTestBridgeRPCServer(t.TempDir(), feishu.DefaultSettings())
	request, _ := json.Marshal(feishuprotocol.InitializeRequest{Protocol: feishuprotocol.Protocol})
	result, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.Initialize, request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	var decoded feishuprotocol.InitializeResult
	if err := privateipc.DecodeStrict(encoded, &decoded, true); err != nil {
		t.Fatal(err)
	}
	if decoded.Protocol != feishuprotocol.Protocol || decoded.Version != version {
		t.Fatal(decoded)
	}
	for _, method := range []string{"core/codex/control", "core/codex/projection/read", "core/ksf/context/read", "bridge/taskLink/create", "bridge/taskLink/inputOutcome"} {
		if _, err := server.HandlePrivateRPC(context.Background(), method, json.RawMessage("{}")); !errors.Is(err, privateipc.ErrMethodNotFound) {
			t.Fatalf("%s: %v", method, err)
		}
	}
}

func TestTransportConfirmationRetainsChallengeWithoutPrivateInput(t *testing.T) {
	failure := transportRPCError(&feishu.TransportAuthorizationError{Prepared: feishu.PreparedOperation{Operation: feishu.OperationView{ID: "OP-20260905000000-ABCDEF12", Status: feishu.OperationAwaitingConfirmation}, Challenge: "isolated-confirmation-fixture"}})
	var privateError *privateipc.RPCError
	if !errors.As(failure, &privateError) || privateError.Code != -32063 {
		t.Fatalf("confirmation became opaque error: %v", failure)
	}
	encoded, err := json.Marshal(privateError)
	if err != nil {
		t.Fatal(err)
	}
	var decoded privateipc.RPCError
	if err := privateipc.DecodeStrict(encoded, &decoded, true); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Status     string               `json:"status"`
		Operation  feishu.OperationView `json:"operation"`
		Challenge  string               `json:"challenge"`
		Submitted  bool                 `json:"submitted"`
		NextAction string               `json:"nextAction"`
	}
	if err := privateipc.DecodeStrict(decoded.Data, &data, true); err != nil {
		t.Fatal(err)
	}
	if data.Challenge != "isolated-confirmation-fixture" || data.Submitted || data.Status != "authorization_required" || data.NextAction != "confirm" {
		t.Fatalf("confirmation contract lost: %+v", data)
	}
}

func TestTaskCardWriteReadinessReflectsRuntimeSettings(t *testing.T) {
	for _, tc := range []struct {
		enabled, dryRun bool
		blocker         string
	}{
		{false, false, ""}, {true, true, ""}, {true, false, ""},
	} {
		settings := feishu.DefaultSettings()
		settings.Actionbox.Enabled, settings.Actionbox.DryRun = tc.enabled, tc.dryRun
		server := newTestBridgeRPCServer(t.TempDir(), settings)
		t.Setenv("LARK_CLI_BIN", filepath.Join(t.TempDir(), "missing"))
		snapshot := server.snapshot(context.Background())
		found := slices.Contains(snapshot.ReadinessBlockers, "taskCardWriteDisabled") || slices.Contains(snapshot.ReadinessBlockers, "taskCardWriteDryRun")
		if found != (tc.blocker != "") || (tc.blocker != "" && !slices.Contains(snapshot.ReadinessBlockers, tc.blocker)) {
			t.Fatalf("enabled=%v dryRun=%v: %v", tc.enabled, tc.dryRun, snapshot.ReadinessBlockers)
		}
	}
}
