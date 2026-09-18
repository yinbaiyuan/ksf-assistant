package feishu

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"ksfassistant/core/internal/capabilitypolicy"
	"ksfassistant/core/internal/privatestore"
)

type logoutCleanupJournal struct {
	SchemaVersion int    `json:"schemaVersion"`
	Stage         string `json:"stage"`
	StartedAt     string `json:"startedAt"`
}

var (
	purgeManagedPlatformCredentials = purgeManagedPlatformCredentialStorage
	removeManagedTree               = os.RemoveAll
	removeManagedFile               = os.Remove
)

func logoutCleanupPath(dataRoot string) string {
	return filepath.Join(dataRoot, "private-cache", "feishu-logout-cleanup-v1.json")
}

func LocalFeishuCleanupPending(dataRoot string) bool {
	_, err := os.Lstat(logoutCleanupPath(dataRoot))
	return !errors.Is(err, os.ErrNotExist)
}

func RecoverPendingLocalFeishuCleanup(dataRoot string) error {
	if !LocalFeishuCleanupPending(dataRoot) {
		return nil
	}
	return PurgeLocalFeishuState(dataRoot)
}

func BeginLocalFeishuCleanup(dataRoot string) error {
	if !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot {
		return errors.New("飞书本地数据目录无效")
	}
	journal := logoutCleanupJournal{SchemaVersion: 1, Stage: "local_cleanup", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := privatestore.WriteJSON(logoutCleanupPath(dataRoot), journal); err != nil {
		return err
	}
	return capabilitypolicy.SignOut(dataRoot)
}

// PurgeLocalFeishuState removes only authentication and routing material owned
// by KSFAssistant. Task links, Codex tasks, and append-only audit/history files
// are deliberately outside this list. Identity-dependent channel switches are
// reset so they cannot silently carry an old application's trust boundary into
// the next connection.
func PurgeLocalFeishuState(dataRoot string) error {
	if !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot {
		return errors.New("飞书本地数据目录无效")
	}
	if err := BeginLocalFeishuCleanup(dataRoot); err != nil {
		return err
	}
	CancelUserAuthFlow(dataRoot)
	CancelAppConfiguration(dataRoot)
	configDir, err := ManagedLarkCLIConfigDir(dataRoot)
	if err != nil || filepath.Dir(configDir) != dataRoot {
		return errors.New("受管飞书凭据目录校验失败")
	}
	managedTrees := []string{
		configDir,
		filepath.Join(dataRoot, "auth"),
	}
	for _, path := range managedTrees {
		if err := removeManagedTree(path); err != nil {
			return err
		}
	}
	if err := purgePlatformOfficialCredentials(dataRoot); err != nil {
		return err
	}
	localCredentialPaths := []string{
		filepath.Join(dataRoot, "client.json"),
		filepath.Join(dataRoot, SetupFilename),
		filepath.Join(dataRoot, "credentials", "official-sdk.json"),
		registrationOperatorRecoveryPath(dataRoot),
		progressiveAuthorizationRequestPath(dataRoot),
		progressiveAuthorizationRequestPath(dataRoot) + ".lock",
		legacyMigrationJournalPath(dataRoot),
	}
	for _, path := range localCredentialPaths {
		if err := removeManagedFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := purgeManagedPlatformCredentials(); err != nil {
		return err
	}
	settingsStore := NewSettingsStore(dataRoot)
	settings, err := settingsStore.Load()
	if err != nil {
		return err
	}
	if settings.Group.Enabled || settings.MailEvents.Enabled {
		settings.Group.Enabled = false
		settings.MailEvents.Enabled = false
		if err := settingsStore.Save(settings); err != nil {
			return err
		}
	}
	for _, path := range append(managedTrees, localCredentialPaths...) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("本地飞书认证数据清理未完成")
		}
	}
	if err := removeManagedFile(filepath.Join(dataRoot, capabilitypolicy.SessionFilename)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeManagedFile(logoutCleanupPath(dataRoot))
}
