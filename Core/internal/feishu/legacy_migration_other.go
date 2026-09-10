//go:build !darwin && !windows

package feishu

import "errors"

func loadLegacyOfficialCredentials(string, string, larkProfile) (OfficialCredentials, error) {
	return OfficialCredentials{}, errors.New("此平台不支持旧 lark-cli 凭据迁移")
}

func removeLegacyPlatformCredentials([]string) error { return nil }
