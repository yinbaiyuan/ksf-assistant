package usercommand

import (
	"ksfassistant/core/internal/capabilitypolicy"
	"testing"
)

func TestSignedOutBlocksBothIdentitiesButRetainsLocalDiagnosis(t *testing.T) {
	root := t.TempDir()
	if err := capabilitypolicy.SignOut(root); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{"user", "bot"} {
		for _, risk := range []string{"read", "write"} {
			if _, err := CheckPolicy(root, Review{Identity: identity, Risk: risk}); err != capabilitypolicy.ErrSignedOut {
				t.Fatal(identity, risk, err)
			}
		}
	}
	if _, err := CheckPolicy(root, Review{Identity: "local"}); err != nil {
		t.Fatal(err)
	}
}
