//go:build darwin

package feishu

import (
	"errors"
	"os/exec"
)

func loadLegacyOfficialCredentials(path, home string, profile larkProfile) (OfficialCredentials, error) {
	return loadLarkProfileForService(path, home, legacyProfileName(profile), legacyLarkCLIKeyService, readLegacyPlatformMasterKey)
}

func readLegacyPlatformMasterKey() ([]byte, error) {
	output, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", legacyLarkCLIKeyService, "-a", "master.key", "-w").Output()
	if err != nil {
		return nil, errors.New("旧 lark-cli 主密钥不可用")
	}
	return decodeKeychainEnvelope(string(output))
}

func removeLegacyPlatformCredentials(accounts []string) error {
	for _, account := range accounts {
		command := exec.Command("/usr/bin/security", "delete-generic-password", "-s", legacyLarkCLIKeyService, "-a", account)
		if err := command.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 44 {
				return err
			}
		}
	}
	return nil
}
