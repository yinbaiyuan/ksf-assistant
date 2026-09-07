package feishucommands

import (
	"errors"
	"strconv"

	"ksfassistant/core/internal/feishu"
)

func (call *invocation) runDirectoryClient(settings feishu.Settings, capabilities *feishu.CapabilityService, kind feishu.DirectoryKind, action string, arguments []string, store feishu.ClientConfigStore, write clientJSONWriter) error {
	return errors.New("legacy_directory_resolution_retired: use the contact or IM Skill")
}

func clientOptionalInteger(arguments []string, flag string, fallback int) int {
	value := clientOptionalFlag(arguments, flag)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
