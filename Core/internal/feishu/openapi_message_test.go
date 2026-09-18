package feishu

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestOpenAPITransportDownloadsInboundMessageResourceWithoutCLI(t *testing.T) {
	root := t.TempDir()
	transport, err := NewOpenAPIMessageTransport(OfficialCredentials{AppID: "app", AppSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	transport.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "tenant_access_token") {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"tenant_access_token":"token","expire":3600}`)), Request: request}, nil
		}
		if request.Method != http.MethodGet || request.URL.Path != "/open-apis/im/v1/messages/om_fixture/resources/img_fixture" || request.URL.Query().Get("type") != "image" || request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected resource request: %s %s %#v", request.Method, request.URL.String(), request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, ContentLength: 7, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("picture")), Request: request}, nil
	})
	output := filepath.Join(root, "image")
	if err := transport.DownloadMessageResource(context.Background(), "om_fixture", "img_fixture", "image", output, time.Second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "picture" {
		t.Fatalf("resource was not staged: %q %v", data, err)
	}
}

func TestOpenAPIResourceDownloadRejectsRemoteFailureWithoutPartialFile(t *testing.T) {
	root := t.TempDir()
	transport, _ := NewOpenAPIMessageTransport(OfficialCredentials{AppID: "app", AppSecret: "secret"})
	transport.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, status := `{"code":0,"tenant_access_token":"token","expire":3600}`, http.StatusOK
		if !strings.Contains(request.URL.Path, "tenant_access_token") {
			body, status = `{"code":234043,"msg":"private detail"}`, http.StatusBadRequest
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	output := filepath.Join(root, "image")
	err := transport.DownloadMessageResource(context.Background(), "om_fixture", "img_fixture", "image", output, time.Second)
	var failure *CLIExecutionError
	if !errors.As(err, &failure) || failure.Outcome != "rejected" || failure.Structured["code"] != float64(234043) {
		t.Fatalf("unexpected resource failure: %#v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("failed download left a file: %v", statErr)
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
