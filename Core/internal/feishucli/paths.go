package feishucli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var inputExtensionPattern = regexp.MustCompile(`^\.[A-Za-z0-9_-]{1,16}$`)

func retainInputExtension(request *Request, name, path string) {
	key := "input-extension-" + name
	if request.Options[key] != "" {
		return
	}
	if extension := filepath.Ext(path); inputExtensionPattern.MatchString(extension) {
		request.Options[key] = extension
	}
}

func requestCapability(request Request) string {
	if (request.Command == "capability" && (request.Action == "read" || request.Action == "write")) || request.Command == "operation" && request.Action == "prepare" {
		if len(request.Positionals) == 1 {
			return request.Positionals[0]
		}
	}
	return compatibilityCommands[request.Command+"/"+request.Action].id
}

func withCapabilityFiles(request Request, schema commandSpec) (commandSpec, error) {
	if len(request.Positionals) == 0 {
		return schema, nil
	}
	fields, err := capabilityFields(requestCapability(request))
	if err != nil {
		return schema, err
	}
	for name, field := range fields {
		if field.Type == "path" && !field.Output {
			schema.flags[name] = flagSpec{kind: "payload"}
		}
	}
	return schema, nil
}

func payloadFieldName(request Request, name string) string {
	if _, ok := compatibilityCommands[request.Command+"/"+request.Action]; !ok {
		return name
	}
	var result strings.Builder
	for index, letter := range name {
		if unicode.IsUpper(letter) && index > 0 {
			result.WriteByte('-')
		}
		if letter == '_' {
			letter = '-'
		}
		result.WriteRune(unicode.ToLower(letter))
	}
	return result.String()
}

func collectPayloadFiles(request *Request) error {
	capability := requestCapability(*request)
	if capability == "" {
		return nil
	}
	fields, err := capabilityFields(capability)
	if err != nil {
		return err
	}
	payload, ok := request.Payloads["payload-file"]
	if !ok {
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil || object == nil {
		return errors.New("invalid JSON object payload")
	}
	changed := false
	total := 0
	for _, value := range request.Payloads {
		total += len(value)
	}
	for rawName, rawValue := range object {
		name := payloadFieldName(*request, rawName)
		field, ok := fields[name]
		if !ok || field.Type != "path" || field.Output {
			continue
		}
		if _, ok := request.Payloads[name]; ok {
			return fmt.Errorf("duplicate file input --%s", name)
		}
		var path string
		if json.Unmarshal(rawValue, &path) != nil || path == "" || path == "-" {
			return fmt.Errorf("use an explicit --%s file flag for stdin", name)
		}
		data, err := readInput(path, nil)
		if err != nil {
			return fmt.Errorf("read payload field %s: %w", name, err)
		}
		total += len(data)
		if total > MaxPayloadBytes {
			return errors.New("private inputs exceed 4 MiB")
		}
		request.Payloads[name] = data
		retainInputExtension(request, name, path)
		delete(object, rawName)
		changed = true
	}
	if changed {
		request.Payloads["payload-file"], err = json.Marshal(object)
	}
	return err
}

func rejectPayloadPaths(request Request, object map[string]json.RawMessage) error {
	capability := requestCapability(request)
	if capability == "" {
		return nil
	}
	fields, err := capabilityFields(capability)
	if err != nil {
		return err
	}
	for name := range object {
		field, ok := fields[payloadFieldName(request, name)]
		if ok && field.Type == "path" && !field.Output {
			return fmt.Errorf("payload field %s must be transferred as file bytes", name)
		}
	}
	return nil
}
