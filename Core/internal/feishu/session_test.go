package feishu

import (
	"context"
	"ksfassistant/core/internal/capabilitypolicy"
	"testing"
)

func TestSignedOutRefusesMessageAndCapabilityBeforeCLI(t *testing.T) {
	root := t.TempDir()
	if err := capabilitypolicy.SignOut(root); err != nil {
		t.Fatal(err)
	}
	runner := CapabilityExecutor{DataRoot: root, Binary: "must-not-execute"}
	if _, err := runner.CallMessage(context.Background(), MessageCLIRequest{Resource: "messages", Method: "create"}); err != capabilitypolicy.ErrSignedOut {
		t.Fatal(err)
	}
	if _, err := runner.ExecuteWithOptions(context.Background(), "im.sdk.message.send", nil, CapabilityExecutionOptions{}); err != capabilitypolicy.ErrSignedOut {
		t.Fatal(err)
	}
}
func TestLoginScopesIncludeOfferedExtensionsWithoutRequiringUnavailableOnes(t *testing.T) {
	scopes, err := loginPermissionScopes([]string{"contact:user.base:readonly", "docx:document:readonly"})
	if err != nil || len(scopes) != 1 || scopes[0] != "contact:user.base:readonly" {
		t.Fatal(scopes, err)
	}
	if _, err = loginPermissionScopes([]string{"docx:document:readonly"}); err == nil {
		t.Fatal("identity prerequisite omitted")
	}
}

func TestUserOAuthNeverChangesGlobalBotSessionMarker(t *testing.T) {
	for _, verified := range []string{"true", "false"} {
		t.Run(verified, func(t *testing.T) {
			runner := fakeAuthCLI(t, `case "$4" in
status) printf '%s' '{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"verified":`+verified+`,"scope":"contact:user.base:readonly"}}}' ;;
scopes) printf '%s' '{"appId":"cli_fixture","brand":"feishu","tokenType":"user","userScopes":["contact:user.base:readonly"]}' ;;
esac`)
			if err := capabilitypolicy.SignOut(runner.DataRoot); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			close(done)
			userAuthSessions.Lock()
			userAuthSessions.items[runner.DataRoot] = &userAuthSession{done: done, cancel: func() {}}
			userAuthSessions.Unlock()
			_, err := finishUserAuthSession(context.Background(), runner, runner.DataRoot)
			if err != nil {
				t.Fatal(err)
			}
			if capabilitypolicy.CheckSession(runner.DataRoot) == nil {
				t.Fatal("user OAuth changed the independent bot session marker")
			}
		})
	}
}
