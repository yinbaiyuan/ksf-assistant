//go:build windows

package feishu

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	helper := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_CREDENTIAL_HELPER"))
	if helper == "" {
		helper = filepath.Join(os.Getenv("CODEX_USAGE_BAR_FEISHU_SERVICE_ROOT"), "windows", "Read-FeishuBridgeCredential.ps1")
	}
	if err := validatePrivateRegularFile(helper); err != nil {
		return OfficialCredentials{}, errors.New("Windows DPAPI credential helper is unavailable")
	}
	command := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", helper)
	command.Env = append(os.Environ(), "FEISHU_BRIDGE_CREDENTIAL_FILE="+absCredential)
	output, err := command.Output()
	if err != nil {
		return OfficialCredentials{}, errors.New("Windows DPAPI credential could not be decrypted for the current user")
	}
	var value struct {
		AppID     string `json:"appId"`
		AppSecret string `json:"appSecret"`
		Brand     string `json:"brand"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(output), "\ufeff")), &value); err != nil || strings.TrimSpace(value.AppID) == "" || value.AppSecret == "" {
		return OfficialCredentials{}, errors.New("Windows DPAPI credential returned invalid data")
	}
	return OfficialCredentials{AppID: strings.TrimSpace(value.AppID), AppSecret: value.AppSecret, Brand: brandOrDefault(value.Brand), Source: "windows-dpapi"}, nil
}
