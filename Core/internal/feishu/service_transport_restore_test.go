package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

type restorationSDKFixture struct {
	response map[string]any
	status   int
	calls    atomic.Int32
	onGet    func()
}

func (fixture *restorationSDKFixture) Do(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	var payload map[string]any
	switch {
	case strings.HasPrefix(request.URL.Path, "/open-apis/auth/"):
		payload = map[string]any{"code": 0, "tenant_access_token": "fixture-only-token", "expire": 7200}
	case request.Method == http.MethodGet && request.URL.Path == "/open-apis/im/v1/messages/om_legacy":
		fixture.calls.Add(1)
		if fixture.onGet != nil {
			fixture.onGet()
		}
		payload = fixture.response
	default:
		return nil, errors.New("unexpected_sdk_request")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if request.Method == http.MethodGet && fixture.status != 0 {
		status = fixture.status
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
}

func TestCardRestorationClassifiesHTTPFailures(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusForbidden} {
		fixture := &restorationSDKFixture{status: status, response: map[string]any{"code": 999, "msg": "fixture failure"}}
		client := newRestorationSDK(t, fixture, "cli_fixture_restore")
		err := client.VerifyBotMessage(context.Background(), MessageTarget{Type: "open_id", ID: "ou_fixture"}, "om_legacy")
		if err == nil || CardRestorationRetryable(err) != (status != http.StatusForbidden) {
			t.Fatalf("status=%d err=%v", status, err)
		}
	}
}

func restorationSDKMessage() map[string]any {
	return map[string]any{"message_id": "om_legacy", "msg_type": "interactive", "chat_id": "oc_fixture", "deleted": false,
		"sender": map[string]any{"id": "cli_fixture_restore", "id_type": "app_id", "sender_type": "app"}}
}

func newRestorationSDK(t *testing.T, fixture *restorationSDKFixture, appID string) *OfficialMessageClient {
	t.Helper()
	client, err := NewOfficialMessageClient(appID, "fixture-secret-not-credentials", lark.WithHttpClient(fixture))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestOfficialMessageRestoreVerifiesSDKBotCard(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		allow  bool
	}{
		{name: "bot-card", allow: true},
		{name: "user-root", mutate: func(message map[string]any) { message["sender"].(map[string]any)["sender_type"] = "user" }},
		{name: "other-app", mutate: func(message map[string]any) { message["sender"].(map[string]any)["id"] = "cli_other" }},
		{name: "wrong-id-type", mutate: func(message map[string]any) { message["sender"].(map[string]any)["id_type"] = "open_id" }},
		{name: "missing-sender", mutate: func(message map[string]any) { delete(message, "sender") }},
		{name: "not-card", mutate: func(message map[string]any) { message["msg_type"] = "text" }},
		{name: "deleted", mutate: func(message map[string]any) { message["deleted"] = true }},
		{name: "wrong-message", mutate: func(message map[string]any) { message["message_id"] = "om_other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := restorationSDKMessage()
			if test.mutate != nil {
				test.mutate(message)
			}
			fixture := &restorationSDKFixture{response: map[string]any{"code": 0, "data": map[string]any{"items": []any{message}}}}
			client := newRestorationSDK(t, fixture, "cli_fixture_restore")
			_, transport := newTransportFixture(t, client)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := transport.RestoreCardBinding(ctx, MessageTarget{Type: "open_id", ID: "ou_fixture"}, "om_legacy")
			if (err == nil) != test.allow || fixture.calls.Load() != 1 {
				t.Fatalf("allow=%v calls=%d err=%v", test.allow, fixture.calls.Load(), err)
			}
			binding, readErr := transport.readBinding("om_legacy")
			if test.allow && (readErr != nil || !binding.Writable || !binding.Restored) {
				t.Fatalf("binding=%#v err=%v", binding, readErr)
			}
			if !test.allow && readErr == nil {
				t.Fatal("unverified root persisted")
			}
		})
	}
}

func TestOfficialMessageRestoreMarkerAndAppRotation(t *testing.T) {
	fixture := &restorationSDKFixture{response: map[string]any{"code": 0, "data": map[string]any{"items": []any{restorationSDKMessage()}}}}
	client := newRestorationSDK(t, fixture, "cli_fixture_restore")
	root, transport := newTransportFixture(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	target := MessageTarget{Type: "open_id", ID: "ou_fixture"}
	for attempt := 0; attempt < 2; attempt++ {
		if err := transport.RestoreCardBinding(ctx, target, "om_legacy"); err != nil {
			t.Fatal(err)
		}
		transport = NewServiceTransport(root, client)
	}
	if fixture.calls.Load() != 1 {
		t.Fatal("completed marker did not prevent repeated SDK reads")
	}
	rotated := NewServiceTransport(root, newRestorationSDK(t, fixture, "cli_rotated"))
	if err := rotated.RestoreCardBinding(ctx, target, "om_legacy"); err == nil || fixture.calls.Load() != 2 {
		t.Fatalf("app rotation reused stale ownership: %v", err)
	}
	if err := client.VerifyBotMessage(ctx, MessageTarget{Type: "chat_id", ID: "oc_wrong"}, "om_legacy"); !errors.Is(err, ErrOperationRequestMismatch) {
		t.Fatalf("wrong chat accepted: %v", err)
	}
}

func TestServiceTransportRestoreRejectsNilVerifierEvenWithMarker(t *testing.T) {
	root, transport := newTransportFixture(t, &transportClientFixture{})
	target := MessageTarget{Type: "open_id", ID: "ou_fixture"}
	if err := transport.RestoreCardBinding(context.Background(), target, "om_legacy"); err != nil {
		t.Fatal(err)
	}
	for _, client := range []ServiceMessageClient{nil, (*OfficialMessageClient)(nil), &OfficialMessageClient{}} {
		if err := NewServiceTransport(root, client).RestoreCardBinding(context.Background(), target, "om_legacy"); err == nil {
			t.Fatal("nil verifier accepted existing marker")
		}
	}
}

func TestServiceTransportRestoreRechecksTargetAfterSDK(t *testing.T) {
	fixture := &restorationSDKFixture{response: map[string]any{"code": 0, "data": map[string]any{"items": []any{restorationSDKMessage()}}}}
	root, transport := newTransportFixture(t, newRestorationSDK(t, fixture, "cli_fixture_restore"))
	fixture.onGet = func() {
		if err := NewClientConfigStore(root).Save(DefaultClientConfig()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := transport.RestoreCardBinding(ctx, MessageTarget{Type: "open_id", ID: "ou_fixture"}, "om_legacy"); err == nil {
		t.Fatal("revoked target restored")
	}
	if _, err := transport.readBinding("om_legacy"); err == nil {
		t.Fatal("revoked target binding persisted")
	}
}
