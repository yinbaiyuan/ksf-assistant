package feishu

import (
	"context"
	"errors"
	"testing"

	"ksfassistant/core/internal/feishuprotocol"
)

func prepareTransportConfirmation(t *testing.T, transport *ServiceTransport) PreparedOperation {
	t.Helper()
	_, err := transport.Message(context.Background(), false, feishuprotocol.MessageRequest{TargetType: "open_id", TargetID: "ou_fixture", Format: "text", Content: "fixture", IdempotencyKey: "confirmation-fixture"})
	var authorization *TransportAuthorizationError
	if !errors.As(err, &authorization) || authorization.Prepared.Challenge == "" {
		t.Fatalf("missing confirmation: %v", err)
	}
	return authorization.Prepared
}

func TestServiceMessageConfirmationRequiresTransportWithoutConsumingChallenge(t *testing.T) {
	client := &transportClientFixture{}
	root, transport := newTransportFixture(t, client)
	prepared := prepareTransportConfirmation(t, transport)
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	if _, err := service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge); err == nil {
		t.Fatal("missing transport accepted")
	}
	if view, err := service.Status(prepared.Operation.ID); err != nil || view.Status != OperationAwaitingConfirmation {
		t.Fatal("challenge consumed without executor")
	}
	service.SetMessageTransport(transport)
	result, err := service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge)
	if err != nil || result.Operation.ID != prepared.Operation.ID || result.Operation.Status != OperationSucceeded || result.Result["messageID"] == "" || client.calls.Load() != 1 {
		t.Fatalf("confirmed message not dispatched: %#v %v", result, err)
	}
	if _, err := service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge); err == nil || client.calls.Load() != 1 {
		t.Fatal("duplicate confirmation executed")
	}
}

func TestServiceMessageConfirmationRechecksRuntimeGates(t *testing.T) {
	for _, mode := range []string{"dry-run", "target-revoked", "policy-revoked"} {
		t.Run(mode, func(t *testing.T) {
			client := &transportClientFixture{}
			root, transport := newTransportFixture(t, client)
			prepared := prepareTransportConfirmation(t, transport)
			switch mode {
			case "dry-run":
				settings, _ := NewSettingsStore(root).Load()
				settings.Outbound.DryRun = true
				if err := NewSettingsStore(root).Save(settings); err != nil {
					t.Fatal(err)
				}
			case "target-revoked":
				config := DefaultClientConfig()
				if err := NewClientConfigStore(root).Save(config); err != nil {
					t.Fatal(err)
				}
			case "policy-revoked":
				store := NewCapabilityPolicyStore(root)
				policy, _ := store.Load()
				policy.CapabilityOverrides["im.sdk.message.send"] = CapabilityDisabled
				if _, err := store.Save(policy, policy.Revision); err != nil {
					t.Fatal(err)
				}
			}
			service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
			service.SetMessageTransport(transport)
			_, err := service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge)
			if mode != "dry-run" && err == nil || client.calls.Load() != 0 {
				t.Fatalf("revoked request executed: %v", err)
			}
		})
	}
}

func TestInterruptedConfirmedMessageRequiresReprepareWithoutReplay(t *testing.T) {
	client := &transportClientFixture{}
	root, transport := newTransportFixture(t, client)
	prepared := prepareTransportConfirmation(t, transport)
	operations := NewOperationService(root, NewCapabilityPolicyStore(root), nil)
	if _, err := operations.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge); err != nil {
		t.Fatal(err)
	}
	if err := operations.RecoverInterrupted(1000); err != nil {
		t.Fatal(err)
	}
	view, err := operations.Status(prepared.Operation.ID)
	if err != nil || view.Status != OperationFailed || view.NextAction != "reprepare_on_user_request" || client.calls.Load() != 0 {
		t.Fatalf("orphaned confirmation: %#v %v", view, err)
	}
	if _, err := transport.ResumeOperation(context.Background(), prepared.Operation.ID); err == nil || client.calls.Load() != 0 {
		t.Fatal("interrupted confirmation replayed")
	}
}
