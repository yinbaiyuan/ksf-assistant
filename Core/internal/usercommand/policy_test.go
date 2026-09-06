package usercommand

import (
	"os"
	"path/filepath"
	"testing"

	"ksfassistant/core/internal/capabilitypolicy"
)

func TestEveryIdentityAndAliasChecksDisabledPolicy(t *testing.T) {
	root := t.TempDir()
	policy := capabilitypolicy.Default()
	policy.CapabilityOverrides["fixture.upload"] = capabilitypolicy.Disabled
	if _, err := capabilitypolicy.NewStore(root).Save(policy, 1); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{"user", "bot"} {
		for _, risk := range []string{"read", "write"} {
			review := Review{Identity: identity, Risk: risk, CapabilityIDs: []string{"fixture.send", "fixture.upload"}}
			if _, err := CheckPolicy(root, review); err == nil || err.Error() != "approval_policy_denied" {
				t.Fatal("disabled alias bypassed", identity, risk, err)
			}
		}
	}
}

func TestUnavailablePolicyIsNotDefaultSuccess(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feishu-capability-policy-v1.json"), []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckPolicy(root, Review{Identity: "user", Risk: "read", CapabilityID: "fixture"}); err == nil {
		t.Fatal("malformed policy accepted")
	}
	if _, err := CheckPolicy(root, Review{Identity: "local"}); err != nil {
		t.Fatal("local diagnostics depend on policy", err)
	}
}

func TestReadConfirmationOverrideIsNotImplicitPermission(t *testing.T) {
	root := t.TempDir()
	policy := capabilitypolicy.Default()
	policy.CapabilityOverrides["calendar.shortcut.agenda"] = capabilitypolicy.ConfirmEach
	if _, err := capabilitypolicy.NewStore(root).Save(policy, 1); err != nil {
		t.Fatal(err)
	}
	command := Command{Args: []string{"calendar", "+agenda", "--as", "user"}}
	review := Review{Identity: "user", Risk: "read", CapabilityID: "calendar.shortcut.agenda"}
	if err := CheckUnpromptedPolicy(root, command, review); err == nil {
		t.Fatal("confirmation override treated as allowed")
	}
}

func TestPolicyUsesEachEffectRisk(t *testing.T) {
	root := t.TempDir()
	policy := capabilitypolicy.Default()
	policy.RiskDefaults["read"] = capabilitypolicy.Disabled
	if _, err := capabilitypolicy.NewStore(root).Save(policy, 1); err != nil {
		t.Fatal(err)
	}
	review := Review{Identity: "user", Risk: "write", CapabilityID: "fixture.write", CapabilityIDs: []string{"fixture.write", "fixture.read"}, Effects: []Effect{
		{CapabilityID: "fixture.write", Risk: "write"}, {CapabilityID: "fixture.read", Risk: "read"},
	}}
	if _, err := CheckPolicy(root, review); err == nil {
		t.Fatal("write risk hid disabled implicit read")
	}
}
