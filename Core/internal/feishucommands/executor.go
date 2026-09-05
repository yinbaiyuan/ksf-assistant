package feishucommands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ksfassistant/core/internal/feishu"
	"ksfassistant/core/internal/feishucli"
)

type invocation struct {
	ctx      context.Context
	service  *feishu.CapabilityService
	request  feishucli.Request
	dataRoot string
}

func Execute(ctx context.Context, root string, capability *feishu.CapabilityService, request feishucli.Request) (any, error) {
	if err := feishucli.Validate(request); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Command == "task-link" {
		return nil, errors.New("task-link commands require the Core integration gateway")
	}
	if result, local, err := feishucli.Static(request); local || err != nil {
		return result, err
	}
	if capability == nil {
		return nil, errors.New("service unavailable: process capability service is required")
	}
	settings, err := feishu.NewSettingsStore(root).Load()
	if err != nil {
		return nil, err
	}
	call := &invocation{ctx: ctx, service: capability, request: request, dataRoot: root}
	var result any
	err = call.runClient(root, settings, commandArguments(request), func(value any) error { result = value; return nil })
	return result, err
}

func commandArguments(request feishucli.Request) []string {
	arguments := []string{request.Command}
	if request.Action != "" {
		arguments = append(arguments, strings.Split(request.Action, "/")...)
	}
	arguments = append(arguments, request.Positionals...)
	names := make([]string, 0, len(request.Options))
	for name := range request.Options {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		arguments = append(arguments, "--"+name, request.Options[name])
	}
	for _, name := range request.Switches {
		arguments = append(arguments, "--"+name)
	}
	names = names[:0]
	for name := range request.Payloads {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		arguments = append(arguments, "--"+name, "-")
	}
	return arguments
}

func (call *invocation) clientPayload(arguments []string) ([]byte, error) {
	return call.clientPrivateValue(arguments, "--payload-file")
}

func (call *invocation) clientPrivateValue(arguments []string, flag string) ([]byte, error) {
	if _, err := clientFlag(arguments, flag); err != nil {
		return nil, err
	}
	payload, ok := call.request.Payloads[strings.TrimPrefix(flag, "--")]
	if !ok {
		return nil, fmt.Errorf("missing payload for %s", flag)
	}
	return append([]byte(nil), payload...), nil
}

func (call *invocation) wait(duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-call.ctx.Done():
		return call.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (call *invocation) stageMedia(root, id string) (string, error) {
	payload, ok := call.request.Payloads["media-file"]
	if !ok || len(payload) == 0 {
		return "", errors.New("missing media payload")
	}
	source, err := call.stageInput("media-file", payload)
	if err != nil {
		return "", err
	}
	defer os.Remove(source)
	return feishu.StageOutboundMedia(root, id, source)
}

func (call *invocation) stageInput(name string, payload []byte) (string, error) {
	root := filepath.Join(call.dataRoot, "private-cache", "media")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(root, "cli-input-*"+call.request.Options["input-extension-"+name])
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(payload)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func (call *invocation) prepare(service *feishu.CapabilityService, ctx context.Context, capabilityID string, input map[string]any, source string) (feishu.PreparedOperation, error) {
	definition, ok := feishu.CapabilityByID(capabilityID)
	if !ok || !feishu.CapabilityPublished(definition) {
		return feishu.PreparedOperation{}, errors.New("unknown_capability")
	}
	staged := []string{}
	defer func() {
		for _, path := range staged {
			_ = os.Remove(path)
		}
	}()
	for name, field := range definition.Flags {
		if field.Type != "path" || field.Output {
			continue
		}
		payload, ok := call.request.Payloads[name]
		if !ok {
			if _, supplied := input[name]; supplied && call.request.Command != "send" {
				return feishu.PreparedOperation{}, fmt.Errorf("file input %s requires transferred bytes", name)
			}
			continue
		}
		path, err := call.stageInput(name, payload)
		if err != nil {
			return feishu.PreparedOperation{}, err
		}
		staged = append(staged, path)
		relative, err := filepath.Rel(call.dataRoot, path)
		if err != nil {
			return feishu.PreparedOperation{}, err
		}
		input[name] = filepath.ToSlash(relative)
	}
	result, err := service.Prepare(ctx, capabilityID, input, source)
	if err == nil && result.Operation.ID != "" && !operationTerminal(result.Operation.Status) {
		staged = nil
	}
	return result, err
}

func (call *invocation) validationInput(definition feishu.CapabilityDefinition, input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for name, value := range input {
		result[name] = value
	}
	for name, field := range definition.Flags {
		if field.Type == "path" && !field.Output {
			if _, exists := call.request.Payloads[name]; exists {
				result[name] = "private-cache/media/cli-input"
			}
		}
	}
	return result
}
