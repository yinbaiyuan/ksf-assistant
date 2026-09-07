package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/userapproval"
	"ksfassistant/core/internal/usercommand"
)

func allowFixtureBusinessCommands() *UserApprovalGate {
	return &UserApprovalGate{testExecute: func(context.Context, CapabilityDefinition, []string, []byte) error { return nil }}
}

func TestBusinessWriteWithoutDesktopNeverStartsProcess(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "denied")
	runner.UserApproval = nil
	definition := CapabilityDefinition{ID: "docs.shortcut.create", Identity: "user", Risk: "write", Command: []string{"docs", "+create"}}
	_, err := runner.run(context.Background(), definition, []string{"docs", "+create", "--doc-format", "markdown", "--content", "-"}, []byte("private body"), nil, time.Second)
	if err == nil || err.Error() != "approval_desktop_unavailable" || isUncertainExecutionError(err) {
		t.Fatalf("unexpected approval result: %v", err)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unapproved write reached process")
	}
}

func TestBusinessApprovalErrorsRemainPreExecutionFailures(t *testing.T) {
	for _, code := range []string{"approval_expired", "user_approval_denied", "approval_policy_changed", "approval_cancelled"} {
		err := &CapabilityExecutionError{Phase: "write", Err: &UserApprovalError{Code: code}}
		if CapabilityOutcomeUncertain(err) || isUncertainExecutionError(err) || CapabilityOperationErrorCode(err) != code {
			t.Fatalf("approval denial treated as side effect: %v", err)
		}
	}
}

func fakeApprovedBusinessRunner(t *testing.T, state string) (CapabilityExecutor, string, *int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	root := t.TempDir()
	logPath := filepath.Join(root, "writes")
	binary := filepath.Join(root, "fake-cli")
	script := "#!/bin/sh\ncase \"$*\" in\n *'auth status'*) printf '%s\\n' '{\"appId\":\"cli_fixture\",\"brand\":\"feishu\",\"identities\":{\"user\":{\"available\":true,\"openId\":\"ou_fixture\",\"userName\":\"Fixture\"},\"bot\":{\"available\":true}}}' ;;\n *) cat >/dev/null; printf '%s\\n' write >> '" + logPath + "'; printf '%s\\n' '{\"ok\":true,\"data\":{}}' ;;\nesac\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	requests := 0
	gate := &UserApprovalGate{}
	gate.SetCaller(func(_ context.Context, method string, input, output any) error {
		switch method {
		case "userApproval/request":
			requests++
			output.(*usercommand.RequestResult).ID = "fixture"
		case "userApproval/status":
			output.(*usercommand.StatusResult).State = state
		case "userApproval/consume":
			output.(*usercommand.ConsumeResult).Allowed = true
		case "userApproval/result", "userApproval/cancel":
		default:
			return errors.New("unexpected_method")
		}
		return nil
	})
	return CapabilityExecutor{Binary: binary, Profile: "default", DataRoot: root, WorkingDirectory: root, UserApproval: gate}, logPath, &requests
}

func TestBusinessApprovalFreezesStdinBeforeReview(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "approved")
	script, err := os.ReadFile(runner.Binary)
	if err != nil {
		t.Fatal(err)
	}
	bodyPath := logPath + ".body"
	script = []byte(strings.Replace(string(script), "cat >/dev/null", "cat > '"+bodyPath+"'", 1))
	if err := os.WriteFile(runner.Binary, script, 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("original body")
	original := runner.UserApproval.caller
	runner.UserApproval.SetCaller(func(ctx context.Context, method string, input, output any) error {
		if method == "userApproval/request" {
			copy(body, []byte("changed body!"))
		}
		return original(ctx, method, input, output)
	})
	definition := CapabilityDefinition{ID: "docs.shortcut.create", Identity: "user", Risk: "write", Command: []string{"docs", "+create"}}
	_, err = runner.run(context.Background(), definition, []string{"docs", "+create", "--doc-format", "markdown", "--content", "-"}, body, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(bodyPath)
	if err != nil || string(actual) != "original body" {
		t.Fatalf("input changed after approval: %q %v", actual, err)
	}
}

func TestBusinessApprovalRechecksUserIdentityBeforeConsume(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "approved")
	original := runner.UserApproval.caller
	runner.UserApproval.SetCaller(func(ctx context.Context, method string, input, output any) error {
		if method == "userApproval/status" {
			script, err := os.ReadFile(runner.Binary)
			if err != nil {
				return err
			}
			if err := os.WriteFile(runner.Binary, []byte(strings.ReplaceAll(string(script), "ou_fixture", "ou_changed")), 0700); err != nil {
				return err
			}
		}
		return original(ctx, method, input, output)
	})
	_, err := runFixtureDocumentCreate(context.Background(), runner, "fixture")
	if err == nil || err.Error() != "user_command_identity_changed" {
		t.Fatalf("identity change: %v", err)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("identity-changed request executed")
	}
}

func TestBusinessApprovalRechecksIdentityImmediatelyBeforeExecution(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "approved")
	original := runner.UserApproval.caller
	runner.UserApproval.SetCaller(func(ctx context.Context, method string, input, output any) error {
		if method == "userApproval/consume" {
			script, err := os.ReadFile(runner.Binary)
			if err != nil {
				return err
			}
			if err := os.WriteFile(runner.Binary, []byte(strings.ReplaceAll(string(script), "ou_fixture", "ou_changed")), 0700); err != nil {
				return err
			}
		}
		return original(ctx, method, input, output)
	})
	_, err := runFixtureDocumentCreate(context.Background(), runner, "fixture")
	if err == nil || err.Error() != "user_command_identity_changed" {
		t.Fatalf("late identity change: %v", err)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late identity-changed request executed")
	}
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatalf("failed write leaked authorization lease: %v", err)
	}
	release()
}

func TestBusinessApprovalHoldsAuthorizationLeaseOnlyAfterDecision(t *testing.T) {
	runner, _, _ := fakeApprovedBusinessRunner(t, "approved")
	original := runner.UserApproval.caller
	consumed := false
	runner.UserApproval.SetCaller(func(ctx context.Context, method string, input, output any) error {
		if method == "userApproval/request" {
			release, err := userapproval.TryExecutionLease(runner.DataRoot)
			if err != nil {
				t.Fatalf("lease held while waiting for user: %v", err)
			}
			release()
		}
		if method == "userApproval/consume" {
			consumed = true
			release, err := userapproval.TryExecutionLease(runner.DataRoot)
			if err == nil {
				release()
				t.Fatal("consume did not hold identity lease")
			}
			if err.Error() != "approval_authorization_busy" {
				t.Fatal(err)
			}
			if _, err := runner.RunAuthJSON(ctx, []string{"auth", "logout", "--json"}, nil, time.Second); err == nil || err.Error() != "approval_authorization_busy" {
				t.Fatalf("logout crossed execute lease: %v", err)
			}
		}
		return original(ctx, method, input, output)
	})
	if _, err := runFixtureDocumentCreate(context.Background(), runner, "fixture"); err != nil {
		t.Fatal(err)
	}
	if !consumed {
		t.Fatal("not consumed")
	}
	release, err := userapproval.TryExecutionLease(runner.DataRoot)
	if err != nil {
		t.Fatalf("completed write leaked lease: %v", err)
	}
	release()
}

func TestBusinessUserWriteApprovalAndRejection(t *testing.T) {
	for _, state := range []string{"approved", "denied"} {
		t.Run(state, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, state)
			_, err := runFixtureDocumentCreate(context.Background(), runner, "plain content")
			data, _ := os.ReadFile(logPath)
			if *requests != 1 {
				t.Fatalf("requests=%d err=%v", *requests, err)
			}
			if state == "approved" {
				if err != nil || strings.TrimSpace(string(data)) != "write" {
					t.Fatalf("approved request: %q %v", data, err)
				}
			} else if err == nil || len(data) != 0 || isUncertainExecutionError(err) {
				t.Fatalf("denied request executed: %q %v", data, err)
			}
		})
	}
}

func TestBusinessApprovalDoesNotHoldGateMutex(t *testing.T) {
	runner, _, _ := fakeApprovedBusinessRunner(t, "denied")
	gate := runner.UserApproval
	gate.SetCaller(func(_ context.Context, method string, _, _ any) error {
		gate.SetCaller(nil)
		return errors.New("approval_desktop_unavailable")
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := runFixtureDocumentCreate(ctx, runner, "body")
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("gate callback locked itself: %v", err)
	}
}

func TestBusinessReadsAndBotDoNotRequestDesktopApproval(t *testing.T) {
	for _, identity := range []string{"user", "bot"} {
		t.Run(identity, func(t *testing.T) {
			runner, _, requests := fakeApprovedBusinessRunner(t, "denied")
			definition := CapabilityDefinition{ID: "docs.shortcut.fetch", Identity: identity, Risk: "read", Command: []string{"docs", "+fetch"}}
			if _, err := runner.run(context.Background(), definition, []string{"docs", "+fetch", "--doc", "doc_fixture"}, nil, nil, time.Second); err != nil {
				t.Fatal(err)
			}
			if *requests != 0 {
				t.Fatal("read requested desktop approval")
			}
		})
	}
}

func TestBusinessApprovalRechecksPolicyBeforeConsume(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "approved")
	original := runner.UserApproval.caller
	runner.UserApproval.SetCaller(func(ctx context.Context, method string, input, output any) error {
		if method == "userApproval/status" {
			store := NewCapabilityPolicyStore(runner.DataRoot)
			policy, err := store.Load()
			if err != nil {
				return err
			}
			if _, err := store.Save(policy, policy.Revision); err != nil {
				return err
			}
		}
		return original(ctx, method, input, output)
	})
	_, err := runFixtureDocumentCreate(context.Background(), runner, "fixture")
	if err == nil || err.Error() != "approval_policy_changed" {
		t.Fatalf("policy change: %v", err)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("policy-changed request executed")
	}
}

func TestBusinessApprovalRechecksPolicyAfterConsume(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "approved")
	original := runner.UserApproval.caller
	outcome := ""
	runner.UserApproval.SetCaller(func(ctx context.Context, method string, input, output any) error {
		if method == "userApproval/consume" {
			store := NewCapabilityPolicyStore(runner.DataRoot)
			policy, err := store.Load()
			if err != nil {
				return err
			}
			policy.CapabilityOverrides["docs.shortcut.create"] = CapabilityDisabled
			if _, err := store.Save(policy, policy.Revision); err != nil {
				return err
			}
		}
		if method == "userApproval/result" {
			outcome = input.(usercommand.ResultRequest).Outcome
		}
		return original(ctx, method, input, output)
	})
	_, err := runFixtureDocumentCreate(context.Background(), runner, "fixture")
	if err == nil || err.Error() != "approval_policy_denied" || outcome != "not_started" {
		t.Fatalf("late policy rejection: %v, outcome=%s", err, outcome)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("policy changed after consume but process still executed")
	}
}

func TestBusinessGateRejectionTerminatesQueuedWritesWithoutReplay(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "denied"}[configured], func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			if !configured {
				runner.UserApproval = nil
			}
			settings := enableCapabilityWrites(t, runner.DataRoot)
			if err := NewSettingsStore(runner.DataRoot).Save(settings); err != nil {
				t.Fatal(err)
			}
			service := NewCapabilityService(runner.DataRoot, UnifiedCapabilityExecutor{LongTail: runner, DataRoot: runner.DataRoot}, nil)
			policy, _ := service.ReadPolicy()
			policy.CapabilityOverrides["docs.shortcut.create"] = CapabilityConfirmEach
			if _, err := service.UpdatePolicy(policy, policy.Revision); err != nil {
				t.Fatal(err)
			}
			id, input := "docs.shortcut.create", map[string]any{"content": "fixture", "doc-format": "markdown"}
			prepared, err := service.Prepare(context.Background(), id, input, "test")
			if err != nil {
				t.Fatal(err)
			}
			if configured {
				if prepared.Challenge != "" || !prepared.Submitted {
					t.Fatalf("double confirmation: %#v", prepared)
				}
			} else {
				prepared, err = service.Confirm(context.Background(), prepared.Operation.ID, prepared.Challenge)
				if err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := service.ProcessActions(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			view, err := service.Status(prepared.Operation.ID)
			if err != nil || view.Status != OperationFailed || view.AttemptCount != 1 {
				t.Fatalf("not terminal: %#v %v", view, err)
			}
			if configured && *requests != 1 {
				t.Fatalf("approval replayed: %d", *requests)
			}
			if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected queued write executed")
			}
		})
	}
}

func TestBusinessUnknownSemanticsNeverExecute(t *testing.T) {
	runner, logPath, requests := fakeApprovedBusinessRunner(t, "approved")
	definition := CapabilityDefinition{ID: "unreviewed", Identity: "user", Risk: "write", Command: []string{"unknown", "+execute"}}
	_, err := runner.run(context.Background(), definition, []string{"unknown", "+execute"}, nil, nil, time.Second)
	if err == nil || *requests != 0 {
		t.Fatalf("unknown semantics approved: %v", err)
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unknown semantics executed")
	}
}

func runFixtureDocumentCreate(ctx context.Context, runner CapabilityExecutor, body string) (map[string]any, error) {
	return runner.run(ctx, CapabilityDefinition{ID: "docs.shortcut.create", Identity: "user", Risk: "write", Command: []string{"docs", "+create"}}, []string{"docs", "+create", "--doc-format", "markdown", "--content", "-"}, []byte(body), nil, time.Second)
}
