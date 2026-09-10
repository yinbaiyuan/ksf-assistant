package feishu

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLarkProfileReusesEncryptedCredential(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".lark-cli")
	storageDir := filepath.Join(home, "Library", "Application Support", ManagedLarkCLIKeyService)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := []byte("0123456789ab")
	encrypted := append(append([]byte{}, nonce...), gcm.Seal(nil, nonce, []byte("secret-value"), nil)...)
	if err := os.WriteFile(filepath.Join(storageDir, "appsecret_cli_test.enc"), encrypted, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storageDir, "master.key.file"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"currentApp": "default", "apps": []any{map[string]any{"name": "default", "appId": "cli_test", "brand": "feishu", "appSecret": map[string]any{"ref": map[string]any{"source": "keychain", "id": "appsecret:cli_test"}}}}}
	data, _ := json.Marshal(config)
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := loadLarkProfile(configPath, home, "", func() ([]byte, error) { t.Fatal("unexpected Keychain read"); return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if credential.AppID != "cli_test" || credential.AppSecret != "secret-value" || credential.Source != "lark-cli-keychain" {
		t.Fatalf("unexpected credential: %#v", credential)
	}
}

func TestDirectLarkKeychainReferenceIsAccepted(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	storage := filepath.Join(home, "Library", "Application Support", ManagedLarkCLIKeyService)
	if err := os.MkdirAll(storage, 0o700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, masterKeyBytes)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := bytes.Repeat([]byte{3}, gcm.NonceSize())
	encrypted := append(append([]byte{}, nonce...), gcm.Seal(nil, nonce, []byte("secret-value"), nil)...)
	if err := os.WriteFile(filepath.Join(storage, safeCredentialFilename("appsecret:app_123")), encrypted, 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"currentApp":"default","apps":[{"name":"default","appId":"app_123","appSecret":{"source":"keychain","id":"appsecret:app_123"},"brand":"feishu"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := loadLarkProfile(configPath, home, "", func() ([]byte, error) { return append([]byte{}, key...), nil })
	if err != nil {
		t.Fatal(err)
	}
	if credential.AppSecret != "secret-value" {
		t.Fatal("credential was not decrypted")
	}
}
