package feishu

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUserCapabilityCreatesAppBoundExactAuthorizationRequest(t *testing.T) {
	runner := fakeAuthCLI(t, `case "$4" in
status) printf '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":false,"status":"missing","scope":[]}}}' ;;
*) exit 1 ;;
esac`)
	definition := CapabilityDefinition{ID: "docs.fixture.read", Identity: "user", RequiredScopes: []string{"docx:document:readonly"}}
	err := ensureCapabilityUserAuthorization(context.Background(), runner, definition)
	var required *ProgressiveAuthorizationRequiredError
	if !errors.As(err, &required) || required.RequestID == "" {
		t.Fatalf("missing exact authorization request: %v", err)
	}
	request, err := readProgressiveAuthorizationRequest(runner.DataRoot)
	if err != nil || request == nil || request.ID != required.RequestID || request.ApplicationID != "cli_fixture" || request.Purpose != definition.ID || strings.Join(request.Scopes, " ") != "docx:document:readonly" {
		t.Fatalf("invalid progressive request: %#v %v", request, err)
	}
}

func TestProgressiveAuthorizationRequestRejectsArbitraryScope(t *testing.T) {
	root := t.TempDir()
	if _, err := writeProgressiveAuthorizationRequest(root, "cli_fixture", "fixture", []string{"admin:everything"}); err == nil {
		t.Fatal("desktop-controlled scope entered authorization store")
	}
}

func TestProgressiveAuthorizationRequestIsIdempotentForSameOperation(t *testing.T) {
	root := t.TempDir()
	first, err := writeProgressiveAuthorizationRequest(root, "cli_fixture", "docs.fixture.read", []string{"docx:document:write_only", "docx:document:readonly"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeProgressiveAuthorizationRequest(root, "cli_fixture", "docs.fixture.read", []string{"docx:document:readonly", "docx:document:write_only"})
	if err != nil || second.ID != first.ID || strings.Join(second.Scopes, " ") != "docx:document:readonly docx:document:write_only" {
		t.Fatalf("same preflight replaced the authorization request: first=%#v second=%#v err=%v", first, second, err)
	}
}
