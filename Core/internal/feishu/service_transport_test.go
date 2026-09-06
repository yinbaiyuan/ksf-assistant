package feishu

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

type transportClientFixture struct {
	calls   atomic.Int32
	err     error
	gate    <-chan struct{}
	started chan struct{}
}

func (client *transportClientFixture) VerifyBotMessage(ctx context.Context, _ MessageTarget, _ string) error {
	return ctx.Err()
}

func (client *transportClientFixture) Send(ctx context.Context, _ MessageTarget, _ string, _ string, id string) (string, error) {
	client.calls.Add(1)
	if client.started != nil {
		client.started <- struct{}{}
	}
	if client.gate != nil {
		select {
		case <-client.gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "om_" + id, client.err
}
func (client *transportClientFixture) Reply(ctx context.Context, _ string, format, content, id string) (string, error) {
	return client.Send(ctx, MessageTarget{}, format, content, id)
}
func (client *transportClientFixture) PatchCard(ctx context.Context, _ string, _ string) error {
	_, err := client.Send(ctx, MessageTarget{}, "card", "", "patch")
	return err
}

func newTransportFixture(t *testing.T, client ServiceMessageClient) (string, *ServiceTransport) {
	t.Helper()
	root := t.TempDir()
	settings := enableCapabilityWrites(t, root)
	settings.Outbound.Enabled, settings.Outbound.DryRun = true, false
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	config := DefaultClientConfig()
	config.MessageTargets["fixture"] = MessageTarget{Type: "open_id", ID: "ou_fixture"}
	config.DirectAllowedAliases = []string{"fixture"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	return root, NewServiceTransport(root, client)
}

func confirmTransportSend(t *testing.T, transport *ServiceTransport, request feishuprotocol.MessageRequest) string {
	t.Helper()
	_, err := transport.Message(context.Background(), false, request)
	var authorization *TransportAuthorizationError
	if !errors.As(err, &authorization) || authorization.Prepared.Challenge == "" {
		t.Fatalf("authorization=%#v err=%v", authorization, err)
	}
	service := NewCapabilityService(transport.root, &recordingCapabilityServiceExecutor{}, nil)
	service.SetMessageTransport(transport)
	prepared, err := service.Confirm(context.Background(), authorization.Prepared.Operation.ID, authorization.Prepared.Challenge)
	if err != nil || !prepared.Submitted || prepared.Operation.Status != OperationSucceeded {
		t.Fatalf("confirmation=%#v err=%v", prepared, err)
	}
	return prepared.Operation.ID
}

func TestServiceTransportSendConfirmReplayAndInputBinding(t *testing.T) {
	client := &transportClientFixture{}
	root, transport := newTransportFixture(t, client)
	request := feishuprotocol.MessageRequest{TargetType: "open_id", TargetID: "ou_fixture", Format: "card", Content: `{"elements":[]}`, IdempotencyKey: "fixture-send"}
	operationID := confirmTransportSend(t, transport, request)
	result, err := transport.Message(context.Background(), false, request)
	if err != nil || result.MessageID == "" || client.calls.Load() != 1 {
		t.Fatalf("result=%#v calls=%d err=%v", result, client.calls.Load(), err)
	}
	restarted := NewServiceTransport(root, client)
	replayed, err := restarted.Message(context.Background(), false, request)
	if err != nil || replayed != result || client.calls.Load() != 1 {
		t.Fatalf("replay=%#v calls=%d err=%v", replayed, client.calls.Load(), err)
	}
	binding, err := restarted.readBinding(result.MessageID)
	if err != nil || !binding.Writable || binding.OperationID != operationID {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	request.Content = `{"elements":[{"tag":"hr"}]}`
	if _, err := restarted.Message(context.Background(), false, request); !errors.Is(err, ErrOperationRequestMismatch) {
		t.Fatal(err)
	}
	if client.calls.Load() != 1 {
		t.Fatal("changed input sent")
	}
}

func TestServiceTransportReplyAndPatchUseFixedProfileAndRealPolicies(t *testing.T) {
	client := &transportClientFixture{}
	root, transport := newTransportFixture(t, client)
	inbound := InboundMessage{MessageID: "om_inbound", ChatType: "p2p", SenderOpenID: "ou_fixture"}
	if err := transport.BindInbound(inbound); err != nil {
		t.Fatal(err)
	}
	request := feishuprotocol.MessageRequest{MessageID: inbound.MessageID, Format: "card", Content: `{"elements":[]}`, IdempotencyKey: "fixture-reply"}
	result, err := transport.Message(context.Background(), true, request)
	if err != nil {
		t.Fatal(err)
	}
	patch := feishuprotocol.CardRequest{MessageID: result.MessageID, Content: `{"elements":[{"tag":"hr"}]}`}
	if err := transport.Patch(context.Background(), patch); err != nil {
		t.Fatal(err)
	}
	if err := NewServiceTransport(root, client).Patch(context.Background(), patch); err != nil {
		t.Fatal(err)
	}
	if client.calls.Load() != 2 {
		t.Fatalf("calls=%d", client.calls.Load())
	}
	policyStore := NewCapabilityPolicyStore(root)
	policy, err := policyStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides["im.message.edit"] = CapabilityDisabled
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	patch.Content = `{"elements":[{"tag":"markdown","content":"new"}]}`
	if err := transport.Patch(context.Background(), patch); err == nil {
		t.Fatal("disabled edit executed")
	}
	if client.calls.Load() != 2 {
		t.Fatal("disabled edit called SDK")
	}
}

func TestServiceTransportTimeoutCannotReplayAfterRestart(t *testing.T) {
	client := &transportClientFixture{err: context.DeadlineExceeded}
	root, transport := newTransportFixture(t, client)
	request := feishuprotocol.MessageRequest{TargetType: "open_id", TargetID: "ou_fixture", Format: "text", Content: "fixture", IdempotencyKey: "timeout-send"}
	_, prepareErr := transport.Message(context.Background(), false, request)
	var authorization *TransportAuthorizationError
	if !errors.As(prepareErr, &authorization) {
		t.Fatal(prepareErr)
	}
	operationID := authorization.Prepared.Operation.ID
	service := NewCapabilityService(root, &recordingCapabilityServiceExecutor{}, nil)
	service.SetMessageTransport(transport)
	if _, err := service.Confirm(context.Background(), operationID, authorization.Prepared.Challenge); err == nil {
		t.Fatal("confirmation hid execution timeout")
	}
	if _, err := transport.Message(context.Background(), false, request); err == nil {
		t.Fatal("timeout succeeded")
	}
	if _, err := NewServiceTransport(root, client).Message(context.Background(), false, request); err == nil {
		t.Fatal("unknown replayed")
	}
	view, err := NewOperationService(root, NewCapabilityPolicyStore(root), nil).Status(operationID)
	if err != nil || view.Status != OperationOutcomeUnknown || client.calls.Load() != 1 {
		t.Fatalf("view=%#v calls=%d err=%v", view, client.calls.Load(), err)
	}
}

func TestServiceTransportParentSenderInheritsOperationWithoutNestedClaim(t *testing.T) {
	client := &transportClientFixture{}
	root, transport := newTransportFixture(t, client)
	gate := make(chan struct{})
	close(gate)
	service, _ := reviewSDKService(t, root, gate, time.Second, 1)
	config := DefaultClientConfig()
	config.MessageTargets["fixture"] = MessageTarget{Type: "open_id", ID: "ou_fixture_0"}
	config.DirectAllowedAliases = []string{"fixture"}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	scheduler := NewWorkScheduler(root)
	scheduler.RegisterCapabilityService(service)
	scheduler.RegisterDocbox(NewDocbox(root), CapabilityExecutor{}, false)
	scheduler.RegisterOutbox(NewOutbox(root), transport, false)
	if err := scheduler.dispatchAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	reviewDrain(t, scheduler, 1)
	if client.calls.Load() != 1 {
		t.Fatalf("calls=%d", client.calls.Load())
	}
	reviewAssertNoChildWork(t, root, "outbox")
}

func TestServiceTransportSerializesSameTargetAcrossInstances(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := &transportClientFixture{gate: release, started: make(chan struct{}, 2)}
	root, transport := newTransportFixture(t, client)
	if err := transport.BindInbound(InboundMessage{MessageID: "om_inbound", ChatType: "p2p", SenderOpenID: "ou_fixture"}); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, key := range []string{"reply-1", "reply-2"} {
		workers.Add(1)
		go func(key string) {
			defer workers.Done()
			_, err := NewServiceTransport(root, client).Message(ctx, true, feishuprotocol.MessageRequest{MessageID: "om_inbound", Format: "text", Content: "fixture", IdempotencyKey: key})
			errorsCh <- err
		}(key)
	}
	select {
	case <-client.started:
	case err := <-errorsCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("first transport did not start")
	}
	select {
	case <-client.started:
		t.Fatal("same target executed concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("transport workers did not finish")
	}
	for index := 0; index < 2; index++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceTransportRestoresCoreCardsOnceWithoutOverwritingNewBindings(t *testing.T) {
	client := &transportClientFixture{}
	root, transport := newTransportFixture(t, client)
	target := MessageTarget{Type: "open_id", ID: "ou_fixture"}
	if err := transport.RestoreCardBinding(context.Background(), target, "om_legacy"); err != nil {
		t.Fatal(err)
	}
	if err := NewServiceTransport(root, client).RestoreCardBinding(context.Background(), target, "om_legacy"); err != nil {
		t.Fatal(err)
	}
	if client.calls.Load() != 0 {
		t.Fatal("restoration sent a message")
	}
	if err := transport.Patch(context.Background(), feishuprotocol.CardRequest{MessageID: "om_legacy", Content: `{"elements":[]}`}); err != nil {
		t.Fatal(err)
	}
	binding, err := transport.readBinding("om_legacy")
	if err != nil || binding.OperationID == "" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	if err := transport.RestoreCardBinding(context.Background(), target, "om_legacy"); err != nil {
		t.Fatal(err)
	}
	after, err := transport.readBinding("om_legacy")
	if err != nil || after != binding {
		t.Fatalf("binding overwritten: %#v %#v %v", binding, after, err)
	}
	if err := transport.RestoreCardBinding(context.Background(), MessageTarget{Type: "open_id", ID: "ou_other"}, "om_legacy"); err == nil {
		t.Fatal("unapproved target restored")
	}
}

type rejectingMessageVerifier struct{ transportClientFixture }

func (*rejectingMessageVerifier) VerifyBotMessage(context.Context, MessageTarget, string) error {
	return errors.New("message_not_bot_owned")
}

func TestServiceTransportRestorationOwnershipFailureIsClosed(t *testing.T) {
	_, transport := newTransportFixture(t, &rejectingMessageVerifier{})
	if err := transport.RestoreCardBinding(context.Background(), MessageTarget{Type: "open_id", ID: "ou_fixture"}, "om_foreign"); err == nil {
		t.Fatal("foreign message restored")
	}
	if _, err := transport.readBinding("om_foreign"); err == nil {
		t.Fatal("rejected restoration wrote binding")
	}
}

func TestServiceTransportAndSchedulerShareGlobalFourSlots(t *testing.T) {
	clientGate := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(clientGate) })
	client := &transportClientFixture{gate: clientGate, started: make(chan struct{}, 5)}
	root, transport := newTransportFixture(t, client)
	config := DefaultClientConfig()
	for index := 0; index < 5; index++ {
		alias := fmt.Sprintf("fixture-%d", index)
		config.MessageTargets[alias] = MessageTarget{Type: "open_id", ID: fmt.Sprintf("ou_fixture_%d", index)}
		config.DirectAllowedAliases = append(config.DirectAllowedAliases, alias)
	}
	if err := NewClientConfigStore(root).Save(config); err != nil {
		t.Fatal(err)
	}
	parentGate := make(chan struct{})
	close(parentGate)
	service, _ := reviewSDKService(t, root, parentGate, 2*time.Second, 4)
	scheduler := NewWorkScheduler(root)
	scheduler.RegisterCapabilityService(service)
	scheduler.RegisterDocbox(NewDocbox(root), CapabilityExecutor{}, false)
	scheduler.RegisterOutbox(NewOutbox(root), transport, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := scheduler.dispatchAvailable(ctx); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		select {
		case <-client.started:
		case <-ctx.Done():
			t.Fatal("four scheduled SDK writes did not start")
		}
	}
	if err := transport.BindInbound(InboundMessage{MessageID: "om_fifth", ChatType: "p2p", SenderOpenID: "ou_fixture_4"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := transport.Message(ctx, true, feishuprotocol.MessageRequest{MessageID: "om_fifth", Format: "text", Content: "fixture", IdempotencyKey: "fifth-reply"})
		done <- err
	}()
	select {
	case <-client.started:
		t.Fatal("synchronous send exceeded shared capacity")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(clientGate) })
	reviewDrain(t, scheduler, 4)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("fifth send did not resume")
	}
	if client.calls.Load() != 5 {
		t.Fatal(client.calls.Load())
	}
}
