//go:build windows

package feishu

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
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
func loadWindowsOfficialCredentials(home string) (OfficialCredentials, error) {
	dataRoot := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_DATA_DIR"))
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".config", "feishu-bridge")
	}
	credentialPath := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_CREDENTIAL_FILE"))
	if credentialPath == "" {
		credentialPath = filepath.Join(dataRoot, "credentials", "official-sdk.json")
	}
	absRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return OfficialCredentials{}, err
	}
	absCredential, err := filepath.Abs(credentialPath)
	if err != nil {
		return OfficialCredentials{}, err
	}
	relative, err := filepath.Rel(absRoot, absCredential)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return OfficialCredentials{}, errors.New("Windows bridge credential must stay inside FEISHU_BRIDGE_DATA_DIR")
	}
	if err := validatePrivateRegularFile(absCredential); err != nil {
		return OfficialCredentials{}, err
	}
	data, err := os.ReadFile(absCredential)
	if err != nil {
		return OfficialCredentials{}, err
	}
	var value windowsOfficialCredentialFile
	if err := json.Unmarshal(data, &value); err != nil || value.SchemaVersion != 1 || strings.TrimSpace(value.AppID) == "" || strings.TrimSpace(value.ProtectedSecret) == "" {
		return OfficialCredentials{}, errors.New("Windows DPAPI credential file is invalid")
	}
	protected, err := hex.DecodeString(strings.TrimSpace(value.ProtectedSecret))
	if err != nil || len(protected) == 0 {
		return OfficialCredentials{}, errors.New("Windows DPAPI credential payload is invalid")
	}
	decrypted, err := unprotectWindowsCredential(protected)
	clear(protected)
	if err != nil {
		return OfficialCredentials{}, errors.New("Windows DPAPI credential could not be decrypted for the current user")
	}
	defer clear(decrypted)
	secret, err := decodeWindowsCredentialSecret(decrypted)
	if err != nil {
		return OfficialCredentials{}, err
	}
	return OfficialCredentials{AppID: strings.TrimSpace(value.AppID), AppSecret: secret, Brand: brandOrDefault(value.Brand), Source: "windows-dpapi"}, nil
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
	if len(protected) == 0 {
		return nil, errors.New("empty Windows credential")
	}
	in := windows.DataBlob{Size: uint32(len(protected)), Data: &protected[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
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
