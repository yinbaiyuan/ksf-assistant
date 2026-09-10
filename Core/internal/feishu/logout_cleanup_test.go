package feishu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ksfassistant/core/internal/capabilitypolicy"
)

func TestPurgeLocalFeishuStateRemovesAuthenticationAndPreservesHistory(t *testing.T) {
	root := t.TempDir()
	original := purgeManagedPlatformCredentials
	purgeManagedPlatformCredentials = func() error { return nil }
	t.Cleanup(func() { purgeManagedPlatformCredentials = original })
	for _, relative := range []string{"lark-cli/config.json", "auth/config-init.json", "auth/config-init.log", "auth/config-init.png", "auth/user-oauth.json", "auth/user-oauth.png", "client.json", SetupFilename, "credentials/official-sdk.json", "private-cache/" + progressiveAuthorizationRequestFilename, "private-cache/lark-cli-legacy-migration-v1.json", "feishu-session-signed-out-v1"} {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settings := DefaultSettings()
	settings.Group.Enabled = true
	settings.MailEvents.Enabled = true
	if err := NewSettingsStore(root).Save(settings); err != nil {
		t.Fatal(err)
	}
	history := map[string]string{
		"task-links-v1.json":         `{"links":[{"linkState":"released"}]}`,
		"logs/audit.jsonl":           "history",
		"integration-events-v1.json": "history",
	}
	for relative, contents := range history {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PurgeLocalFeishuState(root); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"lark-cli", "auth", "client.json", SetupFilename, "credentials/official-sdk.json", "private-cache/" + progressiveAuthorizationRequestFilename, "private-cache/lark-cli-legacy-migration-v1.json", "feishu-session-signed-out-v1", "private-cache/feishu-logout-cleanup-v1.json"} {
		if _, err := os.Lstat(filepath.Join(root, relative)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("authentication material survived: %s", relative)
		}
	}
	settings, err := NewSettingsStore(root).Load()
	if err != nil || settings.Group.Enabled || settings.MailEvents.Enabled {
		t.Fatalf("identity-dependent settings survived logout: %#v %v", settings, err)
	}
	if err := appCreationBusinessGuard(root); err != nil {
		t.Fatalf("successful cleanup still blocked a new connection: %v", err)
	}
	for relative, contents := range history {
		if data, err := os.ReadFile(filepath.Join(root, relative)); err != nil || string(data) != contents {
			t.Fatalf("history changed: %s", relative)
		}
	}
}

func TestPurgeFailureKeepsCleanupJournal(t *testing.T) {
	root := t.TempDir()
	original := purgeManagedPlatformCredentials
	purgeManagedPlatformCredentials = func() error { return errors.New("fixture") }
	t.Cleanup(func() { purgeManagedPlatformCredentials = original })
	if err := PurgeLocalFeishuState(root); err == nil {
		t.Fatal("platform cleanup failure claimed success")
	}
	if _, err := os.Stat(logoutCleanupPath(root)); err != nil {
		t.Fatal("cleanup journal was lost")
	}
	if capabilitypolicy.CheckSession(root) == nil {
		t.Fatal("failed cleanup reopened the Feishu execution gate")
	}
	purgeManagedPlatformCredentials = func() error { return nil }
	if err := PurgeLocalFeishuState(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logoutCleanupPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retry did not finish cleanup")
	}
}

func TestPurgeDeleteFailuresStayClosedAndCanResume(t *testing.T) {
	for _, test := range []struct {
		name   string
		inject func(root string) func()
	}{
		{
			name: "managed profile",
			inject: func(root string) func() {
				original := removeManagedTree
				failed := false
				removeManagedTree = func(path string) error {
					if !failed && path == filepath.Join(root, "lark-cli") {
						failed = true
						return errors.New("fixture profile delete failure")
					}
					return original(path)
				}
				return func() { removeManagedTree = original }
			},
		},
		{
			name: "authorization cache",
			inject: func(root string) func() {
				original := removeManagedTree
				failed := false
				removeManagedTree = func(path string) error {
					if !failed && path == filepath.Join(root, "auth") {
						failed = true
						return errors.New("fixture authorization cache delete failure")
					}
					return original(path)
				}
				return func() { removeManagedTree = original }
			},
		},
		{
			name: "operator binding",
			inject: func(root string) func() {
				original := removeManagedFile
				failed := false
				removeManagedFile = func(path string) error {
					if !failed && path == filepath.Join(root, "client.json") {
						failed = true
						return errors.New("fixture binding delete failure")
					}
					return original(path)
				}
				return func() { removeManagedFile = original }
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			originalPlatform := purgeManagedPlatformCredentials
			purgeManagedPlatformCredentials = func() error { return nil }
			defer func() { purgeManagedPlatformCredentials = originalPlatform }()
			for _, path := range []string{filepath.Join(root, "lark-cli", "config.json"), filepath.Join(root, "client.json")} {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			restore := test.inject(root)
			if err := PurgeLocalFeishuState(root); err == nil {
				restore()
				t.Fatal("delete failure claimed success")
			}
			restore()
			if !LocalFeishuCleanupPending(root) || capabilitypolicy.CheckSession(root) == nil {
				t.Fatal("delete failure did not keep Feishu fail-closed")
			}
			if err := PurgeLocalFeishuState(root); err != nil {
				t.Fatal(err)
			}
			if LocalFeishuCleanupPending(root) {
				t.Fatal("cleanup journal survived successful resume")
			}
		})
	}
}
