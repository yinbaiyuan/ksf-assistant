//go:build !darwin && !windows

package feishu

import "errors"

func readPlatformMasterKey() ([]byte, error) {
	return nil, errors.New("platform credential storage is unsupported")
}
func loadWindowsOfficialCredentials(string) (OfficialCredentials, error) {
	return OfficialCredentials{}, errors.New("Windows credentials are unavailable")
}

func storePlatformOfficialCredentials(string, string, string, string) error { return nil }

func platformOfficialCredentialStatus() string { return "lark-cli-config" }
