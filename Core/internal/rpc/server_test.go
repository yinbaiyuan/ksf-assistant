package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codexusagebar/core/internal/service"
)

func TestServerPublishesVersionedInitializeContract(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var output bytes.Buffer
	server := New(service.New(), input, &output)
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
	if response.Result.Protocol != "codex-usage-core-v1" || response.Result.Version == "" {
		t.Fatalf("unexpected contract: %#v", response.Result)
	}
	if !response.Result.Capabilities["tokenHistory"] || !response.Result.Capabilities["tokenHistoryComparison"] || !response.Result.Capabilities["tokenCostEstimate"] || !response.Result.Capabilities["feishuTaskLinks"] {
		t.Fatalf("missing public capabilities: %#v", response.Result.Capabilities)
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
	if response.Result.DefaultPlanID != "openai:gpt-5.6-sol" || len(response.Result.Plans) != 8 || response.Result.Plans[7].ID != "custom:a" {
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
