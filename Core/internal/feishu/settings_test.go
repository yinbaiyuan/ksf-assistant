package feishu

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSettingsStoreStartsWithSafeDefaultsAndWritesPrivately(t *testing.T) {
	root := t.TempDir()
	store := NewSettingsStore(root)

	settings, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Version != 1 || settings.Profile != ProfileManualOnly {
		t.Fatalf("unexpected defaults: %#v", settings)
	}
	if settings.Outbound.Enabled || !settings.Outbound.DryRun || settings.Group.Enabled {
		t.Fatalf("unsafe defaults: %#v", settings)
	}

	settings.Profile = ProfilePrimary
	settings.Directory.Enabled = true
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Profile != ProfilePrimary || !reloaded.Directory.Enabled {
		t.Fatalf("settings did not round trip: %#v", reloaded)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, SettingsFilename))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("settings permissions = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestSettingsValidationRejectsUnknownProfilesAndUnsafeValues(t *testing.T) {
	settings := DefaultSettings()
	settings.Profile = "cards-only"
	if err := settings.Validate(); err == nil {
		t.Fatal("expected unknown profile to fail")
	}

	settings = DefaultSettings()
	settings.Codex.DefaultThreadTitle = "bad\nvalue"
	if err := settings.Validate(); err == nil {
		t.Fatal("expected control characters to fail")
	}
}

func TestSetupStorePersistsOnlyResumableNonSecretState(t *testing.T) {
	root := t.TempDir()
	store := NewSetupStore(root)
	state := DefaultSetupState()
	state.Stage = SetupAuthorizationPending
	state.Mode = SetupModeExisting
	state.UserCode = "ABCD-EFGH"
	state.VerificationURL = "https://accounts.feishu.cn/example"
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, SetupFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"appSecret", "access_token", "open_id"} {
		if containsFold(string(data), forbidden) {
			t.Fatalf("setup state contains forbidden key %q: %s", forbidden, data)
		}
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Stage != SetupAuthorizationPending || reloaded.Mode != SetupModeExisting {
		t.Fatalf("setup state did not resume: %#v", reloaded)
	}
}
