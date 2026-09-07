package feishu

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestSettingsCompareAndSwapHasOneConcurrentWinner(t *testing.T) {
	store := NewSettingsStore(t.TempDir())
	expected, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, title := range []string{"first", "second"} {
		next := expected
		next.Codex.DefaultThreadTitle = title
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- store.CompareAndSwap(expected, next)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrSettingsConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", successes, conflicts)
	}
}

func TestSettingsCompareAndSwapPreservesUnknownAndRejectsKnownChanges(t *testing.T) {
	store := NewSettingsStore(t.TempDir())
	expected, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte(`{"version":2,"future":{"private":"fixture"},"group":{"futureFlag":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	next := expected
	next.Group.Enabled = true
	if err := store.CompareAndSwap(expected, next); err != nil {
		t.Fatalf("unknown-only change rejected: %v", err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil || !bytes.Contains(before, []byte(`"private": "fixture"`)) || !bytes.Contains(before, []byte(`"futureFlag": true`)) {
		t.Fatal("CAS discarded current unknown fields")
	}
	stale := expected
	stale.Group.Enabled = false
	if err := store.CompareAndSwap(expected, stale); !errors.Is(err, ErrSettingsConflict) {
		t.Fatalf("known field change overwritten: %v", err)
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("conflicting CAS wrote settings")
	}
}

func TestSettingsLegacyEventProfileIsReadOnly(t *testing.T) {
	for _, legacy := range []string{"", "primary", "manual-only", "retired-role"} {
		t.Run(legacy, func(t *testing.T) {
			root := t.TempDir()
			store := NewSettingsStore(root)
			original := DefaultSettings()
			original.Profile = legacy
			if err := writePrivateJSON(store.path, original); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Load()
			if err != nil || loaded.Profile != legacy {
				t.Fatalf("legacy read: %+v %v", loaded, err)
			}
			after, err := os.ReadFile(store.path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("read migrated settings: %v", err)
			}
			loaded.Profile = "replacement-role"
			loaded.Group.Enabled = true
			if err := store.Save(loaded); err != nil {
				t.Fatal(err)
			}
			stored, err := store.Load()
			if err != nil || stored.Profile != legacy || !stored.Group.Enabled {
				t.Fatalf("settings write changed legacy event role: %+v %v", stored, err)
			}
		})
	}
}

func TestSettingsNewWritesDoNotCreateEventRole(t *testing.T) {
	store := NewSettingsStore(t.TempDir())
	settings := DefaultSettings()
	settings.Profile = "manual-only"
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil || !bytes.Contains(data, []byte(`"profile": ""`)) {
		t.Fatalf("new settings created retired event profile: %s %v", data, err)
	}
}

func TestSettingsUnknownFieldsStayOnDiskOnly(t *testing.T) {
	store := NewSettingsStore(t.TempDir())
	data := []byte(`{"version":2,"profile":"manual-only","futureSettings":{"fixturePrivate":"retained"},"group":{"enabled":false,"futureMode":"retained"},"codex":{"defaultThreadTitle":"fixture","futureOption":42}}`)
	if err := os.WriteFile(store.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || !loaded.Outbound.Enabled || loaded.Outbound.DryRun {
		t.Fatalf("known settings unavailable: %+v %v", loaded, err)
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("reading changed stored settings")
	}
	public, err := json.Marshal(loaded)
	if err != nil || bytes.Contains(public, []byte("future")) || bytes.Contains(public, []byte("fixturePrivate")) {
		t.Fatal("unknown fields leaked onto the wire")
	}
	var wire Settings
	if err := json.Unmarshal(public, &wire); err != nil || wire != loaded {
		t.Fatalf("known wire settings not comparable: %v", err)
	}
	newer := bytes.Replace(data, []byte(`"futureMode":"retained"`), []byte(`"futureMode":"newer"`), 1)
	if err := os.WriteFile(store.path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	wire.Group.Enabled = true
	wire.Profile = "replacement-role"
	if err := store.Save(wire); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{`"fixturePrivate": "retained"`, `"futureMode": "newer"`, `"futureOption": 42`} {
		if !bytes.Contains(after, []byte(retained)) {
			t.Fatalf("current unknown field was lost: %s", retained)
		}
	}
	reloaded, err := store.Load()
	if err != nil || reloaded.Profile != "manual-only" || !reloaded.Group.Enabled || !reloaded.Outbound.Enabled || reloaded.Outbound.DryRun {
		t.Fatalf("known settings or legacy profile changed: %+v %v", reloaded, err)
	}
}

func TestSetupUnknownFieldsStayOnDiskAndClearedKnownFieldsStayCleared(t *testing.T) {
	store := NewSetupStore(t.TempDir())
	data := []byte(`{"version":1,"stage":"authorization_pending","verificationURL":"https://example.test","userCode":"fixture","readyToActivate":true,"futurePrivate":{"fixtureValue":"retained"}}`)
	if err := os.WriteFile(store.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil || state.Stage != SetupAuthorizationPending {
		t.Fatalf("known setup unavailable: %+v %v", state, err)
	}
	public, err := json.Marshal(state)
	if err != nil || bytes.Contains(public, []byte("futurePrivate")) || bytes.Contains(public, []byte("fixtureValue")) {
		t.Fatal("private unknown setup leaked to wire")
	}
	state.Stage = SetupPlatformPending
	state.VerificationURL, state.UserCode, state.ReadyToActivate = "", "", false
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Contains(after, []byte(`"futurePrivate"`)) {
		t.Fatal("unknown setup field was lost")
	}
	for _, omitted := range []string{"verificationURL", "userCode", "readyToActivate"} {
		if bytes.Contains(after, []byte(omitted)) {
			t.Fatalf("cleared known field was restored: %s", omitted)
		}
	}
	reloaded, err := store.Load()
	if err != nil || reloaded != state {
		t.Fatalf("setup known values changed: %+v %v", reloaded, err)
	}
}

func TestStoreUnknownPreservationDoesNotRelaxWireOrValidation(t *testing.T) {
	for _, target := range []any{&Settings{}, &SetupState{}} {
		decoder := json.NewDecoder(bytes.NewReader([]byte(`{"version":1,"futurePrivate":true}`)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err == nil {
			t.Fatal("wire unknown field accepted")
		}
	}
	for _, data := range []string{`{"version":3}`, `null`, `[]`, `{"version":1} {}`, `{"version":1,"outbound":{"enabled":"true"}}`} {
		store := NewSettingsStore(t.TempDir())
		if err := os.WriteFile(store.path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(); err == nil {
			t.Fatalf("invalid settings accepted: %s", data)
		}
		if err := store.Save(DefaultSettings()); err == nil {
			t.Fatalf("invalid current settings overwritten: %s", data)
		}
		after, err := os.ReadFile(store.path)
		if err != nil || string(after) != data {
			t.Fatal("invalid stored settings modified")
		}
	}
	for _, data := range []string{`{"version":2,"stage":"not_started"}`, `{"version":1,"stage":"future"}`, `{"version":1,"stage":"not_started","mode":"future"}`} {
		store := NewSetupStore(t.TempDir())
		if err := os.WriteFile(store.path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(); err == nil {
			t.Fatal("unknown setup semantics accepted")
		}
		if err := store.Save(DefaultSetupState()); err == nil {
			t.Fatal("unknown setup semantics overwritten")
		}
	}
}

func TestSettingsStoreStartsWithSafeDefaultsAndWritesPrivately(t *testing.T) {
	root := t.TempDir()
	store := NewSettingsStore(root)

	settings, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Version != 2 || settings.Profile != "" {
		t.Fatalf("unexpected defaults: %#v", settings)
	}
	if !settings.Outbound.Enabled || settings.Outbound.DryRun || settings.Group.Enabled || settings.MailEvents.Enabled {
		t.Fatalf("unsafe defaults: %#v", settings)
	}

	settings.Group.Enabled = true
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Profile != "" || !reloaded.Group.Enabled {
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

func TestSettingsValidationRejectsUnsafeValues(t *testing.T) {
	settings := DefaultSettings()
	settings.Profile = "bad\nlegacy-role"
	if err := settings.Validate(); err == nil {
		t.Fatal("expected unsafe legacy profile text to fail")
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
