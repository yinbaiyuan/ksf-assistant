package integration

import (
	"context"
	"errors"
	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishuprotocol"
	"testing"
)

type failingCardPort struct {
	fakeFeishuPort
	fail bool
}

func (port *failingCardPort) Send(ctx context.Context, target MessageTarget, format, content, key string) (string, error) {
	if port.fail {
		return "", errors.New("temporary send failure")
	}
	return port.fakeFeishuPort.Send(ctx, target, format, content, key)
}

func TestFailedCardDeliveryIsNotPresentedAsConnectedAndCanRetry(t *testing.T) {
	messages := &failingCardPort{fail: true}
	runtime, err := NewRuntime(t.TempDir(), messages, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	request := CreateTaskLinkRequest{ThreadID: "delivery-test", Title: "test", TargetAlias: "me"}
	if _, err := runtime.CreateTaskLink(context.Background(), request); err == nil {
		t.Fatal("send should fail")
	}
	file, err := runtime.Store().Load()
	if err != nil || len(file.Links) != 1 {
		t.Fatalf("durable retry identity missing: %v", err)
	}
	link := PublicLinks(file.Links)[0]
	if link.LinkState != "pending" {
		t.Fatalf("undelivered card presented as %s", link.LinkState)
	}
	id := file.Links[0].ID
	messages.fail = false
	link, err = runtime.CreateTaskLink(context.Background(), request)
	if err != nil || link.LinkState != "active" {
		t.Fatalf("retry did not connect: %#v %v", link, err)
	}
	file, _ = runtime.Store().Load()
	if len(file.Links) != 1 || file.Links[0].ID != id {
		t.Fatal("retry replaced durable idempotency identity")
	}
}

type governedCardPort struct {
	fakeFeishuPort
	transport *feishu.ServiceTransport
}

func (port *governedCardPort) Send(ctx context.Context, target MessageTarget, format, content, key string) (string, error) {
	result, err := port.transport.Message(ctx, false, feishuprotocol.MessageRequest{TargetType: target.Type, TargetID: target.ID, Format: format, Content: content, IdempotencyKey: key})
	return result.MessageID, err
}

func TestTaskCardConfirmationResumesTheOriginalGovernedDelivery(t *testing.T) {
	root := t.TempDir()
	settings := feishu.DefaultSettings()
	settings.Actionbox.Enabled, settings.Actionbox.DryRun = true, false
	settings.Outbound.Enabled, settings.Outbound.DryRun = true, false
	if err := feishu.NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	config := feishu.DefaultClientConfig()
	config.MessageTargets["me"] = feishu.MessageTarget{Type: "open_id", ID: "user-1"}
	config.DirectAllowedAliases = []string{"me"}
	if err := feishu.NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	remote := &governedCardClient{}
	transport := feishu.NewServiceTransport(root, remote)
	runtime, err := NewRuntime(root, &governedCardPort{transport: transport}, &fakeCorePort{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	request := CreateTaskLinkRequest{ThreadID: "confirm-task", Title: "confirm", ProjectName: "project", TargetAlias: "me"}
	_, err = runtime.CreateTaskLink(context.Background(), request)
	var authorization *feishu.TransportAuthorizationError
	if !errors.As(err, &authorization) || remote.calls.Load() != 0 {
		t.Fatalf("expected pending confirmation: %v", err)
	}
	file, _ := runtime.Store().Load()
	if PublicLinks(file.Links)[0].LinkState != "pending" {
		t.Fatal("unsent link presented as active")
	}
	capability := feishu.NewCapabilityService(root, nil, nil)
	capability.SetMessageTransport(transport)
	if _, err := capability.Confirm(context.Background(), authorization.Prepared.Operation.ID, authorization.Prepared.Challenge); err != nil {
		t.Fatal(err)
	}
	link, err := runtime.CreateTaskLink(context.Background(), request)
	if err != nil || link.LinkState != "active" || remote.calls.Load() != 1 {
		t.Fatalf("confirmation/retry failed or duplicated send: %v calls=%d state=%s", err, remote.calls.Load(), link.LinkState)
	}
	file, _ = runtime.Store().Load()
	if len(file.Links) != 1 || file.Links[0].RootMessageID != "card-1" {
		t.Fatal("confirmed message was not attached to original link")
	}
}

type governedCardClient struct{ fakeFeishuPort }

func (client *governedCardClient) Send(ctx context.Context, target feishu.MessageTarget, format, content, key string) (string, error) {
	return client.fakeFeishuPort.Send(ctx, MessageTarget{Type: target.Type, ID: target.ID}, format, content, key)
}
