package usercommand

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validateFrozen(command Command, parsed parsed) error {
	if err := validateArtifactPlan(command.ArtifactPlan, parsed); err != nil {
		return err
	}
	if !utf8.Valid(command.Stdin) || strings.ContainsRune(string(command.Stdin), 0) {
		return errors.New("user_command_input_invalid")
	}
	files := map[string]File{}
	total := len(command.Stdin)
	for _, argument := range command.Args {
		total += len(argument)
	}
	for _, file := range command.Files {
		parts := strings.Split(file.Name, "/")
		if len(parts) != 2 || !safeIdentifier(parts[0]) || !strings.HasPrefix(parts[0], "frozen-") || parts[1] != file.DisplayName || parts[1] == "." || parts[1] == ".." || parts[1] == "" {
			return errors.New("user_command_file_invalid")
		}
		if len(file.DisplayName) > 255 || !utf8.ValidString(file.DisplayName) || strings.ContainsAny(file.DisplayName, "/\\") {
			return errors.New("user_command_file_invalid")
		}
		for _, char := range file.DisplayName {
			if unicode.IsControl(char) {
				return errors.New("user_command_file_invalid")
			}
		}
		if _, exists := files[file.Name]; exists {
			return errors.New("user_command_file_duplicate")
		}
		files[file.Name] = file
		total += len(file.Data)
	}
	if total > MaxContentBytes {
		return errors.New("user_command_too_large")
	}
	used := map[string]bool{}
	stdinUsed := false
	resolved := map[string][]string{}
	for _, name := range sortedFlagNames(parsed) {
		flag, _ := descriptorFlag(*parsed.spec.descriptor, name)
		for _, original := range parsed.values[name] {
			value := original
			if includes("file media-file multipart", flag.Role) {
				_, path, existing, err := fileArgument(flag, value)
				if err != nil {
					return err
				}
				if !existing {
					if _, exists := files[path]; !exists {
						return errors.New("user_command_file_not_frozen")
					}
					used[path] = true
				}
			} else if len(flag.Input) > 0 {
				if value == "-" {
					if !contains(flag.Input, "stdin") || stdinUsed {
						return errors.New("user_command_stdin_conflict")
					}
					stdinUsed = true
					value = string(command.Stdin)
				} else if strings.HasPrefix(value, "@@") {
					value = value[1:]
				} else if strings.HasPrefix(value, "@") {
					file, exists := files[value[1:]]
					if !contains(flag.Input, "file") || !exists {
						return errors.New("user_command_input_not_frozen")
					}
					used[file.Name] = true
					value = string(file.Data)
				}
			}
			if flag.Role == "artifact" && !safeArtifactPath(value) {
				return errors.New("user_command_artifact_path_unsafe")
			}
			if flag.Role == "json" {
				if err := businessJSON(value); err != nil {
					return err
				}
			}
			if flag.Role == "resource-text" && implicitResource(value) {
				return errors.New("user_command_dynamic_resource_unsupported")
			}
			if flag.Role == "restricted" {
				return ErrUnsupported
			}
			resolved[name] = append(resolved[name], value)
		}
	}
	if len(used) != len(files) || len(command.Stdin) > 0 && !stdinUsed {
		return errors.New("user_command_unexpected_input")
	}
	if err := validateConditions(parsed, resolved); err != nil {
		return err
	}
	if parsed.spec.path == "sheets +batch-update" {
		_, err := sheetBatchCommands(command, parsed)
		return err
	}
	if parsed.spec.descriptor.Kind == "typed" {
		return validateTypedInput(*parsed.spec.descriptor, resolved)
	}
	if parsed.spec.descriptor.Kind == "api" {
		return validateAPIInput(resolved)
	}
	return nil
}

func safeArtifactPath(value string) bool {
	if value == "" || strings.Contains(value, ":") || strings.Contains(value, "\\") || filepath.IsAbs(value) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || strings.HasPrefix(part, "frozen-") {
			return false
		}
		for _, char := range part {
			if unicode.IsControl(char) {
				return false
			}
		}
	}
	return true
}

func implicitResource(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "![") || strings.Contains(lower, "<image") || strings.Contains(lower, "<img") || strings.Contains(lower, "src=\"@") || strings.Contains(lower, "src='@") || strings.Contains(lower, "clipboard:")
}

func validateConditions(parsed parsed, resolved map[string][]string) error {
	value := func(name string) string {
		if entries := resolved[name]; len(entries) > 0 {
			return entries[len(entries)-1]
		}
		return ""
	}
	has := func(name string) bool { return value(name) != "" }
	path := parsed.spec.path
	if path == "base +record-share-link-create" {
		count := 0
		for _, value := range resolved["record-id"] {
			count += len(strings.Split(value, ","))
		}
		if count == 0 || count > 100 {
			return errors.New("user_command_record_share_limit")
		}
	}
	if path == "im +messages-send" || path == "im +chat-messages-list" {
		if has("chat-id") == has("user-id") {
			return errors.New("user_command_target_unresolved")
		}
	}
	if path == "im +messages-send" || path == "im +messages-reply" || path == "im +messages-edit" {
		count := 0
		for _, name := range strings.Fields("text content markdown image file video audio") {
			if has(name) {
				count++
			}
		}
		if count > 1 || count == 0 && !(path == "im +messages-edit" && (has("set-attachments") || value("clear-attachments") == "true")) {
			return errors.New("user_command_content_required")
		}
		if has("video") != has("video-cover") {
			return errors.New("user_command_video_cover_required")
		}
		if len(value("idempotency-key")) > 50 {
			return errors.New("user_command_idempotency_key_invalid")
		}
	}
	if path == "docs +create" || path == "docs +update" {
		if has("parent-token") && has("parent-position") {
			return errors.New("user_command_mutually_exclusive")
		}
		content := value("content")
		if content != "" && value("doc-format") != "markdown" {
			return errors.New("user_command_document_format_restricted")
		}
		if dynamicDocumentResource.MatchString(content) {
			return errors.New("user_command_dynamic_resource_unsupported")
		}
		if path == "docs +create" && strings.TrimSpace(content) == "" && !has("title") {
			return errors.New("user_command_content_required")
		}
		if path == "docs +update" {
			if !includes("append overwrite str_replace", value("command")) || value("command") == "" {
				return ErrUnsupported
			}
			if value("command") == "str_replace" && !has("pattern") {
				return errors.New("user_command_pattern_required")
			}
			if value("command") != "str_replace" && strings.TrimSpace(content) == "" {
				return errors.New("user_command_content_required")
			}
		}
	}
	if strings.HasPrefix(path, "sheets ") {
		_, token := descriptorFlag(*parsed.spec.descriptor, "spreadsheet-token")
		_, url := descriptorFlag(*parsed.spec.descriptor, "url")
		if token && url && has("spreadsheet-token") == has("url") {
			return errors.New("user_command_spreadsheet_locator_required")
		}
		if has("sheet-id") && has("sheet-name") {
			return errors.New("user_command_mutually_exclusive")
		}
	}
	if strings.HasPrefix(path, "mail ") {
		if has("body") && has("body-file") {
			return errors.New("user_command_mutually_exclusive")
		}
		if path == "mail +send" || path == "mail +draft-create" {
			if !has("to") || !has("subject") || !has("body") && !has("body-file") {
				return errors.New("user_command_mail_content_required")
			}
		}
	}
	if path == "slides +create" {
		if has("slide") && has("slides") {
			return errors.New("user_command_mutually_exclusive")
		}
		if len(resolved["slide"]) > 10 {
			return errors.New("user_command_slide_limit")
		}
		for _, slide := range resolved["slide"] {
			if strings.HasPrefix(slide, "@") {
				return errors.New("user_command_input_not_frozen")
			}
			if implicitResource(slide) {
				return errors.New("user_command_dynamic_resource_unsupported")
			}
		}
		if has("slides") {
			var slides []string
			if json.Unmarshal([]byte(value("slides")), &slides) != nil || len(slides) > 10 {
				return errors.New("user_command_slides_invalid")
			}
			for _, slide := range slides {
				if implicitResource(slide) {
					return errors.New("user_command_dynamic_resource_unsupported")
				}
			}
		}
	}
	if path == "base +form-submit" {
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(value("json")), &body) != nil {
			return errors.New("user_command_json_invalid")
		}
		if attachments, ok := body["attachments"]; ok && string(attachments) != "{}" && string(attachments) != "[]" && string(attachments) != "null" {
			return errors.New("user_command_dynamic_resource_unsupported")
		}
	}
	return nil
}

func validateAPIInput(values map[string][]string) error {
	for _, name := range []string{"data", "params"} {
		for _, value := range values[name] {
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(value), &object) != nil || object == nil {
				return errors.New("user_command_json_invalid")
			}
			for key := range object {
				normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
				if includes("as method path headers authorization accesstoken useraccesstoken tenantaccesstoken appsecret profile configdir", normalized) {
					return errors.New("user_command_api_override_refused")
				}
			}
		}
	}
	return nil
}

func validateTypedInput(descriptor ExecutionDescriptor, values map[string][]string) error {
	var schema struct {
		Properties map[string]struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Flag string `json:"flag"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if json.Unmarshal(descriptor.InputSchema, &schema) != nil {
		return errors.New("user_command_schema_invalid")
	}
	params := map[string]json.RawMessage{}
	if entries := values["params"]; len(entries) > 0 {
		if json.Unmarshal([]byte(entries[0]), &params) != nil || params == nil {
			return errors.New("user_command_json_invalid")
		}
	}
	for _, key := range schema.Properties["params"].Required {
		flag := strings.TrimPrefix(schema.Properties["params"].Properties[key].Flag, "--")
		if len(values[flag]) == 0 && len(params[key]) == 0 {
			return fmt.Errorf("user_command_required_field_missing: %s", key)
		}
	}
	for key, property := range schema.Properties["params"].Properties {
		flag := strings.TrimPrefix(property.Flag, "--")
		if len(values[flag]) > 0 && len(params[key]) > 0 {
			return fmt.Errorf("user_command_duplicate_parameter: %s", key)
		}
	}
	if entries := values["data"]; len(entries) > 0 {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(entries[0]), &object) != nil || object == nil {
			return errors.New("user_command_json_invalid")
		}
	} else if len(schema.Properties["data"].Required) > 0 && len(values["file"]) == 0 {
		return errors.New("user_command_body_required")
	}
	return nil
}
