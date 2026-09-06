package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/userapproval"
)

func TestReadAndBotRejectBusyAuthorizationLease(t *testing.T) {
	for _, identity := range []string{"user", "bot"} {
		t.Run(identity, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			runner.UserApproval = nil
			release, err := userapproval.TryExecutionLease(runner.DataRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			definition := CapabilityDefinition{ID: "docs.shortcut.fetch", Identity: identity, Risk: "read", Command: []string{"docs", "+fetch"}}
			_, err = runner.run(context.Background(), definition, []string{"docs", "+fetch", "--doc", "doc_fixture"}, nil, nil, time.Second)
			if err == nil || err.Error() != "approval_authorization_busy" || CapabilityOutcomeUncertain(err) {
				t.Fatalf("busy read: %v", err)
			}
			if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) || *requests != 0 {
				t.Fatal("busy read executed or requested desktop")
			}
		})
	}
}

func TestReadAndBotRecheckIdentityUnderAuthorizationLease(t *testing.T) {
	for _, identity := range []string{"user", "bot"} {
		t.Run(identity, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			runner.UserApproval = nil
			script, err := os.ReadFile(runner.Binary)
			if err != nil {
				t.Fatal(err)
			}
			changed := `{"appId":"cli_changed","brand":"feishu","identities":{"user":{"available":true,"openId":"ou_fixture","userName":"Fixture"},"bot":{"available":true}}}`
			branch := "*'auth status'*) if [ -e '" + logPath + ".auth' ]; then printf '%s' '" + changed + "'; exit 0; fi; : > '" + logPath + ".auth';"
			if err := os.WriteFile(runner.Binary, []byte(strings.Replace(string(script), "*'auth status'*)", branch, 1)), 0700); err != nil {
				t.Fatal(err)
			}
			definition := CapabilityDefinition{ID: "docs.shortcut.fetch", Identity: identity, Risk: "read", Command: []string{"docs", "+fetch"}}
			_, err = runner.run(context.Background(), definition, []string{"docs", "+fetch", "--doc", "doc_fixture"}, nil, nil, time.Second)
			if err == nil || err.Error() != "user_command_identity_changed" || CapabilityOutcomeUncertain(err) {
				t.Fatalf("identity changed read: %v", err)
			}
			if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) || *requests != 0 {
				t.Fatal("identity-changed read executed or requested desktop")
			}
			release, err := userapproval.TryExecutionLease(runner.DataRoot)
			if err != nil {
				t.Fatalf("identity failure leaked lease: %v", err)
			}
			release()
		})
	}
}

func TestReadAndBotHoldAuthorizationLeaseThroughProcess(t *testing.T) {
	for _, identity := range []string{"user", "bot"} {
		t.Run(identity, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			runner.UserApproval = nil
			script, err := os.ReadFile(runner.Binary)
			if err != nil {
				t.Fatal(err)
			}
			resume := filepath.Join(runner.DataRoot, "resume")
			defer os.WriteFile(resume, nil, 0600)
			body := ": > '" + logPath + ".running'; while [ ! -e '" + resume + "' ]; do sleep 0.01; done; cat >/dev/null"
			if err := os.WriteFile(runner.Binary, []byte(strings.Replace(string(script), "cat >/dev/null", body, 1)), 0700); err != nil {
				t.Fatal(err)
			}
			definition := CapabilityDefinition{ID: "docs.shortcut.fetch", Identity: identity, Risk: "read", Command: []string{"docs", "+fetch"}}
			done := make(chan error, 1)
			go func() {
				_, err := runner.run(context.Background(), definition, []string{"docs", "+fetch", "--doc", "doc_fixture"}, nil, nil, 3*time.Second)
				done <- err
			}()
			deadline := time.After(3 * time.Second)
			for {
				if _, err := os.Stat(logPath + ".running"); err == nil {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("read exited before fixture barrier: %v", err)
				case <-deadline:
					t.Fatal("read did not reach fixture barrier")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if release, err := userapproval.TryExecutionLease(runner.DataRoot); err == nil {
				release()
				t.Fatal("read process did not hold identity lease")
			}
			if _, err := runner.RunAuthJSON(context.Background(), []string{"auth", "logout", "--json"}, nil, time.Second); err == nil || err.Error() != "approval_authorization_busy" {
				t.Fatalf("logout crossed read execution: %v", err)
			}
			if err := os.WriteFile(resume, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil || *requests != 0 {
				t.Fatalf("read failed or opened desktop: %v %d", err, *requests)
			}
			release, err := userapproval.TryExecutionLease(runner.DataRoot)
			if err != nil {
				t.Fatalf("completed read leaked lease: %v", err)
			}
			release()
		})
	}
}

func TestApprovalPreflightBorrowsOnlyInternalSameRootLease(t *testing.T) {
	runner, _, requests := fakeApprovedBusinessRunner(t, "approved")
	enableCapabilityWrites(t, runner.DataRoot)
	definition, _ := CapabilityByID("markdown.overwrite")
	policyStore := NewCapabilityPolicyStore(runner.DataRoot)
	policy, err := policyStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.RiskDefaults["destructive"] = CapabilityConfirmEach
	if _, err := policyStore.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"file-token": "fm_fixture", "content": "replacement"}
	evidence, err := runner.ReadPreflight(context.Background(), definition.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	operations := NewOperationService(runner.DataRoot, NewCapabilityPolicyStore(runner.DataRoot), nil)
	view, challenge, err := operations.PrepareWithEvidence(definition, input, "fixture", evidence)
	if err != nil {
		t.Fatal(err)
	}
	if challenge != "" {
		view, err = operations.ConfirmWithEvidence(view.ID, challenge, evidence)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := operations.ClaimExecutionWithEvidence(view.ID, definition.ID, input, evidence); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, executionBoundaryKey{}, executionBoundary{operations: operations, operationID: view.ID, capabilityID: definition.ID, input: input})
	if _, err := runner.runDefinition(ctx, definition, input, time.Second); err != nil || *requests != 1 {
		t.Fatalf("nested preflight reacquired lease or bypassed approval: %v %d", err, *requests)
	}
}
