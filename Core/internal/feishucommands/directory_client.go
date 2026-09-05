package feishucommands

import (
	"errors"
	"strconv"
	"strings"

	"ksfassistant/core/internal/feishu"
)

func (call *invocation) runDirectoryClient(settings feishu.Settings, capabilities *feishu.CapabilityService, kind feishu.DirectoryKind, action string, arguments []string, store feishu.ClientConfigStore, write clientJSONWriter) error {
	enabled := settings.Directory.Enabled
	label := "directory"
	if kind == feishu.GroupDirectory {
		enabled = settings.GroupDirectory.Enabled
		label = "groupDirectory"
	}
	config, err := store.Load()
	if err != nil {
		return err
	}
	service := feishu.NewDirectoryService(capabilities)
	switch action {
	case "status":
		bindings := len(config.NameBindings)
		if kind == feishu.GroupDirectory {
			bindings = len(config.GroupNameBindings)
		}
		return write(map[string]any{"status": "ok", label: map[string]any{"enabled": enabled, "mode": "bounded_online_exact_lookup", "bindingCount": bindings}})
	case "search":
		if !enabled {
			return errors.New(label + " feature is disabled")
		}
		query, err := clientFlag(arguments, "--query")
		if err != nil {
			return err
		}
		matches, err := service.Search(call.ctx, kind, query, clientOptionalInteger(arguments, "--limit", 20))
		if err != nil {
			return err
		}
		return write(feishu.PublicResult(map[string]any{"status": "ok", "query": query, "matches": matches}))
	case "bind":
		if !enabled {
			return errors.New(label + " feature is disabled")
		}
		name, err := clientFlag(arguments, "--name")
		if err != nil {
			return err
		}
		candidate, err := clientFlag(arguments, "--candidate")
		if err != nil {
			return err
		}
		resolution, err := service.Bind(call.ctx, kind, name, candidate, store)
		if err != nil {
			return err
		}
		return write(feishu.PublicResult(map[string]any{"status": "bound", "name": strings.TrimSpace(name), "target": resolution.Candidate}))
	case "unbind":
		name, err := clientFlag(arguments, "--name")
		if err != nil {
			return err
		}
		existed, err := service.Unbind(kind, name, store)
		if err != nil {
			return err
		}
		status := "not_bound"
		if existed {
			status = "unbound"
		}
		return write(map[string]any{"status": status, "name": strings.TrimSpace(name)})
	default:
		return errors.New("directory action must be status, search, bind, or unbind")
	}
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
