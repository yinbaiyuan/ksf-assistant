package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"ksfassistant/core/internal/capabilitypolicy"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

type CLICommandError struct {
	ExitCode     int
	HTTPStatus   int
	Confirmation bool
	retryAfter   time.Duration
	errorClass   string
	cause        error
}

func (failure *CLICommandError) RetryDelay() time.Duration { return failure.retryAfter }

func (failure *CLICommandError) Unwrap() error { return failure.cause }

func (failure *CLICommandError) Error() string {
	if failure.Confirmation {
		return "lark_cli_confirmation_required"
	}
	return fmt.Sprintf("lark_cli_failed: exit=%d status=%d class=%s", failure.ExitCode, failure.HTTPStatus, failure.errorClass)
}

func (runner CapabilityExecutor) CallMessage(ctx context.Context, request MessageCLIRequest) (map[string]any, error) {
	if err := capabilitypolicy.CheckSession(runner.DataRoot); err != nil {
		return nil, err
	}
	key := request.Resource + "." + request.Method
	switch key {
	case "messages.create", "messages.reply", "messages.patch", "messages.get", "images.create", "files.create":
	default:
		return nil, errors.New("unsupported_message_cli_command")
	}
	definition := CapabilityDefinition{ID: "im." + key, Identity: "bot", Risk: "write"}
	switch key {
	case "messages.create":
		definition.ID = "im.sdk.message.send"
	case "messages.reply":
		definition.ID = "im.message.reply"
	case "messages.patch":
		definition.ID = "im.message.edit"
	case "messages.get":
		definition.ID, definition.Risk = "im.message.batch-get", "read"
	}
	args := []string{"im", request.Resource, request.Method, "--as", "bot"}
	paramsValue := request.Params
	if key == "messages.create" {
		args = []string{"api", "POST", "/open-apis/im/v1/messages", "--as", "bot"}
	}
	if key == "messages.reply" || key == "messages.get" || key == "messages.patch" {
		messageID := request.Params["message_id"]
		if messageID == "" || len(messageID) > 400 {
			return nil, errors.New("invalid_message_id")
		}
		method, path := "GET", "/open-apis/im/v1/messages/"+url.PathEscape(messageID)
		if key == "messages.reply" {
			method, path = "POST", path+"/reply"
		}
		if key == "messages.patch" {
			method = "PATCH"
		}
		args = []string{"api", method, path, "--as", "bot"}
		paramsValue = nil
	}
	if paramsValue != nil {
		params, err := json.Marshal(paramsValue)
		if err != nil {
			return nil, err
		}
		args = append(args, "--params", string(params))
	}
	body := request.Body
	directory := runner.WorkingDirectory
	if directory == "" {
		directory = runner.DataRoot
	}
	if request.File != "" {
		if key != "images.create" && key != "files.create" {
			return nil, errors.New("unexpected_message_upload")
		}
		maximum := int64(10 * 1024 * 1024)
		if key == "files.create" {
			maximum = 30 * 1024 * 1024
		}
		staged, err := stageMessageMedia(runner.DataRoot, request.File, maximum)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(staged)
		directory = staged
		field := "image"
		if key == "files.create" {
			field = "file"
		}
		args = append(args, "--file", field+"=upload")
		body = make(map[string]any, len(request.Body)+1)
		for name, value := range request.Body {
			body[name] = value
		}
		if field == "file" {
			body["file_name"] = filepath.Base(request.File)
		}
	}
	var input []byte
	if body != nil {
		var err error
		input, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		args = append(args, "--data", "-")
	}
	result, err := runner.runBusinessCommand(ctx, definition, args, input, directory, time.Minute)
	if err != nil {
		return result, messageCLIError(err)
	}
	if result["ok"] != true || result["identity"] != "bot" || result["dry_run"] == true || result["error"] != nil {
		failure := commandExecutionError("lark_cli_message_identity_or_envelope_mismatch", 0, true, nil)
		return cliFailureResult(nil, failure), messageCLIError(failure)
	}
	data, ok := result["data"].(map[string]any)
	if !ok {
		failure := commandExecutionError("lark_cli_message_data_missing", 0, true, nil)
		return cliFailureResult(nil, failure), messageCLIError(failure)
	}
	return data, nil
}

func messageCLIError(err error) error {
	var execution *CLIExecutionError
	if !errors.As(err, &execution) {
		return err
	}
	failure := &CLICommandError{ExitCode: execution.ExitCode, Confirmation: execution.ExitCode == 10 || execution.Structured["type"] == "confirmation", cause: err}
	if status, ok := execution.Structured["http_status"].(int); ok && status >= 100 && status <= 599 {
		failure.HTTPStatus = status
	}
	if kind, ok := execution.Structured["type"].(string); ok && (kind == "authentication" || kind == "authorization") {
		failure.errorClass = kind
	}
	if seconds, ok := execution.Structured["retry_after"].(float64); ok {
		failure.retryAfter = time.Duration(seconds * float64(time.Second))
	}
	return failure
}

func stageMessageMedia(root, path string, maximum int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return "", errors.New("unsafe_media_file")
	}
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("media_file_changed")
	}
	stagingRoot := filepath.Join(root, "cli-media-staging")
	if err := ensurePrivateDirectory(stagingRoot); err != nil {
		return "", err
	}
	directory, err := os.MkdirTemp(stagingRoot, "upload-")
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(directory)
		}
	}()
	output, err := os.OpenFile(filepath.Join(directory, "upload"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	size, copyErr := io.Copy(output, io.LimitReader(input, maximum+1))
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if size != info.Size() || size > maximum {
		return "", errors.New("media_file_changed")
	}
	committed = true
	return directory, nil
}

func (runner CapabilityExecutor) runCLIJSON(ctx context.Context, args []string, input []byte, directory string, timeout time.Duration) (map[string]any, error) {
	if len(input) != 0 || !slices.Equal(args, []string{"event", "status", "--current", "--json", "--fail-on-orphan"}) && !slices.Equal(args, []string{"event", "stop", "--json"}) {
		return nil, errors.New("unsupported_internal_cli_lifecycle_command")
	}
	if runner.Binary == "" {
		return nil, errors.New("lark_cli_unavailable")
	}
	full := []string{}
	if runner.Profile != "" {
		full = append(full, "--profile", runner.Profile)
	}
	full = append(full, args...)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(callCtx, runner.Binary, full...)
	command.Env = authEnvironment(runner.DataRoot)
	command.Dir = directory
	command.Stdin = bytes.NewReader(input)
	stdout := &boundedCommandBuffer{limit: maximumCapabilityOutputBytes}
	stderr := &boundedCommandBuffer{limit: maximumCapabilityErrorBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if callCtx.Err() != nil {
		return nil, callCtx.Err()
	}
	if err != nil {
		failure := &CLICommandError{ExitCode: -1}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			failure.ExitCode = exit.ExitCode()
		}
		failure.Confirmation = failure.ExitCode == 10
		var details struct {
			Error struct {
				HTTPStatus int `json:"http_status"`
				StatusCode int `json:"status_code"`
			} `json:"error"`
		}
		if json.Unmarshal(stderr.Bytes(), &details) == nil {
			failure.HTTPStatus = details.Error.HTTPStatus
			if failure.HTTPStatus == 0 {
				failure.HTTPStatus = details.Error.StatusCode
			}
		}
		return nil, failure
	}
	if stdout.overflow || stderr.overflow {
		return nil, errors.New("lark_cli_output_limit")
	}
	var result map[string]any
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result == nil {
		return nil, errors.New("lark_cli_invalid_json")
	}
	if result["ok"] == false || result["error"] != nil {
		return nil, errors.New("lark_cli_unsuccessful_envelope")
	}
	return result, nil
}
