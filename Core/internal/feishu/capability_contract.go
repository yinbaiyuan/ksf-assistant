package feishu

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"ksfassistant/core/internal/usercommand"
)

func CanonicalCapabilityContract(definition CapabilityDefinition) (CapabilityDefinition, error) {
	if definition.Transport == "service" || definition.Backend == "go-sdk" || definition.Transform != "" {
		return definition, nil
	}
	command := definition.Command
	if definition.Transport == "raw" {
		path, _, _ := strings.Cut(definition.APIPath, "?")
		command = append(append([]string(nil), command...), path)
	}
	descriptor, err := usercommand.Resolve(command)
	if err != nil {
		return definition, err
	}
	if definition.Transport == "raw" {
		return definition, nil
	}
	fields := map[string]CapabilityField{}
	for _, flag := range descriptor.Flags {
		if !capabilityBusinessFlag(flag) {
			continue
		}
		field := CapabilityField{Type: "string", Required: flag.Required, Max: usercommand.MaxContentBytes}
		switch flag.Type {
		case "bool", "boolean":
			field.Type, field.Max = "boolean", 0
		case "int", "int32", "int64", "integer":
			field.Type, field.Min, field.Max = "integer", math.MinInt64, math.MaxInt64
		case "float64", "number":
			field.Type, field.Max = "number", 0
		case "string_array", "stringArray":
			field.Type, field.Max, field.MaxItems = "string-array", 0, 1000000
		case "string_slice", "stringSlice", "strings":
			field.Type, field.Max, field.MaxItems = "csv", 0, 1000000
		case "object", "array", "json":
			field.Type, field.Max, field.MaxBytes = "json", 0, usercommand.MaxContentBytes
		}
		if flag.Role == "json" || flag.Role == "json-body" || flag.Role == "json-params" {
			field.Type, field.Max, field.MaxBytes = "json", 0, usercommand.MaxContentBytes
		}
		if len(flag.Enum) > 0 && field.Type == "string" {
			field.Type, field.Values = "enum", append([]string(nil), flag.Enum...)
		}
		field.Stdin = contains(flag.Input, "stdin")
		field.Private = contains(flag.Input, "file")
		if flag.Role == "artifact" {
			field.Type, field.Path, field.Output, field.Private = "path", true, true, false
		}
		fields[flag.Name] = field
	}
	definition.Flags = fields
	definition.FlagOrder = nil
	return definition, nil
}

func capabilityBusinessFlag(flag usercommand.FlagDescriptor) bool {
	return flag.Role != "restricted" && flag.Role != "control" && flag.Role != "output-format"
}

func capabilityOutputArguments(args []string) []string {
	descriptor, err := usercommand.Resolve(args)
	if err != nil {
		return args
	}
	for _, flag := range descriptor.Flags {
		if flag.Name != "format" || flag.Role != "output-format" || !usercommand.SupportsFlag(args, flag.Name) {
			continue
		}
		for _, argument := range args {
			if argument == "--format" || strings.HasPrefix(argument, "--format=") {
				return args
			}
		}
		return append(append([]string(nil), args...), "--format", "json")
	}
	return args
}

func validateCanonicalCapabilityArguments(definition CapabilityDefinition, input map[string]any) error {
	if definition.Transport == "service" || definition.Backend == "go-sdk" || definition.Transform != "" {
		return nil
	}
	args, stdin, files, err := capabilityInvocation(definition, input)
	if err != nil {
		return err
	}
	for index, argument := range args {
		if value, ok := files[argument]; ok {
			args[index] = string(value)
		}
		if argument == "-" && len(stdin) > 0 {
			args[index] = string(stdin)
		}
	}
	args = append(args, "--as", definition.Identity)
	return usercommand.ValidateArguments(args)
}

func capabilityJSONText(value any) (string, error) {
	var data []byte
	if raw, ok := value.(string); ok {
		data = []byte(raw)
	} else {
		var err error
		data, err = json.Marshal(value)
		if err != nil {
			return "", errors.New("invalid_json_value")
		}
	}
	var decoded any
	if json.Unmarshal(data, &decoded) != nil {
		return "", errors.New("invalid_json_value")
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return string(data), nil
	default:
		return "", errors.New("invalid_json_value")
	}
}

func stringArrayValues(value any) []string {
	switch items := value.(type) {
	case string:
		return []string{items}
	case []string:
		return append([]string(nil), items...)
	case []any:
		result := make([]string, len(items))
		for index, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			result[index] = text
		}
		return result
	default:
		return nil
	}
}
