//go:build darwin

package feishu

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
)

const nativeFeishuKeychainService = "com.ksfassistant.feishu"

type darwinOfficialCredentialFile struct {
	SchemaVersion int    `json:"schemaVersion"`
	AppID         string `json:"appId"`
	Brand         string `json:"brand"`
	Account       string `json:"account"`
}

func readPlatformMasterKey() ([]byte, error) {
	output, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", ManagedLarkCLIKeyService, "-a", "master.key", "-w").Output()
	if err != nil {
		return nil, errors.New("lark-cli master key is unavailable from the macOS Keychain")
	}
	return decodeKeychainEnvelope(string(output))
}

func loadWindowsOfficialCredentials(string) (OfficialCredentials, error) {
	return OfficialCredentials{}, errors.New("Windows credentials are unavailable on macOS")
}

func loadPlatformOfficialCredentials(dataRoot string) (OfficialCredentials, error) {
	if err := validatePrivateRegularFile(officialCredentialPath(dataRoot)); err != nil {
		return OfficialCredentials{}, err
	}
	data, err := os.ReadFile(officialCredentialPath(dataRoot))
	if err != nil {
		return OfficialCredentials{}, err
	}
	var stored darwinOfficialCredentialFile
	if json.Unmarshal(data, &stored) != nil || stored.SchemaVersion != 1 || strings.TrimSpace(stored.AppID) == "" || stored.Account != "appsecret:"+stored.AppID {
		return OfficialCredentials{}, errors.New("native Feishu credential is invalid")
	}
	output, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", nativeFeishuKeychainService, "-a", stored.Account, "-w").Output()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return OfficialCredentials{}, errors.New("native Feishu credential is unavailable from Keychain")
	}
	return OfficialCredentials{AppID: stored.AppID, AppSecret: strings.TrimSpace(string(output)), Brand: brandOrDefault(stored.Brand), Source: "macos-keychain"}, nil
}

func storePlatformOfficialCredentials(dataRoot, appID, appSecret, brand string) error {
	account := "appsecret:" + appID
	command := exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", nativeFeishuKeychainService, "-a", account, "-w", appSecret)
	if output, err := command.CombinedOutput(); err != nil {
		_ = output
		return errors.New("native Feishu credential could not be stored in Keychain")
	}
	if err := writePrivateJSON(officialCredentialPath(dataRoot), darwinOfficialCredentialFile{SchemaVersion: 1, AppID: appID, Brand: brandOrDefault(brand), Account: account}); err != nil {
		_ = exec.Command("/usr/bin/security", "delete-generic-password", "-s", nativeFeishuKeychainService, "-a", account).Run()
		return err
	}
	return nil
}

func platformOfficialCredentialStatus() string { return "macos-keychain" }

func purgePlatformOfficialCredentials(dataRoot string) error {
	data, err := os.ReadFile(officialCredentialPath(dataRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored darwinOfficialCredentialFile
	if json.Unmarshal(data, &stored) != nil || stored.Account == "" {
		return errors.New("native Feishu credential metadata is invalid")
	}
	command := exec.Command("/usr/bin/security", "delete-generic-password", "-s", nativeFeishuKeychainService, "-a", stored.Account)
	if output, err := command.CombinedOutput(); err != nil && !strings.Contains(strings.ToLower(string(output)), "could not be found") {
		return errors.New("native Feishu credential could not be removed from Keychain")
	}
	return nil
}
