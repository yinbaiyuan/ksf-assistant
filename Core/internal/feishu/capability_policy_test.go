package feishu

import (
	"errors"
	"testing"
)

func TestCapabilityPolicyDefaultsAndOverrides(t *testing.T) {
	policy := DefaultCapabilityPolicy()
	cases := []struct {
		risk string
		want CapabilityPermission
	}{
		{"read", CapabilityAllowed},
		{"write", CapabilityAllowed},
		{"high-impact-write", CapabilityConfirmEach},
		{"remote-operation", CapabilityConfirmEach},
		{"destructive", CapabilityDisabled},
	}
	for _, tc := range cases {
		definition := CapabilityDefinition{ID: "test." + tc.risk, Risk: tc.risk}
		if got := policy.Decision(definition); got != tc.want {
			t.Fatalf("risk %s decision = %s, want %s", tc.risk, got, tc.want)
		}
	}
	policy.CapabilityOverrides["test.destructive"] = CapabilityConfirmEach
	if got := policy.Decision(CapabilityDefinition{ID: "test.destructive", Risk: "destructive"}); got != CapabilityConfirmEach {
		t.Fatalf("override decision = %s", got)
	}
}

func TestCapabilityPolicyStoreRejectsStaleRevision(t *testing.T) {
	store := NewCapabilityPolicyStore(t.TempDir())
	policy, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.CapabilityOverrides["docs.document.delete"] = CapabilityConfirmEach
	updated, err := store.Save(policy, policy.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != policy.Revision+1 {
		t.Fatalf("revision = %d", updated.Revision)
	}
	_, err = store.Save(policy, policy.Revision)
	if !errors.Is(err, ErrCapabilityPolicyRevisionConflict) {
		t.Fatalf("stale save error = %v", err)
	}
}

func TestCapabilityPolicyRejectsUnknownRiskDefaults(t *testing.T) {
	store := NewCapabilityPolicyStore(t.TempDir())
	policy, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	policy.RiskDefaults["unreviewed-risk"] = CapabilityAllowed
	if _, err := store.Save(policy, policy.Revision); err == nil {
		t.Fatal("unknown risk default was accepted")
	}
}
