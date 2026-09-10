package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ksfassistant/core/internal/privatestore"
)

const legacyLarkCLIKeyService = "lark-cli"

type legacyMigrationJournal struct {
	SchemaVersion int      `json:"schemaVersion"`
	ApplicationID string   `json:"applicationId"`
	Profile       string   `json:"profile"`
	Accounts      []string `json:"accounts"`
}

func legacyMigrationJournalPath(dataRoot string) string {
	return filepath.Join(dataRoot, "private-cache", "lark-cli-legacy-migration-v1.json")
}

// MigrateOwnedLegacyProfile imports only a provably KSFAssistant-owned default
// profile. Ambiguous or unrelated lark-cli data is never read beyond the
// minimum non-secret ownership evidence and is never changed.
func MigrateOwnedLegacyProfile(ctx context.Context, runner CapabilityExecutor, dataRoot string) error {
	if resumed, err := resumeLegacyMigrationCleanup(dataRoot); resumed || err != nil {
		return err
	}
	managed, _, err := configurationFileEvidenceFor(dataRoot)
	if err != nil || managed != "missing" {
		return err
	}
	setup, err := NewSetupStore(dataRoot).Load()
	if err != nil || setup.Stage == SetupNotStarted || setup.Stage == SetupAppPending {
		return nil
	}
	client, err := NewClientConfigStore(dataRoot).Load()
	if err != nil || client.Operator == nil || client.Operator.AppID == "" || client.Operator.OpenID == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacyPath := filepath.Join(home, ".lark-cli", "config.json")
	data, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var legacy larkConfig
	if json.Unmarshal(data, &legacy) != nil {
		return nil
	}
	profile, err := selectLarkProfile(legacy, "default")
	if err != nil || profile.AppID != client.Operator.AppID || brandOrDefault(profile.Brand) != "feishu" {
		return nil
	}
	credential, err := loadLegacyOfficialCredentials(legacyPath, home, profile)
	if err != nil {
		return err
	}
	if _, err := ConfigureExistingApp(ctx, runner, credential.AppID, credential.AppSecret, credential.Brand, "default"); err != nil {
		return err
	}
	verified, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 15*time.Second)
	if err != nil || verified["appId"] != profile.AppID || verified["brand"] != "feishu" {
		return errors.New("隔离飞书凭据迁移验证失败")
	}
	accounts := []string{"appsecret:" + profile.AppID, profile.AppID + ":" + client.Operator.OpenID, legacyTenantTokenAccount(profile.AppID)}
	journal := legacyMigrationJournal{SchemaVersion: 1, ApplicationID: profile.AppID, Profile: "default", Accounts: accounts}
	if err := privatestore.WriteJSON(legacyMigrationJournalPath(dataRoot), journal); err != nil {
		return err
	}
	return finishLegacyMigrationCleanup(dataRoot, journal)
}

func resumeLegacyMigrationCleanup(dataRoot string) (bool, error) {
	var journal legacyMigrationJournal
	missing, err := privatestore.ReadJSON(legacyMigrationJournalPath(dataRoot), &journal)
	if err != nil || missing {
		return !missing, err
	}
	if journal.SchemaVersion != 1 || journal.ApplicationID == "" || journal.Profile != "default" || len(journal.Accounts) != 3 {
		return true, errors.New("旧 lark-cli 迁移恢复记录无效")
	}
	managed, _, evidenceErr := configurationFileEvidenceFor(dataRoot)
	if evidenceErr != nil || managed != "present" {
		return true, errors.New("隔离飞书凭据迁移结果已丢失，拒绝清理旧 profile")
	}
	return true, finishLegacyMigrationCleanup(dataRoot, journal)
}

func finishLegacyMigrationCleanup(dataRoot string, journal legacyMigrationJournal) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacyPath := filepath.Join(home, ".lark-cli", "config.json")
	if data, err := os.ReadFile(legacyPath); err == nil {
		var config larkConfig
		if json.Unmarshal(data, &config) != nil {
			return errors.New("旧 lark-cli 配置在迁移后变得不可读")
		}
		profile, selectErr := selectLarkProfile(config, journal.Profile)
		if selectErr == nil {
			if profile.AppID != journal.ApplicationID {
				return errors.New("旧 lark-cli default profile 在迁移期间已变化")
			}
			if err := removeLegacyProfile(legacyPath, data, journal.Profile); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := removeLegacyPlatformCredentials(journal.Accounts); err != nil {
		return err
	}
	err = os.Remove(legacyMigrationJournalPath(dataRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func legacyTenantTokenAccount(appID string) string {
	sum := sha256.Sum256([]byte(appID))
	return "tat:v1:" + hex.EncodeToString(sum[:])
}

func removeLegacyProfile(path string, original []byte, name string) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(original, &root) != nil {
		return errors.New("旧 lark-cli 配置无效")
	}
	var apps []json.RawMessage
	if json.Unmarshal(root["apps"], &apps) != nil {
		return errors.New("旧 lark-cli profile 列表无效")
	}
	kept := make([]json.RawMessage, 0, len(apps))
	removed := false
	for _, raw := range apps {
		var item struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &item) == nil && item.Name == name {
			removed = true
			continue
		}
		kept = append(kept, raw)
	}
	if !removed {
		return errors.New("旧 lark-cli default profile 已变化")
	}
	if len(kept) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	root["apps"], _ = json.Marshal(kept)
	var current string
	_ = json.Unmarshal(root["currentApp"], &current)
	if current == name {
		delete(root, "currentApp")
	}
	return writePrivateJSON(path, root)
}

func legacyProfileName(profile larkProfile) string {
	if strings.TrimSpace(profile.Name) == "" {
		return "default"
	}
	return profile.Name
}
