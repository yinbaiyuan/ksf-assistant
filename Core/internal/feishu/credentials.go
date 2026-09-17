package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const masterKeyBytes = 32

type OfficialCredentials struct {
	AppID     string
	AppSecret string
	Brand     string
	Source    string
}

type larkConfig struct {
	CurrentApp string        `json:"currentApp"`
	Apps       []larkProfile `json:"apps"`
}

type larkProfile struct {
	Name      string          `json:"name"`
	AppID     string          `json:"appId"`
	AppSecret json.RawMessage `json:"appSecret"`
	Brand     string          `json:"brand"`
}

type secretReference struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Ref    struct {
		Source string `json:"source"`
		ID     string `json:"id"`
	} `json:"ref"`
}

func LoadOfficialCredentials() (OfficialCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return OfficialCredentials{}, err
	}
	dataRoot := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_DATA_DIR"))
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".config", "feishu-bridge")
	}
	if _, nativePathErr := os.Lstat(officialCredentialPath(dataRoot)); nativePathErr == nil {
		return loadPlatformOfficialCredentials(dataRoot)
	} else if !errors.Is(nativePathErr, os.ErrNotExist) {
		return OfficialCredentials{}, nativePathErr
	}
	// Existing installations are migrated lazily. Reading the previous managed
	// profile here is compatibility only; the CLI is no longer executed or
	// packaged and every new credential is written to the product-owned store.
	if runtime.GOOS == "windows" {
		return loadWindowsOfficialCredentials(home)
	}
	configDir, err := ManagedLarkCLIConfigDir(dataRoot)
	if err != nil {
		return OfficialCredentials{}, err
	}
	return loadLarkProfile(filepath.Join(configDir, "config.json"), home, "default", readPlatformMasterKey)
}

// LoadOfficialCredentialsForApp makes the application already bound by
// KSFAssistant authoritative. A stale native credential may be replaced only
// by the product-owned legacy default profile for that exact application.
func LoadOfficialCredentialsForApp(dataRoot, expectedAppID string) (OfficialCredentials, error) {
	if runtime.GOOS != "windows" {
		return LoadOfficialCredentials()
	}
	expectedAppID = strings.TrimSpace(expectedAppID)
	if expectedAppID == "" {
		return OfficialCredentials{}, errors.New("bound Feishu application is unavailable")
	}
	if credentials, err := loadPlatformOfficialCredentials(dataRoot); err == nil && credentials.AppID == expectedAppID {
		return credentials, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return OfficialCredentials{}, err
	}
	var legacy OfficialCredentials
	if runtime.GOOS == "windows" {
		legacy, err = loadWindowsOfficialCredentials(home)
	} else {
		configDir, pathErr := ManagedLarkCLIConfigDir(dataRoot)
		if pathErr != nil {
			return OfficialCredentials{}, pathErr
		}
		legacy, err = loadLarkProfile(filepath.Join(configDir, "config.json"), home, "default", readPlatformMasterKey)
	}
	if err != nil {
		return OfficialCredentials{}, errors.New("bound Feishu application credential is unavailable")
	}
	if legacy.AppID != expectedAppID {
		return OfficialCredentials{}, errors.New("stored Feishu credential does not match the bound application")
	}
	if err := StoreOfficialCredentials(dataRoot, legacy.AppID, legacy.AppSecret, legacy.Brand); err != nil {
		return OfficialCredentials{}, err
	}
	legacy.Source = "migrated-native-store"
	return legacy, nil
}

func StoreOfficialCredentials(dataRoot, appID, appSecret, brand string) error {
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" {
		return errors.New("official Feishu credentials are incomplete")
	}
	return storePlatformOfficialCredentials(dataRoot, strings.TrimSpace(appID), appSecret, brandOrDefault(brand))
}

func officialCredentialPath(dataRoot string) string {
	return filepath.Join(dataRoot, "credentials", "official-sdk.json")
}

func loadLarkProfile(configPath, home, requested string, masterKeyReader func() ([]byte, error)) (OfficialCredentials, error) {
	return loadLarkProfileForService(configPath, home, requested, ManagedLarkCLIKeyService, masterKeyReader)
}

func loadLarkProfileForService(configPath, home, requested, keyService string, masterKeyReader func() ([]byte, error)) (OfficialCredentials, error) {
	if err := validatePrivateRegularFile(configPath); err != nil {
		return OfficialCredentials{}, fmt.Errorf("lark-cli config: %w", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return OfficialCredentials{}, err
	}
	var config larkConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return OfficialCredentials{}, errors.New("lark-cli config is invalid")
	}
	profile, err := selectLarkProfile(config, strings.TrimSpace(requested))
	if err != nil {
		return OfficialCredentials{}, err
	}
	if strings.TrimSpace(profile.AppID) == "" {
		return OfficialCredentials{}, errors.New("lark-cli profile has no app id")
	}
	var plain string
	if json.Unmarshal(profile.AppSecret, &plain) == nil {
		if plain == "" {
			return OfficialCredentials{}, errors.New("lark-cli profile has no app secret")
		}
		return OfficialCredentials{AppID: profile.AppID, AppSecret: plain, Brand: brandOrDefault(profile.Brand), Source: "lark-cli-config"}, nil
	}
	var reference secretReference
	if json.Unmarshal(profile.AppSecret, &reference) != nil {
		return OfficialCredentials{}, errors.New("lark-cli profile has an unsupported app secret reference")
	}
	source, identifier := reference.Ref.Source, reference.Ref.ID
	if source == "" && identifier == "" {
		source, identifier = reference.Source, reference.ID
	}
	if source != "keychain" || identifier != "appsecret:"+profile.AppID {
		return OfficialCredentials{}, errors.New("lark-cli app id and app secret reference do not match")
	}
	storageDir := filepath.Join(home, "Library", "Application Support", keyService)
	encryptedPath := filepath.Join(storageDir, safeCredentialFilename(identifier))
	if err := validatePrivateRegularFile(encryptedPath); err != nil {
		return OfficialCredentials{}, err
	}
	encrypted, err := os.ReadFile(encryptedPath)
	if err != nil {
		return OfficialCredentials{}, err
	}
	key, err := loadMasterKey(storageDir, masterKeyReader)
	if err != nil {
		return OfficialCredentials{}, err
	}
	defer clear(key)
	defer clear(encrypted)
	secret, err := decryptCredential(encrypted, key)
	if err != nil {
		return OfficialCredentials{}, err
	}
	return OfficialCredentials{AppID: profile.AppID, AppSecret: secret, Brand: brandOrDefault(profile.Brand), Source: "lark-cli-keychain"}, nil
}

func loadMasterKey(storageDir string, fallback func() ([]byte, error)) ([]byte, error) {
	path := filepath.Join(storageDir, "master.key.file")
	if _, err := os.Lstat(path); err == nil {
		if err := validatePrivateRegularFile(path); err != nil {
			return nil, err
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(key) != masterKeyBytes {
			clear(key)
			return nil, errors.New("lark-cli master key file is invalid")
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return fallback()
}

func selectLarkProfile(config larkConfig, requested string) (larkProfile, error) {
	wanted := requested
	if wanted == "" {
		wanted = config.CurrentApp
	}
	if wanted != "" {
		for _, profile := range config.Apps {
			if profile.Name == wanted {
				return profile, nil
			}
		}
		return larkProfile{}, fmt.Errorf("lark-cli profile is not configured: %s", wanted)
	}
	if len(config.Apps) == 1 {
		return config.Apps[0], nil
	}
	if len(config.Apps) == 0 {
		return larkProfile{}, errors.New("lark-cli has no configured application profile")
	}
	return larkProfile{}, errors.New("lark-cli profile is ambiguous; set LARK_CLI_PROFILE")
}

func decryptCredential(data, key []byte) (string, error) {
	if len(key) != masterKeyBytes || len(data) < 28 {
		return "", errors.New("lark-cli encrypted credential is invalid")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, data[:12], data[12:], nil)
	if err != nil {
		return "", errors.New("lark-cli encrypted credential could not be decrypted")
	}
	defer clear(plain)
	if len(plain) == 0 {
		return "", errors.New("lark-cli encrypted credential is empty")
	}
	return string(plain), nil
}

func decodeKeychainEnvelope(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "go-keyring-base64:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "go-keyring-base64:"))
		if err != nil {
			return nil, err
		}
		value = string(decoded)
		clear(decoded)
	}
	if strings.HasPrefix(value, "go-keyring-encoded:") {
		decoded, err := hex.DecodeString(strings.TrimPrefix(value, "go-keyring-encoded:"))
		if err != nil {
			return nil, err
		}
		value = string(decoded)
		clear(decoded)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil || len(key) != masterKeyBytes {
		clear(key)
		return nil, errors.New("lark-cli master key is invalid")
	}
	return key, nil
}

func validatePrivateRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("credential path must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("credential permissions allow group or other access")
	}
	return nil
}
func safeCredentialFilename(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			return r
		}
		return '_'
	}, value) + ".enc"
}
func brandOrDefault(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "lark" {
		return "lark"
	}
	return "feishu"
}
