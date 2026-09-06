package usercommand

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var boundedSheetBatchShortcuts = strings.Fields("+cells-set +cells-set-style +cells-clear +cells-replace +cells-merge +cells-unmerge +dropdown-set +dim-insert +dim-delete +dim-hide +dim-unhide +dim-freeze +dim-group +dim-ungroup +rows-resize +cols-resize +range-move +range-copy +range-fill +range-sort +sheet-create +sheet-delete +sheet-rename +sheet-move +sheet-copy +sheet-hide +sheet-unhide +sheet-set-tab-color +sheet-show-gridline +sheet-hide-gridline")

func sheetBatchCommands(command Command, parent parsed) ([]Command, error) {
	flag, _ := descriptorFlag(*parent.spec.descriptor, "operations")
	content := resolvedText(command, flag, parent.flags["operations"])
	if err := businessJSON(content); err != nil {
		return nil, err
	}
	var operations []map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &operations) != nil || len(operations) == 0 || len(operations) > 100 {
		return nil, errors.New("user_command_batch_limit_or_shape")
	}
	if parent.flags["continue-on-error"] == "true" {
		return nil, errors.New("user_command_batch_continue_on_error_restricted")
	}
	children := make([]Command, 0, len(operations))
	for index, operation := range operations {
		var shortcut string
		var input map[string]json.RawMessage
		if len(operation) != 2 || json.Unmarshal(operation["shortcut"], &shortcut) != nil || !contains(boundedSheetBatchShortcuts, shortcut) || json.Unmarshal(operation["input"], &input) != nil || input == nil {
			return nil, fmt.Errorf("user_command_batch_operation_restricted: %d", index)
		}
		descriptor, _, found := resolveDescriptor([]string{"sheets", shortcut})
		if !found || descriptor.Status != "supported" {
			return nil, ErrUnsupported
		}
		arguments := []string{"sheets", shortcut, "--as", parent.flags["as"]}
		for _, locator := range []string{"spreadsheet-token", "url"} {
			if parent.flags[locator] != "" {
				arguments = append(arguments, "--"+locator, parent.flags[locator])
			}
		}
		keys := make([]string, 0, len(input))
		for key := range input {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		seen := map[string]bool{}
		for _, key := range keys {
			name := strings.ReplaceAll(key, "_", "-")
			flag, exists := descriptorFlag(descriptor, name)
			if !exists || seen[flag.Name] || includes("control output-format restricted file media-file multipart artifact", flag.Role) && flag.Role != "" || includes("spreadsheet-token url token writes ranges heights widths print-schema flag-name operation", flag.Name) {
				return nil, fmt.Errorf("user_command_batch_input_restricted: %d/%s", index, key)
			}
			seen[flag.Name] = true
			value := string(input[key])
			var text string
			if json.Unmarshal(input[key], &text) == nil {
				value = text
			} else if flag.Type == "string" && flag.Role != "json" {
				return nil, errors.New("user_command_batch_input_type")
			}
			if len(flag.Input) > 0 && (value == "-" || strings.HasPrefix(value, "@")) {
				return nil, errors.New("user_command_batch_nested_input_restricted")
			}
			if flag.Type == "bool" {
				arguments = append(arguments, "--"+flag.Name+"="+value)
			} else {
				arguments = append(arguments, "--"+flag.Name, value)
			}
		}
		childParsed, err := parse(arguments)
		if err != nil || childParsed.local {
			return nil, fmt.Errorf("user_command_batch_arguments_invalid: %d: %v", index, err)
		}
		if shortcut == "+sheet-move" && (childParsed.flags["sheet-id"] == "" || childParsed.flags["source-index"] == "" || childParsed.flags["index"] == "") {
			return nil, errors.New("user_command_batch_sheet_move_target_required")
		}
		child := Command{Version: Version, Args: canonicalArguments(childParsed), Identity: command.Identity}
		if err := validateFrozen(child, childParsed); err != nil {
			return nil, fmt.Errorf("user_command_batch_input_invalid: %d: %w", index, err)
		}
		children = append(children, child)
	}
	return children, nil
}
