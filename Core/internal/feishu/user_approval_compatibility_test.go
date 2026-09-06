package feishu

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestReadAndBotCommandsRespectDisabledWithoutDesktop(t *testing.T) {
	for _, identity := range []string{"user", "bot"} {
		t.Run(identity, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			runner.UserApproval = nil
			store := NewCapabilityPolicyStore(runner.DataRoot)
			policy, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			policy.CapabilityOverrides["docs.shortcut.fetch"] = CapabilityDisabled
			if _, err := store.Save(policy, policy.Revision); err != nil {
				t.Fatal(err)
			}
			definition := CapabilityDefinition{ID: "docbox.fetch", Identity: identity, Risk: "read", Command: []string{"docs", "+fetch"}}
			_, err = runner.run(context.Background(), definition, []string{"docs", "+fetch", "--doc", "doc_fixture"}, nil, nil, time.Second)
			if err == nil || err.Error() != "approval_policy_denied" {
				t.Fatalf("disabled %s: %v", identity, err)
			}
			if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) || *requests != 0 {
				t.Fatal("disabled read reached process or desktop")
			}
		})
	}
}

func TestPublishedCompatibilityCannotOverrideCanonicalSemantics(t *testing.T) {
	runner, logPath, _ := fakeApprovedBusinessRunner(t, "denied")
	runner.UserApproval = nil
	definition, _ := CapabilityByID("mail.shortcut.message.modify")
	definition.Risk, definition.Effect = "read", "read"
	_, err := runner.run(context.Background(), definition, []string{"mail", "+message-modify", "--message-ids", "om_fixture", "--remove-label-ids", "UNREAD"}, nil, nil, time.Second)
	if err == nil {
		t.Fatal("forged read reached process")
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("mutation was executed")
	}
}

func TestReadAndBotConfirmEachCannotBeSilentlyAllowed(t *testing.T) {
	for _, identity := range []string{"user", "bot"} {
		t.Run(identity, func(t *testing.T) {
			runner, logPath, requests := fakeApprovedBusinessRunner(t, "denied")
			runner.UserApproval = nil
			store := NewCapabilityPolicyStore(runner.DataRoot)
			policy, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			policy.CapabilityOverrides["docs.shortcut.fetch"] = CapabilityConfirmEach
			if _, err := store.Save(policy, policy.Revision); err != nil {
				t.Fatal(err)
			}
			definition := CapabilityDefinition{ID: "docbox.fetch", Identity: identity, Risk: "read", Command: []string{"docs", "+fetch"}}
			_, err = runner.run(context.Background(), definition, []string{"docs", "+fetch", "--doc", "doc_fixture"}, nil, nil, time.Second)
			if err == nil || err.Error() != "user_command_confirmation_required" {
				t.Fatalf("confirm_each bypassed: %v", err)
			}
			if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) || *requests != 0 {
				t.Fatal("unconfirmed call executed or requested a new desktop gate")
			}
		})
	}
}
