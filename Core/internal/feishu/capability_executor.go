package feishu

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"ksfassistant/core/internal/capabilitypolicy"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CapabilityExecutor struct {
	Binary, Profile, DataRoot, WorkingDirectory string
	UserApproval                                *UserApprovalGate
}

const (
	maximumCapabilityOutputBytes = 4 * 1024 * 1024
	maximumCapabilityErrorBytes  = 64 * 1024
	maximumCapabilityResultBytes = 512 * 1024
)

type boundedCommandBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (buffer *boundedCommandBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.overflow = buffer.overflow || original > 0
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		buffer.overflow = true
	}
	_, _ = buffer.buffer.Write(value)
	return original, nil
}

func (buffer *boundedCommandBuffer) Len() int       { return buffer.buffer.Len() }
func (buffer *boundedCommandBuffer) Bytes() []byte  { return buffer.buffer.Bytes() }
func (buffer *boundedCommandBuffer) String() string { return buffer.buffer.String() }

type CapabilityExecutionOptions struct {
	Timeout            time.Duration
	RemoteTimeout      time.Duration
	RemotePollInterval time.Duration
	OperationID        string
}

type CapabilityExecutionError struct {
	Phase string
	Err   error
}

func (err *CapabilityExecutionError) Error() string { return err.Phase + ": " + err.Err.Error() }
func (err *CapabilityExecutionError) Unwrap() error { return err.Err }

func CapabilityOutcomeUncertain(err error) bool {
	var cliError *CLIExecutionError
	if errors.As(err, &cliError) {
		return cliError.Started
	}
	if isUserApprovalError(err) {
		return false
	}
	var executionError *CapabilityExecutionError
	if !errors.As(err, &executionError) {
		return false
	}
	return executionError.Phase == "write" || executionError.Phase == "remote_verification" || executionError.Phase == "verification"
}

func CapabilityOperationErrorCode(err error) string {
	var cliError *CLIExecutionError
	if errors.As(err, &cliError) {
		return cliError.Code
	}
	var approvalError *UserApprovalError
	if errors.As(err, &approvalError) {
		return approvalError.Code
	}
	if code := safeUserCommandErrorCode(err); code != "" {
		return code
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "execution_timeout"
	}
	var executionError *CapabilityExecutionError
	if errors.As(err, &executionError) {
		switch executionError.Phase {
		case "authorization":
			var required *ProgressiveAuthorizationRequiredError
			if errors.As(executionError.Err, &required) {
				return "progressive_authorization_required"
			}
			return "authorization_check_failed"
		case "preflight":
			return "preflight_failed"
		case "write":
			return "write_outcome_unknown"
		case "remote_verification":
			return "remote_outcome_unknown"
		case "verification":
			return "verification_outcome_unknown"
		case "remote_result":
			return "remote_operation_failed"
		default:
			return "execution_failed"
		}
	}
	return "execution_failed"
}

func ValidateCapabilityInput(id string, input map[string]any) error {
	definition, ok := CapabilityByID(id)
	if !ok {
		return errors.New("unknown_capability")
	}
	if !CapabilityPublished(definition) {
		return errors.New("capability_not_published")
	}
	return validateCapabilityInput(definition, input)
}

func (runner CapabilityExecutor) Execute(ctx context.Context, id string, input map[string]any) (map[string]any, error) {
	return runner.ExecuteWithOptions(ctx, id, input, CapabilityExecutionOptions{})
}

func (runner CapabilityExecutor) ReadPreflight(ctx context.Context, id string, input map[string]any) (map[string]any, error) {
	definition, ok := CapabilityByID(id)
	if !ok {
		return nil, errors.New("unknown_capability")
	}
	if !CapabilityPublished(definition) {
		return nil, errors.New("capability_not_published")
	}
	if err := requireLongTailCapability(definition); err != nil {
		return nil, err
	}
	if err := ensureCapabilityUserAuthorization(ctx, runner, definition); err != nil {
		return nil, &CapabilityExecutionError{Phase: "authorization", Err: err}
	}
	if err := validateCapabilityInput(definition, input); err != nil {
		return nil, err
	}
	if definition.Preflight == nil {
		return nil, nil
	}
	return runner.runPreflightStep(ctx, definition, input, 60*time.Second)
}

func (runner CapabilityExecutor) ReadVerification(ctx context.Context, id string, input, response map[string]any) (VerificationAssessment, error) {
	definition, ok := CapabilityByID(id)
	if !ok {
		return VerificationAssessment{}, errors.New("unknown_capability")
	}
	if !CapabilityPublished(definition) {
		return VerificationAssessment{}, errors.New("capability_not_published")
	}
	if err := requireLongTailCapability(definition); err != nil {
		return VerificationAssessment{}, err
	}
	if definition.Postcondition != nil && definition.Postcondition.Kind == "remote_terminal" && definition.Poll != nil {
		return VerificationAssessment{State: VerificationConfirmed, Evidence: map[string]any{"postcondition": "remote_terminal", "observed": true}}, nil
	}
	if definition.Reread == nil {
		return VerificationAssessment{State: VerificationInconclusive, Evidence: map[string]any{"postcondition": "manual_review_only", "observed": false}}, nil
	}
	evidence, err := runner.runReadStep(ctx, definition.Reread, input, response, 60*time.Second, "reread")
	if err != nil {
		return VerificationAssessment{}, err
	}
	return VerificationAssessment{State: VerificationInconclusive, Evidence: evidence}, nil
}

func (runner CapabilityExecutor) ExecuteWithOptions(ctx context.Context, id string, input map[string]any, options CapabilityExecutionOptions) (map[string]any, error) {
	if err := capabilitypolicy.CheckSession(runner.DataRoot); err != nil {
		return nil, err
	}
	definition, ok := CapabilityByID(id)
	if !ok {
		return nil, errors.New("unknown_capability")
	}
	if !CapabilityPublished(definition) {
		return nil, errors.New("capability_not_published")
	}
	if err := requireLongTailCapability(definition); err != nil {
		return nil, err
	}
	if err := ensureCapabilityUserAuthorization(ctx, runner, definition); err != nil {
		return nil, &CapabilityExecutionError{Phase: "authorization", Err: err}
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
		value, err := runner.runPreflightStep(ctx, definition, input, options.Timeout)
		if err != nil {
			return nil, &CapabilityExecutionError{Phase: "preflight", Err: err}
		}
		preflight = value
	}
	response, err := runner.runDefinition(ctx, definition, input, options.Timeout)
	if err != nil {
		if isUserApprovalError(err) {
			return nil, &CapabilityExecutionError{Phase: "approval", Err: err}
		}
		return cliFailureResult(response, err), &CapabilityExecutionError{Phase: "write", Err: err}
	}
	remote, err := runner.pollRemote(ctx, definition, input, response, options)
	if err != nil {
		return capabilityExecutionEnvelope(id, response, preflight, nil, VerificationInconclusive, nil, false), &CapabilityExecutionError{Phase: "remote_verification", Err: err}
	}
	var verification map[string]any
	verificationState := string(VerificationInconclusive)
	if definition.Reread != nil {
		value, err := runner.runReadStep(ctx, definition.Reread, input, response, options.Timeout, "reread")
		if err != nil {
			return capabilityExecutionEnvelope(id, response, preflight, nil, VerificationInconclusive, remote, false), &CapabilityExecutionError{Phase: "verification", Err: err}
		}
		verification = value
		verificationState = string(VerificationInconclusive)
	}
	verified := false
	if remote != nil {
		verified = true
		verificationState = string(VerificationConfirmed)
	}
	return capabilityExecutionEnvelope(id, response, preflight, verification, VerificationState(verificationState), remote, verified), nil
}

func (runner CapabilityExecutor) runPreflightStep(ctx context.Context, definition CapabilityDefinition, input map[string]any, timeout time.Duration) (map[string]any, error) {
	if definition.Preflight == nil {
		return nil, nil
	}
	if definition.Preflight.Kind == "input_summary" {
		fingerprint, err := operationFingerprint(definition.ID, input)
		if err != nil {
			return nil, err
		}
		targets := map[string]any{}
		for _, key := range definition.ConflictKey {
			if key == "$capability" {
				continue
			}
			if value, ok := input[key]; ok {
				encoded, _ := json.Marshal(value)
				targets[key] = map[string]any{"fingerprint": AuditFingerprint(string(encoded)), "length": len(encoded)}
			}
		}
		return map[string]any{"kind": "input_summary", "capability": definition.ID, "requestFingerprint": "sha256:" + fingerprint[:20], "targets": targets}, nil
	}
	return runner.runReadStep(ctx, definition.Preflight, input, nil, timeout, "preflight")
}

func capabilityExecutionEnvelope(id string, response, preflight, verification map[string]any, verificationState VerificationState, remote map[string]any, verified bool) map[string]any {
	return map[string]any{
		"capabilityId":      id,
		"response":          response,
		"preflight":         nullableMap(preflight),
		"verification":      nullableMap(verification),
		"verificationState": string(verificationState),
		"remote":            nullableMap(remote),
		"verified":          verified,
	}
}

func requireLongTailCapability(definition CapabilityDefinition) error {
	if definition.Backend == "go-sdk" || definition.Transport == "service" {
		return errors.New("capability_requires_unified_executor")
	}
	return nil
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
	ctx = context.WithValue(ctx, businessArtifactConsumerKey{}, businessArtifactConsumer(func(directory string, _ map[string]any) error {
		return copyBusinessArtifact(filepath.Join(directory, relative), output)
	}))
	_, err = runner.runFixedBotBusiness(ctx, fixedBotResourceDownload, []string{"im", "+messages-resources-download", "--message-id", messageID, "--file-key", fileKey, "--type", resourceType, "--output", filepath.ToSlash(relative)}, nil, timeout)
	return err
}

func validateCapabilityInput(definition CapabilityDefinition, input map[string]any) error {
	var err error
	definition, err = CanonicalCapabilityContract(definition)
	if err != nil {
		return err
	}
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
	if err := validateSpecialCapabilityInput(definition, input); err != nil {
		return err
	}
	return validateCanonicalCapabilityArguments(definition, input)
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
	case "number":
		value, ok := number(value)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("invalid_number:%s", name)
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
	case "csv", "string-array":
		items := csvValues(value)
		if schema.Type == "string-array" {
			items = stringArrayValues(value)
		}
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
		data, err := capabilityJSONText(value)
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
	var err error
	definition, err = CanonicalCapabilityContract(definition)
	if err != nil {
		return nil, nil, nil, err
	}
	args := append(append([]string{}, definition.Command...), definition.FixedArgs...)
	files := map[string][]byte{}
	var stdin []byte
	if definition.Transport == "raw" {
		path, query, _ := strings.Cut(definition.APIPath, "?")
		body := map[string]any{}
		params := map[string]any{}
		fixedParams, err := url.ParseQuery(query)
		if err != nil {
			return nil, nil, nil, errors.New("invalid_capability_query")
		}
		for name, values := range fixedParams {
			if len(values) != 1 {
				return nil, nil, nil, errors.New("invalid_capability_query")
			}
			params[name] = values[0]
		}
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
				parameter := strings.ReplaceAll(name, "-", "_")
				if _, fixed := fixedParams[parameter]; fixed {
					return nil, nil, nil, errors.New("invalid_fixed_capability_query_override")
				}
				params[parameter] = value
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
		if !ok {
			continue
		}
		if schema.Type == "boolean" {
			args = append(args, "--"+name+"="+strconv.FormatBool(value == true))
			continue
		}
		if schema.Type == "string-array" {
			for _, item := range stringArrayValues(value) {
				args = append(args, "--"+name, item)
			}
			continue
		}
		args = append(args, "--"+name)
		var text string
		if schema.Type == "csv" {
			var buffer strings.Builder
			writer := csv.NewWriter(&buffer)
			if err := writer.Write(csvValues(value)); err != nil {
				return nil, nil, nil, err
			}
			writer.Flush()
			text = strings.TrimSuffix(buffer.String(), "\n")
		} else if schema.Type == "integer" {
			if numeric, ok := value.(float64); ok {
				text = strconv.FormatFloat(numeric, 'f', -1, 64)
			} else {
				text = fmt.Sprint(value)
			}
		} else if schema.Type == "json" {
			text, err = capabilityJSONText(value)
			if err != nil {
				return nil, nil, nil, err
			}
		} else {
			text = fmt.Sprint(value)
		}
		if schema.Stdin && stdin == nil {
			stdin = []byte(text)
			args = append(args, "-")
		} else if schema.Private {
			sequence++
			placeholder := "__PRIVATE_CAPABILITY_" + strconv.Itoa(sequence) + "__"
			files[placeholder] = []byte(text)
			args = append(args, placeholder)
		} else {
			args = append(args, text)
		}
	}
	return args, stdin, files, nil
}

func (runner CapabilityExecutor) runDefinition(ctx context.Context, definition CapabilityDefinition, input map[string]any, timeout time.Duration) (map[string]any, error) {
	if err := validateCapabilityInput(definition, input); err != nil {
		return nil, err
	}
	if definition.ID == "note.shortcut.transcript" || definition.ID == "minutes.shortcut.detail" && input["transcript"] == true {
		return runner.runTranscriptDefinition(ctx, definition, input, timeout)
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

func (runner CapabilityExecutor) runTranscriptDefinition(ctx context.Context, definition CapabilityDefinition, input map[string]any, timeout time.Duration) (map[string]any, error) {
	cwd := runner.WorkingDirectory
	if cwd == "" {
		cwd = runner.DataRoot
	}
	root := filepath.Join(runner.DataRoot, "private-cache", "transcripts")
	if err := ensurePrivateDirectory(root); err != nil {
		return nil, err
	}
	artifactRoot, err := os.MkdirTemp(root, "transcript-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(artifactRoot)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(artifactRoot, 0o700); err != nil {
			return nil, err
		}
	}
	prepared := cloneInput(input)
	definition.Flags = cloneCapabilityFields(definition.Flags)
	if definition.ID == "note.shortcut.transcript" {
		output := filepath.Join(artifactRoot, "transcript.md")
		relative, err := filepath.Rel(cwd, output)
		if err != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil, errors.New("unsafe_transcript_output")
		}
		prepared["output"] = filepath.ToSlash(relative)
		field := definition.Flags["output"]
		field.Type, field.Output, field.Private = "path", true, false
		definition.Flags["output"] = field
	} else {
		relative, err := filepath.Rel(cwd, artifactRoot)
		if err != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil, errors.New("unsafe_transcript_output")
		}
		prepared["output-dir"] = filepath.ToSlash(relative)
		field := definition.Flags["output-dir"]
		field.Type, field.Output, field.Private = "path", true, false
		definition.Flags["output-dir"] = field
	}
	prepared, err = runner.prepareCapabilityPaths(definition, prepared)
	if err != nil {
		return nil, err
	}
	args, stdin, files, err := capabilityInvocation(definition, prepared)
	if err != nil {
		return nil, err
	}
	relativeRoot, err := filepath.Rel(cwd, artifactRoot)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, businessArtifactConsumerKey{}, businessArtifactConsumer(func(directory string, result map[string]any) error {
		transcript, meta, err := readPrivateTranscriptArtifact(filepath.Join(directory, relativeRoot), 50_000)
		if err != nil {
			return err
		}
		result["transcript"], result["transcriptMeta"] = transcript, meta
		return nil
	}))
	response, err := runner.run(ctx, definition, args, stdin, files, timeout)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func cloneCapabilityFields(fields map[string]CapabilityField) map[string]CapabilityField {
	result := make(map[string]CapabilityField, len(fields))
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func readPrivateTranscriptArtifact(root string, maximumCharacters int) (string, map[string]any, error) {
	files := []string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe_transcript_symlink")
		}
		if info.Mode().IsRegular() {
			extension := strings.ToLower(filepath.Ext(path))
			if extension == ".txt" || extension == ".md" {
				files = append(files, path)
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if len(files) != 1 {
		return "", nil, errors.New("transcript_artifact_count_invalid")
	}
	info, err := os.Lstat(files[0])
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("unsafe_transcript_artifact")
	}
	maximumBytes := int64(maximumCharacters*4 + 4)
	file, err := os.Open(files[0])
	if err != nil {
		return "", nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximumBytes))
	closeErr := file.Close()
	if readErr != nil {
		return "", nil, readErr
	}
	if closeErr != nil {
		return "", nil, closeErr
	}
	runes := []rune(string(data))
	truncated := len(runes) > maximumCharacters || info.Size() > int64(len(data))
	if len(runes) > maximumCharacters {
		runes = runes[:maximumCharacters]
	}
	text := string(runes)
	clear(data)
	clear(runes)
	return text, map[string]any{"sourceBytes": info.Size(), "returnedCharacters": len([]rune(text)), "truncated": truncated}, nil
}

func (runner CapabilityExecutor) prepareCapabilityPaths(definition CapabilityDefinition, input map[string]any) (map[string]any, error) {
	var err error
	definition, err = CanonicalCapabilityContract(definition)
	if err != nil {
		return nil, err
	}
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
		if !schema.Output {
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
		} else if strings.HasPrefix(source, "$input.") {
			value, ok = nestedValue(input, strings.TrimPrefix(source, "$input."))
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
	if strings.HasPrefix(definition.ID, "approval.") {
		if err := validateApprovalCapabilityInput(definition.ID, input); err != nil {
			return err
		}
	}
	if definition.ID == "im.sdk.message.send" {
		format := fmt.Sprint(input["format"])
		_, hasText := input["text"]
		_, hasFile := input["file-path"]
		if contains([]string{"image", "file"}, format) {
			if !hasFile || hasText {
				return errors.New("sdk_media_send_requires_file_only")
			}
		} else if !hasText || hasFile {
			return errors.New("sdk_text_send_requires_text_only")
		}
		if format == "card" {
			var card map[string]any
			if json.Unmarshal([]byte(fmt.Sprint(input["text"])), &card) != nil {
				return errors.New("invalid_card_json")
			}
		}
		return nil
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

func validateApprovalCapabilityInput(id string, input map[string]any) error {
	type payloadSpec struct {
		required []string
		allowed  []string
	}
	specs := map[string]payloadSpec{
		"approval.instances.cancel": {required: []string{"instance_code"}, allowed: []string{"instance_code"}},
		"approval.instances.cc":     {required: []string{"cc_user_ids", "instance_code"}, allowed: []string{"cc_user_ids", "comment", "instance_code"}},
		"approval.instances.create": {required: []string{"approval_code"}, allowed: []string{"approval_code", "form", "node_approver_list", "node_cc_list", "uuid"}},
		"approval.tasks.add_sign":   {required: []string{"add_sign_type", "add_sign_user_ids", "instance_code", "task_id"}, allowed: []string{"add_sign_type", "add_sign_user_ids", "approval_method", "comment", "instance_code", "task_id"}},
		"approval.tasks.approve":    {required: []string{"instance_code", "task_id"}, allowed: []string{"comment", "form", "instance_code", "task_id"}},
		"approval.tasks.reject":     {required: []string{"instance_code", "task_id"}, allowed: []string{"comment", "instance_code", "task_id"}},
		"approval.tasks.remind":     {required: []string{"instance_code", "task_ids"}, allowed: []string{"comment", "instance_code", "task_ids"}},
		"approval.tasks.rollback":   {required: []string{"instance_code", "node_ids", "task_id"}, allowed: []string{"comment", "instance_code", "node_ids", "task_id"}},
		"approval.tasks.transfer":   {required: []string{"instance_code", "task_id", "transfer_user_id"}, allowed: []string{"comment", "instance_code", "task_id", "transfer_user_id"}},
	}
	spec, governed := specs[id]
	if !governed {
		return nil
	}
	data, ok := input["data"].(map[string]any)
	if !ok {
		return errors.New("invalid_approval_data")
	}
	for key := range data {
		if !contains(spec.allowed, key) {
			return fmt.Errorf("unsupported_approval_field:%s", key)
		}
	}
	for _, key := range spec.required {
		value, present := data[key]
		if !present || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return fmt.Errorf("missing_required_approval_field:%s", key)
		}
	}
	for key, value := range data {
		switch key {
		case "cc_user_ids", "add_sign_user_ids", "task_ids", "node_ids":
			items, valid := approvalStringList(value)
			if !valid || len(items) < 1 || len(items) > 100 {
				return fmt.Errorf("invalid_approval_list:%s", key)
			}
		case "node_approver_list", "node_cc_list":
			items, valid := approvalAnyList(value)
			if !valid || len(items) > 100 {
				return fmt.Errorf("invalid_approval_list:%s", key)
			}
			for _, item := range items {
				if _, ok := item.(map[string]any); !ok {
					return fmt.Errorf("invalid_approval_list:%s", key)
				}
			}
		case "add_sign_type", "approval_method":
			n, valid := number(value)
			if !valid || math.Trunc(n) != n || n < 1 || n > 3 {
				return fmt.Errorf("invalid_approval_enum:%s", key)
			}
		case "form":
			text, valid := value.(string)
			var fields []any
			if !valid || len(text) > 1024*1024 || json.Unmarshal([]byte(text), &fields) != nil {
				return errors.New("invalid_approval_form")
			}
		case "comment":
			text, valid := value.(string)
			if !valid || len([]rune(text)) > 500 {
				return errors.New("invalid_approval_comment")
			}
		default:
			text, valid := value.(string)
			if !valid || strings.TrimSpace(text) == "" || len(text) > 4000 {
				return fmt.Errorf("invalid_approval_identifier:%s", key)
			}
		}
	}
	return nil
}

func approvalStringList(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		for _, item := range values {
			if strings.TrimSpace(item) == "" || len(item) > 400 {
				return nil, false
			}
		}
		return values, true
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" || len(text) > 400 {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func approvalAnyList(value any) ([]any, bool) {
	switch values := value.(type) {
	case []any:
		return values, true
	case []map[string]any:
		result := make([]any, len(values))
		for index := range values {
			result[index] = values[index]
		}
		return result, true
	default:
		return nil, false
	}
}

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
	return runner.runBusinessCommand(parent, definition, full, stdin, cwd, timeout)
}

func (runner CapabilityExecutor) runBusinessProcess(parent context.Context, args []string, stdin []byte, cwd string, timeout time.Duration) (map[string]any, error) {
	full := append([]string{}, args...)
	profile := runner.Profile
	if profile == "" {
		profile = "default"
	}
	full = append([]string{"--profile", profile}, full...)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, runner.Binary, full...)
	command.WaitDelay = 2 * time.Second
	command.Env = authEnvironment(runner.DataRoot)
	command.Dir = cwd
	command.Stdin = bytes.NewReader(stdin)
	stdout := boundedCommandBuffer{limit: maximumCapabilityOutputBytes}
	stderr := boundedCommandBuffer{limit: maximumCapabilityErrorBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		failure := commandExecutionError("lark_cli_not_started", -1, false, err)
		return cliFailureResult(nil, failure), failure
	}
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			failure := commandExecutionError("lark_cli_timeout", -1, true, ctx.Err(), stderr.Bytes(), stdout.Bytes())
			return cliFailureResult(nil, failure), failure
		}
		var exitError *exec.ExitError
		exitCode := -1
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
		failure := commandExecutionError(fmt.Sprintf("lark_cli_exit_%d", exitCode), exitCode, true, nil, stderr.Bytes(), stdout.Bytes())
		return cliFailureResult(nil, failure), failure
	}
	if stdout.overflow {
		failure := commandExecutionError("lark_cli_output_limit", 0, true, nil)
		return cliFailureResult(nil, failure), failure
	}
	var result map[string]any
	if stdout.Len() == 0 {
		return map[string]any{}, nil
	}
	if json.Unmarshal(stdout.Bytes(), &result) != nil {
		failure := commandExecutionError("lark_cli_non_json", 0, true, nil)
		return cliFailureResult(nil, failure), failure
	}
	if result["ok"] == false {
		failure := commandExecutionError("lark_cli_operation_failed", 0, true, nil, stdout.Bytes())
		return cliFailureResult(nil, failure), failure
	}
	return boundCapabilityResult(result), nil
}

func boundCapabilityResult(result map[string]any) map[string]any {
	encoded, err := json.Marshal(result)
	if err == nil && len(encoded) <= maximumCapabilityResultBytes {
		return result
	}
	budget := maximumCapabilityResultBytes - 4096
	truncated := false
	clipped, _ := clipCapabilityValue(result, &budget, &truncated).(map[string]any)
	if clipped == nil {
		clipped = map[string]any{}
	}
	clipped["_truncated"] = true
	if err == nil {
		clipped["_sourceBytes"] = len(encoded)
	}
	return clipped
}

func clipCapabilityValue(value any, budget *int, truncated *bool) any {
	if *budget <= 0 {
		*truncated = true
		return nil
	}
	switch item := value.(type) {
	case map[string]any:
		output := map[string]any{}
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if *budget <= len(key)+16 {
				*truncated = true
				break
			}
			*budget -= len(key) + 4
			output[key] = clipCapabilityValue(item[key], budget, truncated)
		}
		return output
	case []any:
		limit := len(item)
		if limit > 200 {
			limit = 200
			*truncated = true
		}
		output := make([]any, 0, limit)
		for index := 0; index < limit && *budget > 0; index++ {
			output = append(output, clipCapabilityValue(item[index], budget, truncated))
		}
		if len(output) < len(item) {
			*truncated = true
		}
		return output
	case string:
		maximum := *budget
		if maximum > 64*1024 {
			maximum = 64 * 1024
		}
		if len(item) <= maximum {
			*budget -= len(item)
			return item
		}
		*truncated = true
		prefix := utf8Prefix(item, maximum)
		*budget -= len(prefix)
		return prefix + "…"
	default:
		encoded, _ := json.Marshal(item)
		if len(encoded) > *budget {
			*truncated = true
			*budget = 0
			return nil
		}
		*budget -= len(encoded)
		return item
	}
}

func utf8Prefix(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := 0
	for index := range value {
		if index > maximum {
			break
		}
		end = index
	}
	return value[:end]
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
	case json.Number:
		parsed, err := n.Float64()
		return parsed, err == nil
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
		reader := csv.NewReader(strings.NewReader(v))
		items, err := reader.Read()
		if err != nil {
			return nil
		}
		if _, err := reader.Read(); err != io.EOF {
			return nil
		}
		return items
	case []string:
		return v
	case []any:
		result := make([]string, len(v))
		for index, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			result[index] = text
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
