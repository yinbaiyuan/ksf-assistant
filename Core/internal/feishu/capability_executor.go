package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type CapabilityExecutor struct{ Binary, Profile, DataRoot, WorkingDirectory string }

type CapabilityExecutionOptions struct {
	Timeout            time.Duration
	RemoteTimeout      time.Duration
	RemotePollInterval time.Duration
}

func ValidateCapabilityInput(id string, input map[string]any) error {
	definition, ok := CapabilityByID(id)
	if !ok {
		return errors.New("unknown_capability")
	}
	return validateCapabilityInput(definition, input)
}

func (runner CapabilityExecutor) Execute(ctx context.Context, id string, input map[string]any) (map[string]any, error) {
	return runner.ExecuteWithOptions(ctx, id, input, CapabilityExecutionOptions{})
}

func (runner CapabilityExecutor) ExecuteWithOptions(ctx context.Context, id string, input map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	definition, ok := CapabilityByID(id)
	if !ok {
		return nil, errors.New("unknown_capability")
	}
	if err := validateCapabilityInput(definition, input); err != nil {
		return nil, err
	}
	if options.Timeout <= 0 {
		options.Timeout = 60 * time.Second
	}
	if options.RemoteTimeout <= 0 {
		options.RemoteTimeout = 10 * time.Minute
	}
	if options.RemotePollInterval <= 0 {
		options.RemotePollInterval = 2 * time.Second
	}
	var preflight map[string]any
	if definition.Preflight != nil {
		value, err := runner.runReadStep(ctx, definition.Preflight, input, nil, options.Timeout, "preflight")
		if err != nil {
			return nil, err
		}
		preflight = value
	}
	response, err := runner.runDefinition(ctx, definition, input, options.Timeout)
	if err != nil {
		return nil, err
	}
	remote, err := runner.pollRemote(ctx, definition, input, response, options)
	if err != nil {
		return nil, err
	}
	var verification map[string]any
	if definition.Reread != nil {
		value, err := runner.runReadStep(ctx, definition.Reread, input, response, options.Timeout, "reread")
		if err != nil {
			return nil, err
		}
		verification = value
	}
	return map[string]any{
		"capabilityId": id,
		"response":     response,
		"preflight":    nullableMap(preflight),
		"verification": nullableMap(verification),
		"remote":       nullableMap(remote),
		"verified":     verification != nil || remote != nil,
	}, nil
}

func (runner CapabilityExecutor) DownloadMessageResource(ctx context.Context, messageID, fileKey, resourceType, output string, timeout time.Duration) error {
	if messageID == "" || fileKey == "" || (resourceType != "image" && resourceType != "file") {
		return errors.New("invalid_message_resource_request")
	}
	cwd := runner.WorkingDirectory
	if cwd == "" {
		cwd = runner.DataRoot
	}
	relative, err := filepath.Rel(cwd, output)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("unsafe_message_resource_output")
	}
	parent := filepath.Dir(output)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe_message_resource_parent")
	}
	definition := CapabilityDefinition{Command: []string{"im", "+messages-resources-download"}, Identity: "bot"}
	_, err = runner.run(ctx, definition, []string{"im", "+messages-resources-download", "--message-id", messageID, "--file-key", fileKey, "--type", resourceType, "--output", filepath.ToSlash(relative)}, nil, nil, timeout)
	return err
}

func validateCapabilityInput(definition CapabilityDefinition, input map[string]any) error {
	for name := range input {
		if _, ok := definition.Flags[name]; !ok {
			return fmt.Errorf("unsupported_input_field:%s", name)
		}
	}
	for name, schema := range definition.Flags {
		value, ok := input[name]
		if !ok {
			if schema.Required {
				return fmt.Errorf("missing_input:%s", name)
			}
			continue
		}
		if err := validateCapabilityField(name, value, schema); err != nil {
			return err
		}
	}
	present := func(name string) bool {
		value, ok := input[name]
		return ok && value != nil && value != false && value != ""
	}
	if len(definition.Scope.RequireAny) > 0 {
		found := false
		for _, name := range definition.Scope.RequireAny {
			found = found || present(name)
		}
		if !found {
			return errors.New("required_input_group_missing")
		}
	}
	if len(definition.Scope.RequireExactlyOne) > 0 {
		count := 0
		for _, name := range definition.Scope.RequireExactlyOne {
			if present(name) {
				count++
			}
		}
		if count != 1 {
			return errors.New("exactly_one_input_required")
		}
	}
	for _, group := range definition.Scope.RequiredGroups {
		found := false
		for _, name := range group {
			found = found || present(name)
		}
		if !found {
			return errors.New("required_input_group_missing")
		}
	}
	count := 0
	for _, name := range definition.Scope.MutuallyExclusive {
		if present(name) {
			count++
		}
	}
	if count > 1 {
		return errors.New("mutually_exclusive_inputs")
	}
	if forbiddenPayload(input) {
		return errors.New("forbidden_payload_operation")
	}
	if err := validateSpecialCapabilityInput(definition, input); err != nil {
		return err
	}
	return nil
}

func validateCapabilityField(name string, value any, schema CapabilityField) error {
	switch schema.Type {
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("invalid_string:%s", name)
		}
		if schema.Required && strings.TrimSpace(text) == "" {
			return fmt.Errorf("empty_input:%s", name)
		}
		if schema.Min > 0 && len(text) < schema.Min {
			return fmt.Errorf("input_too_short:%s", name)
		}
		if schema.Max > 0 && len(text) > schema.Max {
			return fmt.Errorf("input_too_long:%s", name)
		}
		if schema.Pattern.Regex != "" {
			matched, err := regexp.MatchString(schema.Pattern.Regex, text)
			if err != nil || !matched {
				return fmt.Errorf("invalid_identifier:%s", name)
			}
		}
	case "integer":
		n, ok := number(value)
		if !ok || math.Trunc(n) != n || n < float64(schema.Min) || (schema.Max > 0 && n > float64(schema.Max)) {
			return fmt.Errorf("invalid_integer:%s", name)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("invalid_boolean:%s", name)
		}
	case "enum":
		text, ok := value.(string)
		if !ok || !contains(schema.Values, text) {
			return fmt.Errorf("invalid_enum:%s", name)
		}
	case "csv":
		items := csvValues(value)
		if items == nil || (schema.Required && len(items) == 0) || (schema.MaxItems > 0 && len(items) > schema.MaxItems) {
			return fmt.Errorf("invalid_list_size:%s", name)
		}
		if schema.ItemPattern.Regex != "" {
			r, err := regexp.Compile(schema.ItemPattern.Regex)
			if err != nil {
				return fmt.Errorf("invalid_list_item:%s", name)
			}
			for _, item := range items {
				if !r.MatchString(item) {
					return fmt.Errorf("invalid_list_item:%s", name)
				}
			}
		}
	case "json":
		switch value.(type) {
		case map[string]any, []any, []string:
		default:
			return fmt.Errorf("invalid_json_value:%s", name)
		}
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("invalid_json_value:%s", name)
		}
		if schema.MaxBytes > 0 && len(data) > schema.MaxBytes {
			return fmt.Errorf("input_too_large:%s", name)
		}
	case "path":
		text, ok := value.(string)
		if !ok || text == "" || filepath.IsAbs(text) || contains(strings.FieldsFunc(text, func(r rune) bool { return r == '/' || r == '\\' }), "..") {
			return fmt.Errorf("unsafe_path:%s", name)
		}
	default:
		return fmt.Errorf("unsupported_field_type:%s", name)
	}
	return nil
}

func capabilityInvocation(definition CapabilityDefinition, input map[string]any) ([]string, []byte, map[string][]byte, error) {
	if definition.Transform == "doc-whiteboard" {
		xml, err := docWhiteboardXML(input)
		if err != nil {
			return nil, nil, nil, err
		}
		input = cloneInput(input)
		input["content"] = xml
		input["doc-format"] = "xml"
	}
	args := append(append([]string{}, definition.Command...), definition.FixedArgs...)
	files := map[string][]byte{}
	var stdin []byte
	if definition.Transport == "raw" {
		path := definition.APIPath
		body := map[string]any{}
		params := map[string]any{}
		for _, name := range orderedCapabilityFlags(definition) {
			schema := definition.Flags[name]
			value, ok := input[name]
			if !ok {
				continue
			}
			if schema.Path {
				path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(fmt.Sprint(value)))
				continue
			}
			if target, ok := schema.Body.(string); ok && target != "" {
				body[target] = value
			} else {
				params[strings.ReplaceAll(name, "-", "_")] = value
			}
		}
		args = []string{"api", definition.Command[1], path}
		if len(body) > 0 {
			data, _ := json.Marshal(body)
			files["__PRIVATE_API_DATA__"] = data
			args = append(args, "--data", "__PRIVATE_API_DATA__")
		}
		if len(params) > 0 {
			data, _ := json.Marshal(params)
			files["__PRIVATE_API_PARAMS__"] = data
			args = append(args, "--params", "__PRIVATE_API_PARAMS__")
		}
		return args, nil, files, nil
	}
	sequence := 0
	for _, name := range orderedCapabilityFlags(definition) {
		schema := definition.Flags[name]
		value, ok := input[name]
		if !ok || value == false {
			continue
		}
		args = append(args, "--"+name)
		if schema.Type == "boolean" {
			continue
		}
		var text string
		if schema.Type == "csv" {
			text = strings.Join(csvValues(value), ",")
		} else if schema.Type == "json" {
			data, _ := json.Marshal(value)
			text = string(data)
		} else {
			text = fmt.Sprint(value)
		}
		if schema.Private && schema.Stdin {
			if stdin != nil {
				return nil, nil, nil, errors.New("only_one_private_stdin_field_is_supported")
			}
			stdin = []byte(text)
			args = append(args, "-")
		} else if schema.Private || schema.Type == "json" {
			sequence++
			placeholder := "__PRIVATE_CAPABILITY_" + strconv.Itoa(sequence) + "__"
			files[placeholder] = []byte(text)
			args = append(args, placeholder)
		} else {
			args = append(args, text)
		}
	}
	if definition.Risk == "high-impact-write" && definition.CLIConfirm {
		args = append(args, "--yes")
	}
	return args, stdin, files, nil
}

func (runner CapabilityExecutor) runDefinition(ctx context.Context, definition CapabilityDefinition, input map[string]any, timeout time.Duration) (map[string]any, error) {
	if err := validateCapabilityInput(definition, input); err != nil {
		return nil, err
	}
	input, err := runner.prepareCapabilityPaths(definition, input)
	if err != nil {
		return nil, err
	}
	args, stdin, files, err := capabilityInvocation(definition, input)
	if err != nil {
		return nil, err
	}
	return runner.run(ctx, definition, args, stdin, files, timeout)
}

func (runner CapabilityExecutor) prepareCapabilityPaths(definition CapabilityDefinition, input map[string]any) (map[string]any, error) {
	cwd := runner.WorkingDirectory
	if cwd == "" {
		cwd = runner.DataRoot
	}
	result := cloneInput(input)
	for name, schema := range definition.Flags {
		if schema.Type != "path" {
			continue
		}
		raw, ok := result[name].(string)
		if !ok || raw == "" {
			continue
		}
		absolute := filepath.Join(cwd, raw)
		relative, err := filepath.Rel(cwd, absolute)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
			return nil, fmt.Errorf("unsafe_path:%s", name)
		}
		if schema.Output {
			parent := filepath.Dir(absolute)
			if err := os.MkdirAll(parent, 0o700); err != nil {
				return nil, err
			}
			info, err := os.Lstat(parent)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("unsafe_output_parent:%s", name)
			}
		} else {
			info, err := os.Lstat(absolute)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("unsafe_input_file:%s", name)
			}
		}
		result[name] = filepath.ToSlash(relative)
	}
	return result, nil
}

func (runner CapabilityExecutor) runReadStep(ctx context.Context, step *CapabilityStep, input, response map[string]any, timeout time.Duration, phase string) (map[string]any, error) {
	definition, ok := CapabilityByID(step.ID)
	if !ok || definition.Risk != "read" {
		return nil, fmt.Errorf("invalid_%s_capability", phase)
	}
	value := mappedCapabilityInput(step, input, response)
	if err := validateCapabilityInput(definition, value); err != nil {
		return nil, fmt.Errorf("%s_%w", phase, err)
	}
	return runner.runDefinition(ctx, definition, value, timeout)
}

func (runner CapabilityExecutor) pollRemote(ctx context.Context, definition CapabilityDefinition, input, response map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	if definition.Poll == nil {
		return nil, nil
	}
	pollDefinition, ok := CapabilityByID(definition.Poll.ID)
	if !ok || pollDefinition.Risk != "read" {
		return nil, errors.New("invalid_remote_poll_capability")
	}
	pollInput := mappedCapabilityInput(&definition.Poll.CapabilityStep, input, response)
	if err := validateCapabilityInput(pollDefinition, pollInput); err != nil {
		return nil, fmt.Errorf("remote_poll_%w", err)
	}
	deadline := time.Now().Add(options.RemoteTimeout)
	var last map[string]any
	for !time.Now().After(deadline) {
		value, err := runner.runDefinition(ctx, pollDefinition, pollInput, minDuration(options.Timeout, options.RemoteTimeout))
		if err != nil {
			return nil, err
		}
		last = value
		status := capabilityStatus(value, definition.Poll.Statuses)
		if contains([]string{"success", "succeeded", "completed", "done", "finished", "published", "released"}, status) {
			return map[string]any{"status": status, "response": value}, nil
		}
		if contains([]string{"failed", "error", "cancelled", "canceled", "terminated"}, status) {
			return nil, fmt.Errorf("remote_operation_%s", status)
		}
		if time.Now().Add(options.RemotePollInterval).After(deadline) {
			break
		}
		timer := time.NewTimer(options.RemotePollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	_ = last // deliberately retained locally so no unredacted remote response enters an error.
	return nil, errors.New("remote_operation_timeout")
}

func mappedCapabilityInput(step *CapabilityStep, input, response map[string]any) map[string]any {
	output := cloneInput(step.Defaults)
	for target, source := range step.Map {
		var value any
		var ok bool
		if strings.HasPrefix(source, "$result.") {
			path := strings.TrimPrefix(source, "$result.")
			value, ok = nestedValue(response, path)
			if !ok {
				value, ok = recursiveKey(response, lastPathPart(path))
			}
		} else {
			value, ok = input[source]
		}
		if ok {
			output[target] = value
		}
	}
	return output
}

func capabilityStatus(response map[string]any, paths []string) string {
	for _, path := range paths {
		if value, ok := nestedValue(response, path); ok && value != nil && fmt.Sprint(value) != "" {
			return strings.ToLower(fmt.Sprint(value))
		}
	}
	return ""
}

func nestedValue(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func recursiveKey(value any, wanted string) (any, bool) {
	switch current := value.(type) {
	case map[string]any:
		if found, ok := current[wanted]; ok && found != nil {
			return found, true
		}
		for _, child := range current {
			if found, ok := recursiveKey(child, wanted); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range current {
			if found, ok := recursiveKey(child, wanted); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func lastPathPart(value string) string {
	parts := strings.Split(value, ".")
	return parts[len(parts)-1]
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func nullableMap(value map[string]any) any {
	if value == nil {
		return nil
	}
	return value
}

func cloneInput(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func validateSpecialCapabilityInput(definition CapabilityDefinition, input map[string]any) error {
	if definition.ID == "docs.whiteboard.insert" {
		_, err := docWhiteboardXML(input)
		return err
	}
	if definition.ID != "mindnotes.node.create" && definition.ID != "mindnotes.node.update" {
		return nil
	}
	data, ok := input["data"].(map[string]any)
	if !ok || len(fmt.Sprint(data["client_token"])) < 10 {
		return errors.New("mindnote_client_token_required")
	}
	nodes, ok := data["nodes"].([]any)
	if !ok || len(nodes) < 1 || len(nodes) > 100 {
		return errors.New("invalid_mindnote_nodes")
	}
	for _, value := range nodes {
		node, ok := value.(map[string]any)
		hasID := ok && strings.TrimSpace(fmt.Sprint(node["node_id"])) != "" && fmt.Sprint(node["node_id"]) != "<nil>"
		if definition.ID == "mindnotes.node.create" && hasID {
			return errors.New("mindnote_create_must_not_include_node_id")
		}
		if definition.ID == "mindnotes.node.update" && !hasID {
			return errors.New("mindnote_update_requires_node_id")
		}
	}
	return nil
}

func docWhiteboardXML(input map[string]any) (string, error) {
	format := fmt.Sprint(input["doc-format"])
	content := strings.TrimSpace(fmt.Sprint(input["content"]))
	if !contains([]string{"mermaid", "plantuml", "svg"}, format) {
		return "", errors.New("invalid_doc_whiteboard_format")
	}
	if content == "" || regexp.MustCompile(`(?i)</whiteboard\s*>`).MatchString(content) {
		return "", errors.New("invalid_doc_whiteboard_content")
	}
	if format == "svg" {
		if !regexp.MustCompile(`(?is)^<svg(?:\s|>).*?</svg>$`).MatchString(content) {
			return "", errors.New("invalid_doc_whiteboard_svg")
		}
		unsafe := regexp.MustCompile(`(?i)<(?:script|foreignObject|iframe|object|embed)\b|\bon[a-z]+\s*=|(?:href|src)\s*=\s*["'](?:https?:|data:|javascript:)`)
		if unsafe.MatchString(content) {
			return "", errors.New("unsafe_doc_whiteboard_svg")
		}
	}
	return `<whiteboard type="` + format + `">\n` + content + `\n</whiteboard>`, nil
}

func DocWhiteboardXML(input map[string]any) (string, error) { return docWhiteboardXML(input) }

func (runner CapabilityExecutor) run(parent context.Context, definition CapabilityDefinition, args []string, stdin []byte, files map[string][]byte, timeout time.Duration) (map[string]any, error) {
	if runner.Binary == "" {
		return nil, errors.New("lark-cli binary is required")
	}
	cwd := runner.WorkingDirectory
	if cwd == "" {
		cwd = runner.DataRoot
	}
	privateDir := filepath.Join(runner.DataRoot, "private-cache", "action-payloads")
	if err := ensurePrivateDirectory(privateDir); err != nil {
		return nil, err
	}
	created := []string{}
	defer func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
	}()
	for placeholder, data := range files {
		file, err := os.CreateTemp(privateDir, "capability-*.json")
		if err != nil {
			return nil, err
		}
		path := file.Name()
		created = append(created, path)
		if err := file.Chmod(0o600); err == nil {
			_, err = file.Write(data)
		}
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		for i, arg := range args {
			if arg == placeholder {
				args[i] = "@" + filepath.ToSlash(path)
			}
		}
	}
	full := []string{}
	if runner.Profile != "" {
		full = append(full, "--profile", runner.Profile)
	}
	commandLength := len(definition.Command)
	if definition.Transport == "raw" {
		commandLength = 3
	}
	if commandLength > len(args) {
		return nil, errors.New("invalid_capability_command")
	}
	full = append(full, args[:commandLength]...)
	if definition.Identity != "" {
		full = append(full, "--as", definition.Identity)
	}
	full = append(full, args[commandLength:]...)
	full = append(full, "--format", "json")
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, runner.Binary, full...)
	command.Dir = cwd
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("lark-cli failed: %s", safeCommandError(stderr.String(), stdout.String()))
	}
	var result map[string]any
	if stdout.Len() == 0 {
		return map[string]any{}, nil
	}
	if json.Unmarshal(stdout.Bytes(), &result) != nil {
		return nil, errors.New("lark-cli returned non-json")
	}
	return result, nil
}

func safeCommandError(values ...string) string {
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value != "" {
			if len(value) > 900 {
				return value[:900]
			}
			return value
		}
	}
	return "command failed"
}
func number(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
func csvValues(value any) []string {
	switch v := value.(type) {
	case string:
		items := strings.Split(v, ",")
		result := []string{}
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
		return result
	case []string:
		return v
	case []any:
		result := make([]string, len(v))
		for i, item := range v {
			result[i] = fmt.Sprint(item)
		}
		return result
	}
	return nil
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func forbiddenPayload(value any) bool {
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if forbiddenPayload(item) {
				return true
			}
		}
		return false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for key, child := range object {
		normalized := strings.ReplaceAll(strings.ToLower(key), "_", "-")
		if contains([]string{
			"delete", "remove", "clear", "permission", "role", "member", "move-to-drive", "wiki-move",
			"workflow", "automation", "openapi-key", "database", "cache", "plugin", "approval",
			"urgent-phone", "urgent-sms", "meeting-join", "meeting-end", "meeting-leave", "minutes-download",
		}, normalized) || strings.HasPrefix(normalized, "delete-") || strings.HasSuffix(normalized, "-delete") {
			return true
		}
		if normalized == "operation" && contains([]string{"delete", "remove", "clear"}, strings.ToLower(fmt.Sprint(child))) {
			return true
		}
		if forbiddenPayload(child) {
			return true
		}
	}
	return false
}
