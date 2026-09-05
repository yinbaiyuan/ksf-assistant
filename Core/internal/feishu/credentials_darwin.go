//go:build darwin

package feishu

import (
	"errors"
	"os/exec"
)

func readPlatformMasterKey() ([]byte, error) {
	output, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", "lark-cli", "-a", "master.key", "-w").Output()
	if err != nil {
		return nil, errors.New("lark-cli master key is unavailable from the macOS Keychain")
	}
	return decodeKeychainEnvelope(string(output))
}

func loadWindowsOfficialCredentials(string) (OfficialCredentials, error) {
	return OfficialCredentials{}, errors.New("Windows credentials are unavailable on macOS")
}

func storePlatformOfficialCredentials(string, string, string, string) error { return nil }

func platformOfficialCredentialStatus() string { return "lark-cli-keychain" }
