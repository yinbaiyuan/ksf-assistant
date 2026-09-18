package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"ksfassistant/core/internal/privateipc"
)

func TestOperatorBindingRPCRequiresExpectedContextAndReportsConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LARKSUITE_CLI_CONFIG_DIR", filepath.Join(root, "missing-config"))
	t.Setenv("LARK_CLI_BIN", filepath.Join(root, "must-not-execute"))
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	for _, params := range []string{`{}`, `{"identityRevision":"fixture"}`, `{"applicationId":"cli_fixture","openId":"ou_untrusted"}`} {
		_, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodAuthEnsureUser, json.RawMessage(params))
		var rpcErr *privateipc.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
			t.Fatalf("unscoped binding accepted: %v", err)
		}
	}
	params, err := json.Marshal(feishu.OperatorBindingExpectation{ApplicationID: "cli_fixture", IdentityRevision: strings.Repeat("a", 64), ContextRevision: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodAuthEnsureUser, params)
	var rpcErr *privateipc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32065 {
		t.Fatalf("context conflict code lost: %v", err)
	}
	if _, err := os.Stat(feishu.NewClientConfigStore(root).Path()); !os.IsNotExist(err) {
		t.Fatal("conflict created operator configuration")
	}
}

func TestSettingsCASRPCIsStrictAndReportsStableConflictCode(t *testing.T) {
	root := t.TempDir()
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	for _, raw := range []string{`{}`, `{"expected":null,"settings":{}}`, `{"expected":{},"settings":{},"extra":true}`, `{"expected":{"extra":true},"settings":{}}`} {
		_, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodSettingsCompareAndSwap, json.RawMessage(raw))
		var rpcErr *privateipc.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
			t.Fatalf("invalid CAS admitted: %s %v", raw, err)
		}
	}
	expected := feishu.DefaultSettings()
	next := expected
	next.Group.Enabled = true
	params, err := json.Marshal(map[string]any{"expected": expected, "settings": next})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodSettingsCompareAndSwap, params); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, feishu.SettingsFilename)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodSettingsCompareAndSwap, params)
	var rpcErr *privateipc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32064 {
		t.Fatalf("CAS conflict code changed: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || !server.settings.Group.Enabled {
		t.Fatal("CAS conflict changed disk or runtime settings")
	}
}

func TestAppConfigurationFinishRPCRequiresLiveSession(t *testing.T) {
	root := t.TempDir()
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	result, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodAuthConfigFinish, json.RawMessage(`{}`))
	if err == nil || result != nil || errors.Is(err, privateipc.ErrMethodNotFound) {
		t.Fatalf("missing creation session accepted or RPC missing: %+v %v", result, err)
	}
	result, err = server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodAuthCancel, json.RawMessage(`{}`))
	var rpcErr *privateipc.RPCError
	if result != nil || !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
		t.Fatalf("unscoped cancellation accepted: %+v %v", result, err)
	}
	if _, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodAuthConfigFinish, json.RawMessage(`{}`)); err == nil {
		t.Fatal("cancelled session claimed success")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("session finish/cancel wrote durable state: %+v %v", entries, err)
	}
}

func TestAppConfigurationRPCRejectsUnexpectedParams(t *testing.T) {
	server := newTestBridgeRPCServer(t.TempDir(), feishu.DefaultSettings())
	for _, method := range []string{feishuprotocol.MethodAuthConfigFinish, feishuprotocol.MethodAuthCancel, feishuprotocol.MethodConfigurationEvidence, feishuprotocol.MethodConfigurationFlow} {
		for _, params := range []string{`{"createNew":true}`, `{"verificationUrl":"https://example.test"}`, `[]`} {
			_, err := server.HandlePrivateRPC(context.Background(), method, json.RawMessage(params))
			var rpcErr *privateipc.RPCError
			if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
				t.Fatalf("%s accepted %s: %v", method, params, err)
			}
		}
	}
}

func TestAppConfigurationRPCHonorsCancelledStart(t *testing.T) {
	root := t.TempDir()
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := server.HandlePrivateRPC(ctx, feishuprotocol.MethodAuthStart, json.RawMessage(`{"kind":"config","profile":"default","createNew":true}`))
	if result != nil || err == nil {
		t.Fatalf("cancelled native registration started: result=%+v err=%v", result, err)
	}
}

func TestSupplementalUserAuthorizationIsRetired(t *testing.T) {
	server := newTestBridgeRPCServer(t.TempDir(), feishu.DefaultSettings())
	result, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodAuthStart, json.RawMessage(`{"kind":"user","scope":"required"}`))
	var rpcErr *privateipc.RPCError
	if result != nil || !errors.As(err, &rpcErr) || rpcErr.Code != -32602 || rpcErr.Message != "supplemental_user_authorization_retired" {
		t.Fatalf("supplemental authorization was not retired: result=%+v err=%v", result, err)
	}
}

func TestConfigurationPrivateReadsAndScopedCancellation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LARKSUITE_CLI_CONFIG_DIR", root+"/missing-config")
	t.Setenv("LARK_CLI_BIN", root+"/must-not-execute")
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	result, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodConfigurationEvidence, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := result.(feishuprotocol.ConfigurationEvidence)
	if !ok || evidence.ApplicationState != "missing" || evidence.Auth != nil {
		t.Fatalf("missing app manufactured auth: %+v", result)
	}
	result, err = server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodConfigurationFlow, json.RawMessage(`{}`))
	flow, ok := result.(*feishuprotocol.ConfigurationFlow)
	if err != nil || !ok || flow != nil {
		t.Fatalf("flow read ran CLI or fabricated session: %+v %v", result, err)
	}
	for _, params := range []string{`{}`, `{"flowId":"fixture"}`, `{"flowId":"fixture","kind":"other"}`, `{"flowId":"fixture","kind":"user","extra":true}`} {
		_, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodConfigurationCancel, json.RawMessage(params))
		var rpcErr *privateipc.RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != -32602 {
			t.Fatalf("invalid cancellation %s: %v", params, err)
		}
	}
	for _, kind := range []string{"app", "user"} {
		_, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodConfigurationCancel, json.RawMessage(`{"flowId":"missing","kind":"`+kind+`"}`))
		if err == nil || errors.Is(err, privateipc.ErrMethodNotFound) {
			t.Fatalf("missing %s session cancellation: %v", kind, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read/cancel persisted state: %+v %v", entries, err)
	}
}

func TestUnscopedAndMismatchedCancellationPreserveLiveSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake process fixture")
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("LARKSUITE_CLI_CONFIG_DIR", filepath.Join(root, "config"))
	binary := filepath.Join(root, "fake-lark")
	contract, _ := feishu.RequiredPermissionScopes()
	scopes, _ := json.Marshal(map[string]any{"appId": "cli_fixture", "brand": "feishu", "tokenType": "user", "userScopes": contract.User})
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
if [ "$4" = scopes ]; then printf '%s' '`+string(scopes)+`'; exit; fi
case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":false}}}' ;;
login) printf '{"event":"device_authorization","verification_uri_complete":"https://accounts.feishu.cn/authorize","user_code":"FIXTURE"}\n'; while :; do sleep 0.02; done ;;
*) exit 1 ;;
esac`), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := feishu.CapabilityExecutor{Binary: binary, Profile: "default", DataRoot: root, WorkingDirectory: root}
	t.Cleanup(func() { feishu.CancelUserAuthFlow(root) })
	if _, err := feishu.StartUserAuth(context.Background(), runner, root, "required"); err != nil {
		t.Fatal(err)
	}
	before := feishu.ReadConfigurationFlow(root)
	if before == nil || before.State != "pending" {
		t.Fatalf("no pending fixture: %+v", before)
	}
	server := newTestBridgeRPCServer(root, feishu.DefaultSettings())
	for _, request := range []struct{ method, params string }{
		{feishuprotocol.MethodAuthCancel, `{}`},
		{feishuprotocol.MethodConfigurationCancel, `{"flowId":"different","kind":"user"}`},
		{feishuprotocol.MethodConfigurationCancel, `{"flowId":"` + before.ID + `","kind":"app"}`},
	} {
		if _, err := server.HandlePrivateRPC(context.Background(), request.method, json.RawMessage(request.params)); err == nil {
			t.Fatal("unscoped or mismatched cancellation accepted")
		}
		after := feishu.ReadConfigurationFlow(root)
		if after == nil || *after != *before {
			t.Fatalf("rejected cancellation changed flow: %+v -> %+v", before, after)
		}
	}
	result, err := server.HandlePrivateRPC(context.Background(), feishuprotocol.MethodConfigurationCancel, json.RawMessage(`{"flowId":"`+before.ID+`","kind":"user"}`))
	if err != nil || result.(feishuprotocol.ConfigurationFlow).State != "cancelled" {
		t.Fatalf("matching cancellation failed: %+v %v", result, err)
	}
}
