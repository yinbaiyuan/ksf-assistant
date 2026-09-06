package usercommand

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

func parse(arguments []string) (parsed, error) {
	result := parsed{flags: map[string]string{}, values: map[string][]string{}}
	if len(arguments) == 0 || len(arguments) > 1024 {
		return result, ErrUnsupported
	}
	argumentBytes := 0
	for _, value := range arguments {
		argumentBytes += len(value)
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return result, ErrUnsupported
		}
	}
	if argumentBytes > MaxContentBytes {
		return result, errors.New("user_command_too_large")
	}
	if supportsGroupHelp(arguments) {
		result.local = true
		result.path = append([]string(nil), arguments...)
		return result, nil
	}
	if len(arguments) == 1 && includes("--help -h --version", arguments[0]) {
		result.local = true
		result.path = append([]string(nil), arguments...)
		return result, nil
	}
	if len(arguments) >= 2 && len(arguments) <= 4 && arguments[0] == "auth" && includes("status scopes", arguments[1]) {
		seen := map[string]bool{}
		for _, flag := range arguments[2:] {
			if !includes("--json --verify", flag) || seen[flag] || flag == "--verify" && arguments[1] != "status" {
				return result, ErrUnsupported
			}
			seen[flag] = true
		}
		result.local = true
		result.path = append([]string(nil), arguments...)
		return result, nil
	}
	if arguments[0] == "schema" && len(arguments) <= 5 {
		for _, part := range arguments[1:] {
			if part != "--json" && !safeSchema(part) {
				return result, ErrUnsupported
			}
		}
		result.local = true
		result.path = append([]string(nil), arguments...)
		return result, nil
	}
	descriptor, count, found := resolveDescriptor(arguments)
	if !found {
		return result, ErrUnsupported
	}
	if len(arguments) == count+1 && includes("--help -h --help=true", arguments[count]) {
		result.local = true
		result.path = append([]string(nil), arguments...)
		return result, nil
	}
	if descriptor.Status != "supported" {
		return result, ErrUnsupported
	}
	result.spec = specificationFor(descriptor)
	result.path = strings.Fields(descriptor.Path)
	if arguments[0] == "api" {
		result.path = append([]string(nil), arguments[:count]...)
	}
	for index := count; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "-h" {
			argument = "--help"
		}
		if !strings.HasPrefix(argument, "--") {
			return result, errors.New("user_command_flag_unsupported")
		}
		name, value, equals := strings.Cut(strings.TrimPrefix(argument, "--"), "=")
		flag, ok := descriptorFlag(descriptor, name)
		if !ok || flag.Role == "restricted" {
			return result, fmt.Errorf("user_command_flag_unsupported: --%s", name)
		}
		name = flag.Name
		if len(result.values[name]) > 0 && !repeatable(flag) {
			return result, errors.New("user_command_duplicate_flag")
		}
		if flag.Type == "bool" {
			if !equals {
				value = "true"
			}
			if value != "true" && value != "false" {
				return result, errors.New("user_command_flag_invalid")
			}
		} else if !equals {
			index++
			if index >= len(arguments) {
				return result, errors.New("user_command_flag_value_required")
			}
			value = arguments[index]
		}
		if err := validateFlagValue(flag, value); err != nil {
			return result, err
		}
		result.values[name] = append(result.values[name], value)
		result.flags[name] = value
	}
	if result.flags["help"] == "true" {
		result.local = true
		return result, nil
	}
	if !includes("user bot", result.flags["as"]) || result.flags["as"] == "" {
		return result, errors.New("user_command_explicit_identity_required")
	}
	if len(descriptor.Identities) > 0 && !contains(descriptor.Identities, result.flags["as"]) {
		return result, errors.New("user_command_identity_unsupported")
	}
	if result.flags["as"] == "user" && result.flags["yes"] != "" {
		return result, errors.New("user_command_cannot_self_approve")
	}
	if result.flags["dry-run"] != "" || result.flags["jq"] != "" {
		return result, errors.New("user_command_execution_override_refused")
	}
	for _, flag := range descriptor.Flags {
		if flag.Required && len(result.values[flag.Name]) == 0 {
			if descriptor.Kind == "typed" && result.flags["params"] != "" {
				continue
			}
			return result, fmt.Errorf("user_command_required_field_missing: --%s", flag.Name)
		}
	}
	if result.flags["page-all"] == "true" {
		limit := result.flags["page-limit"]
		if _, ok := descriptorFlag(descriptor, "page-limit"); !ok || limit == "" {
			return result, errors.New("user_command_explicit_page_limit_required")
		}
		count, err := strconv.Atoi(limit)
		if err != nil || count <= 0 || count > 100 {
			return result, errors.New("user_command_pagination_unbounded")
		}
	}
	return result, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func descriptorFlag(descriptor ExecutionDescriptor, name string) (FlagDescriptor, bool) {
	for _, flag := range descriptor.Flags {
		if flag.Name == name || contains(flag.Aliases, name) {
			return flag, true
		}
	}
	return FlagDescriptor{}, false
}

func repeatable(flag FlagDescriptor) bool {
	return includes("string_array string_slice int_array", flag.Type)
}

func validateFlagValue(flag FlagDescriptor, value string) error {
	if flag.Name == "api-version" && value != "v2" {
		return ErrUnsupported
	}
	if contains(flag.Input, "stdin") && value == "-" || contains(flag.Input, "file") && strings.HasPrefix(value, "@") {
		return nil
	}
	values := []string{value}
	if includes("string_slice int_array", flag.Type) {
		var err error
		values, err = csv.NewReader(strings.NewReader(value)).Read()
		if err != nil {
			return errors.New("user_command_array_invalid")
		}
	}
	for _, entry := range values {
		if strings.HasSuffix(flag.Name, "-id") && !strings.Contains(flag.Description, "URL") && entry != "" && !safeIdentifier(entry) {
			return errors.New("user_command_identifier_invalid")
		}
		if len(flag.Enum) > 0 && entry != "" && !contains(flag.Enum, entry) {
			return fmt.Errorf("user_command_enum_invalid: --%s", flag.Name)
		}
		switch flag.Type {
		case "int", "int64", "integer", "int_array":
			if _, err := strconv.ParseInt(entry, 10, 64); err != nil {
				return errors.New("user_command_integer_invalid")
			}
		case "float", "float64", "number":
			if _, err := strconv.ParseFloat(entry, 64); err != nil || strings.ContainsAny(entry, "nNIi") {
				return errors.New("user_command_number_invalid")
			}
		}
	}
	return nil
}

func specificationFor(descriptor ExecutionDescriptor) specification {
	spec := specification{path: descriptor.Path, risk: descriptor.Risk, action: descriptor.Description, capabilities: strings.Join(descriptor.CapabilityIDs, " "), descriptor: &descriptor}
	for _, flag := range descriptor.Flags {
		if flag.Type == "bool" {
			spec.switches += " " + flag.Name
		} else {
			spec.values += " " + flag.Name
		}
		if flag.Required {
			spec.required += " " + flag.Name
		}
		if includes("file media-file multipart", flag.Role) {
			spec.files += " " + flag.Name
		}
		if flag.Name == "yes" {
			spec.yes = true
		}
		if strings.HasSuffix(flag.Name, "-id") || strings.HasSuffix(flag.Name, "-token") || includes("doc token url name title to", flag.Name) {
			spec.targets += " " + flag.Name
		}
	}
	for _, legacy := range legacySpecifications {
		if legacy.path == spec.path {
			spec.capabilities = strings.TrimSpace(spec.capabilities + " " + legacy.capabilities)
			spec.targets += " " + legacy.targets
		}
	}
	return spec
}

func resolveDescriptor(args []string) (ExecutionDescriptor, int, bool) {
	if descriptor, count, ok := lookupDescriptor(args); ok {
		return descriptor, count, true
	}
	if len(args) < 3 || args[0] != "api" || !strings.HasPrefix(args[2], "/open-apis/") || strings.ContainsAny(args[2], "%?#\\") {
		return ExecutionDescriptor{}, 0, false
	}
	for _, entry := range apiSpecifications {
		if args[1] != entry.method || !matchPath(entry.pattern, args[2]) {
			continue
		}
		spec := entry.spec
		descriptor := ExecutionDescriptor{Path: "api " + entry.method + " " + entry.pattern, Kind: "api", Description: spec.action, OfficialRisk: spec.risk, Risk: spec.risk, Status: "supported", CapabilityIDs: strings.Fields(spec.capabilities), Identities: []string{"user", "bot"}, Source: "reviewed exact method/route adapter"}
		for _, name := range strings.Fields(spec.values) {
			flag := FlagDescriptor{Name: name, Type: "string", Required: includes(spec.required, name)}
			if includes("params data", name) {
				flag.Input = []string{"file", "stdin"}
				flag.Role = "json"
			}
			if includes(spec.files, name) {
				flag.Role = "multipart"
			}
			descriptor.Flags = append(descriptor.Flags, flag)
		}
		descriptor.Flags = append(descriptor.Flags, FlagDescriptor{Name: "as", Type: "string", Role: "control"}, FlagDescriptor{Name: "json", Type: "bool", Role: "output-format"}, FlagDescriptor{Name: "format", Type: "string", Role: "output-format"}, FlagDescriptor{Name: "help", Type: "bool", Role: "control"})
		return descriptor, 3, true
	}
	return ExecutionDescriptor{}, 0, false
}

func businessJSON(value string) error {
	if validateJSON([]byte(value)) != nil {
		return errors.New("user_command_json_invalid")
	}
	var object any
	if json.Unmarshal([]byte(value), &object) != nil {
		return errors.New("user_command_json_invalid")
	}
	switch object.(type) {
	case map[string]any, []any:
		return nil
	}
	return errors.New("user_command_json_invalid")
}
