package capabilitypolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingPolicyReadDoesNotCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	policy, err := NewStore(root).Load()
	if err != nil || policy.Decision("fixture", "read") != Allowed {
		t.Fatalf("%+v %v", policy, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read created policy state")
	}
}

func TestPolicyMalformedAndDuplicateFailClosed(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `{`, `{"version":1,"version":1}`, `{"Version":1}`,
		`{"riskDefaults":{"read":"disabled","read":"allowed"}}`,
		`{"riskDefaults":{"read":"allow"}}`, `{"riskDefaults":{"unreviewed":"allowed"}}`,
		`{"version":99}`,
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "feishu-capability-policy-v1.json"), []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(root).Load(); err == nil {
			t.Fatalf("accepted malformed policy %s", input)
		}
	}
}

func TestPolicySaveDigestAndRevision(t *testing.T) {
	store := NewStore(t.TempDir())
	policy := Default()
	before, _ := policy.Digest()
	policy.CapabilityOverrides["fixture"] = Disabled
	saved, err := store.Save(policy, 1)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Decision("fixture", "read") != Disabled || saved.Revision != 2 {
		t.Fatal("policy did not persist")
	}
	after, _ := loaded.Digest()
	if before == after {
		t.Fatal("policy change did not change digest")
	}
	if _, err := store.Save(policy, 1); err != ErrRevisionConflict {
		t.Fatal("stale policy saved")
	}
}
