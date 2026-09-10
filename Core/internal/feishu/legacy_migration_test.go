package feishu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ksfassistant/core/internal/privatestore"
)

func TestRemoveLegacyProfilePreservesUnrelatedProfilesAndFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"currentApp":"default","futureField":{"keep":true},"apps":[{"name":"default","appId":"cli_owned","marker":"owned"},{"name":"personal","appId":"cli_personal","marker":"keep"}]}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeLegacyProfile(path, original, "default"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(data, &result) != nil || result["currentApp"] != nil || result["futureField"] == nil {
		t.Fatalf("legacy root fields were not preserved: %s", data)
	}
	var apps []map[string]any
	if json.Unmarshal(result["apps"], &apps) != nil || len(apps) != 1 || apps[0]["name"] != "personal" || apps[0]["marker"] != "keep" {
		t.Fatalf("unrelated profile was changed: %s", data)
	}
}

func TestLegacyMigrationRecoveryRefusesCleanupWithoutVerifiedManagedProfile(t *testing.T) {
	root := t.TempDir()
	journal := legacyMigrationJournal{SchemaVersion: 1, ApplicationID: "cli_owned", Profile: "default", Accounts: []string{"appsecret:cli_owned", "cli_owned:ou_owner", legacyTenantTokenAccount("cli_owned")}}
	if err := privatestore.WriteJSON(legacyMigrationJournalPath(root), journal); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeLegacyMigrationCleanup(root)
	if !resumed || err == nil {
		t.Fatalf("unverified migration cleanup was accepted: resumed=%v err=%v", resumed, err)
	}
	if _, err := os.Stat(legacyMigrationJournalPath(root)); err != nil {
		t.Fatal("recovery evidence was removed despite failed verification")
	}
}
