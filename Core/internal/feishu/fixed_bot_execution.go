package feishu

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type fixedBotOperation uint8

const (
	fixedBotResourceDownload fixedBotOperation = iota + 1
)

func (runner CapabilityExecutor) runFixedBotBusiness(ctx context.Context, operation fixedBotOperation, args []string, input []byte, timeout time.Duration) (map[string]any, error) {
	prefix, allowed, required := []string{}, "", ""
	switch operation {
	case fixedBotResourceDownload:
		prefix, allowed, required = []string{"im", "+messages-resources-download"}, "message-id file-key type output", "message-id file-key type output"
	default:
		return nil, errors.New("fixed_bot_operation_unsupported")
	}
	if len(args) < len(prefix) || strings.Join(args[:len(prefix)], " ") != strings.Join(prefix, " ") {
		return nil, errors.New("fixed_bot_command_mismatch")
	}
	flags := map[string]string{}
	for index := len(prefix); index < len(args); index += 2 {
		if index+1 >= len(args) || !strings.HasPrefix(args[index], "--") {
			return nil, errors.New("fixed_bot_flag_invalid")
		}
		name, value := strings.TrimPrefix(args[index], "--"), args[index+1]
		if !contains(strings.Fields(allowed), name) || value == "" || strings.HasPrefix(value, "--") || strings.ContainsRune(value, 0) {
			return nil, errors.New("fixed_bot_flag_invalid")
		}
		if _, found := flags[name]; found {
			return nil, errors.New("fixed_bot_flag_duplicate")
		}
		flags[name] = value
	}
	for _, name := range strings.Fields(required) {
		if flags[name] == "" {
			return nil, errors.New("fixed_bot_required_flag")
		}
	}
	switch operation {
	case fixedBotResourceDownload:
		if !regexp.MustCompile(`^om_[A-Za-z0-9_-]+$`).MatchString(flags["message-id"]) || len(flags["file-key"]) > 1024 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(flags["file-key"]) || !contains([]string{"image", "file"}, flags["type"]) || len(input) != 0 {
			return nil, errors.New("fixed_bot_target_invalid")
		}
		output := filepath.Clean(filepath.FromSlash(flags["output"]))
		if filepath.IsAbs(output) || output == "." || output == ".." || strings.HasPrefix(output, ".."+string(filepath.Separator)) {
			return nil, errors.New("fixed_bot_output_invalid")
		}
	}
	if runner.Profile != "" && runner.Profile != "default" {
		return nil, errors.New("fixed_bot_profile_invalid")
	}
	definition := CapabilityDefinition{Identity: "bot", Risk: "read", Command: prefix}
	full := append(append([]string{}, prefix...), "--as", "bot")
	full = append(full, args[len(prefix):]...)
	directory := runner.WorkingDirectory
	if directory == "" {
		directory = runner.DataRoot
	}
	return runner.runBusinessCommand(ctx, definition, full, input, directory, timeout)
}
