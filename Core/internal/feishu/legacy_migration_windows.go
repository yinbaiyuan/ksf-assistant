//go:build windows

package feishu

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func loadLegacyOfficialCredentials(_ string, _ string, profile larkProfile) (OfficialCredentials, error) {
	var reference secretReference
	if json.Unmarshal(profile.AppSecret, &reference) != nil {
		return OfficialCredentials{}, errors.New("旧 lark-cli App Secret 引用无效")
	}
	source, account := reference.Ref.Source, reference.Ref.ID
	if source == "" && account == "" {
		source, account = reference.Source, reference.ID
	}
	if source != "keychain" || account != "appsecret:"+profile.AppID {
		return OfficialCredentials{}, errors.New("旧 lark-cli App Secret 与 App ID 不匹配")
	}
	secret, err := readWindowsKeychainAccount(legacyLarkCLIKeyService, account)
	if err != nil {
		return OfficialCredentials{}, err
	}
	return OfficialCredentials{AppID: strings.TrimSpace(profile.AppID), AppSecret: secret, Brand: brandOrDefault(profile.Brand), Source: "legacy-lark-cli-keychain"}, nil
}

func removeLegacyPlatformCredentials(accounts []string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\LarkCli\keychain\`+legacyLarkCLIKeyService, registry.SET_VALUE)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	for _, account := range accounts {
		if err := key.DeleteValue(base64.RawURLEncoding.EncodeToString([]byte(account))); err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return err
		}
	}
	return nil
}
