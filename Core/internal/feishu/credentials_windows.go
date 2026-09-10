//go:build windows

package feishu

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type windowsOfficialCredentialFile struct {
	SchemaVersion   int    `json:"schemaVersion"`
	AppID           string `json:"appId"`
	Brand           string `json:"brand"`
	ProtectedSecret string `json:"protectedSecret"`
}

func readPlatformMasterKey() ([]byte, error) {
	return nil, errors.New("macOS Keychain is unavailable on Windows")
}
func loadWindowsOfficialCredentials(_ string) (OfficialCredentials, error) {
	dataRoot := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_DATA_DIR"))
	configDir, err := ManagedLarkCLIConfigDir(dataRoot)
	if err != nil {
		return OfficialCredentials{}, err
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := validatePrivateRegularFile(configPath); err != nil {
		return OfficialCredentials{}, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return OfficialCredentials{}, err
	}
	var config larkConfig
	if json.Unmarshal(data, &config) != nil {
		return OfficialCredentials{}, errors.New("lark-cli config is invalid")
	}
	profile, err := selectLarkProfile(config, "default")
	if err != nil {
		return OfficialCredentials{}, err
	}
	var reference secretReference
	if json.Unmarshal(profile.AppSecret, &reference) != nil {
		return OfficialCredentials{}, errors.New("lark-cli profile has an unsupported app secret reference")
	}
	source, account := reference.Ref.Source, reference.Ref.ID
	if source == "" && account == "" {
		source, account = reference.Source, reference.ID
	}
	if source != "keychain" || account != "appsecret:"+profile.AppID {
		return OfficialCredentials{}, errors.New("lark-cli app id and app secret reference do not match")
	}
	secret, err := readWindowsKeychainAccount(ManagedLarkCLIKeyService, account)
	if err != nil {
		return OfficialCredentials{}, err
	}
	return OfficialCredentials{AppID: strings.TrimSpace(profile.AppID), AppSecret: secret, Brand: brandOrDefault(profile.Brand), Source: "lark-cli-keychain"}, nil
}

func readWindowsKeychainAccount(service, account string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\LarkCli\keychain\`+service, registry.QUERY_VALUE)
	if err != nil {
		return "", errors.New("lark-cli Windows credential is unavailable")
	}
	defer key.Close()
	encoded, _, err := key.GetStringValue(base64.RawURLEncoding.EncodeToString([]byte(account)))
	if err != nil {
		return "", errors.New("lark-cli Windows credential is unavailable")
	}
	protected, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("lark-cli Windows credential is invalid")
	}
	decrypted, err := unprotectWindowsCredentialWithEntropy(protected, []byte(service+"\x00"+account))
	clear(protected)
	if err != nil || len(decrypted) == 0 {
		clear(decrypted)
		return "", errors.New("lark-cli Windows credential could not be decrypted for the current user")
	}
	secret := string(decrypted)
	clear(decrypted)
	return secret, nil
}

func storePlatformOfficialCredentials(dataRoot, appID, appSecret, brand string) error {
	dataRoot = strings.TrimSpace(dataRoot)
	if dataRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dataRoot = filepath.Join(home, ".config", "feishu-bridge")
	}
	plain := encodeWindowsCredentialSecret(appSecret)
	defer clear(plain)
	protected, err := protectWindowsCredential(plain)
	if err != nil {
		return errors.New("Windows DPAPI credential could not be encrypted for the current user")
	}
	defer clear(protected)
	return writePrivateJSON(filepath.Join(dataRoot, "credentials", "official-sdk.json"), windowsOfficialCredentialFile{
		SchemaVersion:   1,
		AppID:           strings.TrimSpace(appID),
		Brand:           brandOrDefault(brand),
		ProtectedSecret: hex.EncodeToString(protected),
	})
}

func platformOfficialCredentialStatus() string { return "windows-dpapi" }

func protectWindowsCredential(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("empty Windows credential")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer freeWindowsDataBlob(out)
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func unprotectWindowsCredential(protected []byte) ([]byte, error) {
	return unprotectWindowsCredentialWithEntropy(protected, nil)
}

func unprotectWindowsCredentialWithEntropy(protected, entropy []byte) ([]byte, error) {
	if len(protected) == 0 {
		return nil, errors.New("empty Windows credential")
	}
	in := windows.DataBlob{Size: uint32(len(protected)), Data: &protected[0]}
	var entropyBlob *windows.DataBlob
	if len(entropy) > 0 {
		entropyBlob = &windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, entropyBlob, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer freeWindowsDataBlob(out)
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func freeWindowsDataBlob(blob windows.DataBlob) {
	if blob.Data != nil {
		_, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(blob.Data))))
	}
}

func encodeWindowsCredentialSecret(secret string) []byte {
	units := utf16.Encode([]rune(secret))
	data := make([]byte, len(units)*2)
	for index, unit := range units {
		data[index*2] = byte(unit)
		data[index*2+1] = byte(unit >> 8)
	}
	clear(units)
	return data
}

func decodeWindowsCredentialSecret(data []byte) (string, error) {
	if len(data) == 0 || len(data)%2 != 0 {
		return "", errors.New("Windows DPAPI credential plaintext is invalid")
	}
	units := make([]uint16, len(data)/2)
	for index := range units {
		units[index] = uint16(data[index*2]) | uint16(data[index*2+1])<<8
	}
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	if len(units) == 0 {
		return "", errors.New("Windows DPAPI credential plaintext is empty")
	}
	secret := string(utf16.Decode(units))
	clear(units)
	if secret == "" {
		return "", errors.New("Windows DPAPI credential plaintext is empty")
	}
	return secret, nil
}
