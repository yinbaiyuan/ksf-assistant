package feishu

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestOpenAPIErrorEnvelopeIsConfirmedRejection(t *testing.T) {
	transport, err := NewOpenAPIMessageTransport(OfficialCredentials{AppID: "app", AppSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	transport.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"code":230099,"msg":"private remote detail"}`
		status := http.StatusBadRequest
		if strings.Contains(request.URL.Path, "tenant_access_token") {
			body = `{"code":0,"tenant_access_token":"token","expire":3600}`
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	_, err = transport.CallMessage(context.Background(), MessageCLIRequest{Resource: "messages", Method: "get", Params: map[string]string{"message_id": "om_test"}})
	var failure *CLIExecutionError
	if !errors.As(err, &failure) || failure.Outcome != "rejected" || !failure.Started || failure.Structured["code"] != float64(230099) {
		t.Fatalf("unexpected failure: %#v", err)
	}
	if CapabilityOutcomeUncertain(err) || isUncertainExecutionError(err) || failure.Structured["message"] != nil {
		t.Fatalf("confirmed rejection was uncertain or leaked details: %#v", failure)
	}
}

func TestOfficialOpenAPIClientKeepsExplicitNativeCardRoot(t *testing.T) {
	root := t.TempDir()
	transport, err := NewOpenAPIMessageTransport(OfficialCredentials{AppID: "app", AppSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewOfficialMessageClientAtRoot("app", root, transport)
	if err != nil || client.nativeRoot != root {
		t.Fatalf("native card root lost across transport boundary: root=%q err=%v", client.nativeRoot, err)
	}
}
