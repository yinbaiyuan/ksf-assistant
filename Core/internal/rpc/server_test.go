package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ksfassistant/core/internal/integration"
	"ksfassistant/core/internal/service"
)

func TestEventProfileWriteRPCIsRetired(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"feishu/profile/set","params":{"profile":"manual-only"}}` + "\n")
	var output bytes.Buffer
	if err := New(&service.Service{}, input, &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "unknown method") || strings.Contains(output.String(), `"result"`) {
		t.Fatalf("retired setter reached service: %s", output.String())
	}
}

func TestServerPublishesVersionedInitializeContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", root)
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("KSF_ASSISTANT_FEISHU_BRIDGE", "")
	fakeCodex := filepath.Join(root, "unexecutable-codex-fixture")
	if err := os.WriteFile(fakeCodex, []byte("test-only: no process or credentials"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_BIN", fakeCodex)
	receipts := filepath.Join(root, "configuration-receipts-v1")
	if err := os.MkdirAll(receipts, 0o700); err != nil {
		t.Fatal(err)
	}
	receiptPath := func(id string) string {
		return filepath.Join(receipts, fmt.Sprintf("%x.json", sha256.Sum256([]byte(fmt.Sprintf("%q", id)))))
	}
	if err := os.WriteFile(receiptPath("legacy-create"), []byte(`{"schemaVersion":1,"requestId":"legacy-create","action":"create_app","outcome":"pending","stage":"submitted","message":"waiting","updatedAt":"2026-09-10T10:30:45Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath("later-logout"), []byte(`{"schemaVersion":1,"requestId":"later-logout","action":"logout","outcome":"completed","stage":"verified","message":"done","updatedAt":"2026-09-10T10:32:48Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var output bytes.Buffer
	core := service.New()
	t.Cleanup(core.Close)
	server := New(core, input, &output)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			Protocol     string          `json:"protocol"`
			Version      string          `json:"version"`
			Capabilities map[string]bool `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.Protocol != "ksf-assistant-core-v2" || response.Result.Version == "" {
		t.Fatalf("unexpected contract: %#v", response.Result)
	}
	if !response.Result.Capabilities["tokenHistory"] || !response.Result.Capabilities["tokenHistoryComparison"] || !response.Result.Capabilities["tokenCostEstimate"] || !response.Result.Capabilities["feishuTaskLinks"] || !response.Result.Capabilities["feishuCapabilityGovernance"] {
		t.Fatalf("missing public capabilities: %#v", response.Result.Capabilities)
	}
	var migrated struct {
		Outcome string `json:"outcome"`
		Stage   string `json:"stage"`
		Code    string `json:"code"`
	}
	bytes, err := os.ReadFile(receiptPath("legacy-create"))
	if err != nil || json.Unmarshal(bytes, &migrated) != nil || migrated.Outcome != "failed" || migrated.Stage != "verified" || migrated.Code != "cancelled_by_logout" {
		t.Fatalf("initialize did not migrate the legacy connection flow: %+v %v", migrated, err)
	}
}

func TestServerUpdatesPrivateHostIntegrationContext(t *testing.T) {
	dataRoot := t.TempDir()
	ksfRoot := t.TempDir()
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", dataRoot)
	input := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"integration/context/update","params":{"ksfRoot":` + fmt.Sprintf("%q", ksfRoot) + `}}` + "\n")
	var output bytes.Buffer
	if err := New(service.New(), input, &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"state":"ready"`) || !strings.Contains(output.String(), fmt.Sprintf("%q", ksfRoot)) {
		t.Fatalf("unexpected integration response: %s", output.String())
	}
	stored, err := os.ReadFile(filepath.Join(dataRoot, integration.HostContextFilename))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("threadId")) || bytes.Contains(stored, []byte("message")) {
		t.Fatalf("host context contains task data: %s", stored)
	}
}

func TestServerPublishesPricingCatalog(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"pricing/catalog/read","params":{"customPlans":[{"id":"custom:a","provider":"Team","model":"A","regularInputMicroUsdPerMillion":1000000,"cachedInputMicroUsdPerMillion":100000,"outputMicroUsdPerMillion":5000000}]}}` + "\n")
	var output bytes.Buffer
	if err := New(service.New(), input, &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			DefaultPlanID string `json:"defaultPlanId"`
			Plans         []struct {
				ID string `json:"id"`
			} `json:"plans"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.DefaultPlanID != "openai:gpt-6-astra" || len(response.Result.Plans) != 9 || response.Result.Plans[8].ID != "custom:a" {
		t.Fatalf("unexpected pricing catalog: %#v", response.Result)
	}
}

func TestServerRejectsUnknownMethods(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"private/raw","params":{}}` + "\n")
	var output bytes.Buffer
	if err := New(service.New(), input, &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "unknown method") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestServerPublishesFeishuGovernanceMethodsWithoutRawPassthrough(t *testing.T) {
	methods := []string{
		"feishu/operation/confirm", "feishu/operation/cancel",
		"feishu/operation/status", "feishu/policy/read", "feishu/policy/update",
	}
	for index, method := range methods {
		input := strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":{}}`+"\n", index+1, method))
		var output bytes.Buffer
		if err := New(service.New(), input, &output).Serve(context.Background()); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if strings.Contains(output.String(), "unknown method") {
			t.Fatalf("governance method was not published: %s", method)
		}
	}
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"feishu/openapi/raw","params":{}}` + "\n")
	var output bytes.Buffer
	if err := New(service.New(), input, &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "unknown method") {
		t.Fatalf("raw passthrough was admitted: %s", output.String())
	}
}

func TestServerSettingsRequireFeishuServiceAndNeverWriteOffline(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FEISHU_BRIDGE_DATA_DIR", root)
	t.Setenv("KSF_ASSISTANT_FEISHU_BRIDGE", "")
	t.Setenv("KSF_ASSISTANT_FEISHU_SERVICE_ROOT", "")
	input := strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"feishu/settings/update","params":{"version":1,"profile":"primary","group":{"enabled":false},"outbound":{"enabled":true,"dryRun":true},"directory":{"enabled":false},"groupDirectory":{"enabled":false},"docbox":{"enabled":false,"dryRun":true},"actionbox":{"enabled":false,"dryRun":true},"codex":{"defaultThreadTitle":"飞书默认对话"}}}` + "\n")
	var output bytes.Buffer
	if err := New(service.New(), input, &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(output.String()), "appsecret") || strings.Contains(strings.ToLower(output.String()), "access_token") {
		t.Fatalf("settings response leaked a secret field: %s", output.String())
	}
	var response struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || !strings.Contains(response.Error.Message, "configuration_legacy_mutation_disabled") {
		t.Fatalf("expected retired mutation response: %s", output.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("offline settings mutated data root: %v %v", entries, err)
	}
}
