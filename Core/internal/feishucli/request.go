package feishucli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
	"ksfassistant/core/internal/productversion"
)

const MethodExecute = "cli/execute"
const Version = productversion.Version
const MaxPayloadBytes = 4 * 1024 * 1024
const MaxRequestBytes = privateipc.MaxFrameBytes - 65536

type Request struct {
	Command     string            `json:"command"`
	Action      string            `json:"action,omitempty"`
	Positionals []string          `json:"positionals,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
	Switches    []string          `json:"switches,omitempty"`
	Payloads    map[string][]byte `json:"payloads,omitempty"`
}

func DecodeRequest(data json.RawMessage) (Request, error) {
	var request Request
	if len(data) > MaxUploadRequestBytes {
		return request, errors.New("CLI request too large")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return request, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return request, errors.New("invalid CLI request")
	}
	for name := range fields {
		switch name {
		case "command", "action", "positionals", "options", "switches", "payloads":
		default:
			return request, fmt.Errorf("unknown CLI request field %s", name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, errors.New("invalid CLI request")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return request, errors.New("unexpected trailing CLI input")
	}
	return request, Validate(request)
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return errors.New("CLI request nesting is too deep")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate or invalid CLI request key")
				}
				seen[name] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid CLI request")
		}
		_, err = decoder.Token()
		return err
	}
	return walk(0)
}

func Parse(args []string, stdin io.Reader) (Request, error) {
	request := Request{Options: map[string]string{}, Payloads: map[string][]byte{}}
	if len(args) > 0 && args[0] == "client" {
		args = args[1:]
	}
	if len(args) == 0 {
		return request, errors.New("missing bridge client command")
	}
	request.Command = args[0]
	if request.Command == "--help" || request.Command == "-h" {
		request.Command = "help"
	}
	if request.Command == "--version" {
		request.Command = "version"
	}
	args = args[1:]
	if needsAction(request.Command) {
		if len(args) == 0 {
			if request.Command != "profile" {
				return request, errors.New("missing command action")
			}
			request.Action = "show"
		} else {
			request.Action, args = args[0], args[1:]
			if nestedAction(request.Command, request.Action) {
				if len(args) == 0 {
					return request, errors.New("missing nested command action")
				}
				request.Action += "/" + args[0]
				args = args[1:]
			}
		}
	}
	spec, err := commandSchema(request)
	if err != nil {
		return request, err
	}
	if spec.count > 0 {
		if len(args) < spec.count {
			return request, errors.New("missing positional arguments")
		}
		for _, value := range args[:spec.count] {
			if !validArgument(value) {
				return request, errors.New("invalid positional argument")
			}
		}
		request.Positionals, args = append([]string(nil), args[:spec.count]...), args[spec.count:]
		spec, err = commandSchema(request)
		if err != nil {
			return request, err
		}
	}
	files := map[string]string{}
	seen := map[string]bool{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "--") {
			if strings.HasPrefix(argument, "-") {
				return request, fmt.Errorf("unsupported flag %s", argument)
			}
			request.Positionals = append(request.Positionals, argument)
			continue
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(argument, "--"), "=")
		field, ok := spec.flags[name]
		if !ok {
			return request, fmt.Errorf("unsupported flag --%s", name)
		}
		if seen[name] {
			return request, fmt.Errorf("duplicate flag --%s", name)
		}
		seen[name] = true
		if field.kind == "boolean" {
			if inline && value != "true" && value != "false" {
				return request, fmt.Errorf("invalid boolean --%s", name)
			}
			if !inline || value == "true" {
				request.Switches = append(request.Switches, name)
			}
			continue
		}
		if !inline {
			index++
			if index >= len(args) {
				return request, fmt.Errorf("missing --%s value", name)
			}
			value = args[index]
		}
		if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "--") {
			return request, fmt.Errorf("missing --%s value", name)
		}
		if field.kind == "payload" {
			files[name] = value
			request.Payloads[name] = nil
		} else {
			request.Options[name] = value
		}
	}
	if err := validate(request, false); err != nil {
		return request, err
	}
	stdinUses := 0
	for _, path := range files {
		if path == "-" {
			stdinUses++
		}
	}
	if stdinUses > 1 {
		return request, errors.New("stdin can supply only one input flag")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	total := 0
	for _, name := range names {
		limit := inputLimit(request, name)
		payload, err := readBoundedInput(files[name], stdin, limit)
		if err != nil {
			return Request{}, fmt.Errorf("read --%s: %w", name, err)
		}
		if limit == MaxPayloadBytes {
			total += len(payload)
		}
		if total > MaxPayloadBytes {
			return Request{}, errors.New("private inputs exceed 4 MiB")
		}
		request.Payloads[name] = payload
		retainInputExtension(&request, name, files[name])
	}
	if err := collectPayloadFiles(&request); err != nil {
		return Request{}, err
	}
	return request, Validate(request)
}

func readInput(path string, stdin io.Reader) ([]byte, error) {
	return readBoundedInput(path, stdin, MaxPayloadBytes)
}

func readBoundedInput(path string, stdin io.Reader, limit int) ([]byte, error) {
	reader := stdin
	if path != "-" {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, errors.New("input file unavailable")
		}
		if !info.Mode().IsRegular() || info.Size() > int64(limit) {
			return nil, errors.New("unsafe private input file")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, errors.New("input file unavailable")
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			return nil, errors.New("input file changed")
		}
		reader = file
	}
	if reader == nil {
		return nil, errors.New("stdin is unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, errors.New("cannot read private input")
	}
	if len(data) > limit {
		return nil, fmt.Errorf("input exceeds %d MiB", limit/(1024*1024))
	}
	return data, nil
}

func Run(ctx context.Context, root string, args []string, stdin io.Reader, stdout io.Writer) error {
	request, err := Parse(args, stdin)
	if err != nil {
		return err
	}
	if request.Command == "version" {
		_, err = fmt.Fprintln(stdout, Version)
		return err
	}
	if value, local, err := Static(request); local || err != nil {
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(value)
	}
	var result json.RawMessage
	session, err := localipc.Open(ctx, root)
	if err != nil {
		if errors.Is(err, localipc.ErrNotRunning) {
			return fmt.Errorf("service unavailable: %w", err)
		}
		return err
	}
	defer session.Close()
	if err := CallRequest(ctx, session.Call, MethodExecute, request, &result); err != nil {
		var rpcError *privateipc.RPCError
		if errors.As(err, &rpcError) && rpcError.Code == -32063 && len(rpcError.Data) > 0 && json.Valid(rpcError.Data) {
			if writeErr := json.NewEncoder(stdout).Encode(rpcError.Data); writeErr != nil {
				return errors.Join(err, writeErr)
			}
		}
		return err
	}
	if !json.Valid(result) {
		return errors.New("invalid service response")
	}
	return json.NewEncoder(stdout).Encode(result)
}

func validArgument(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && strings.TrimSpace(value) != "" && !strings.HasPrefix(value, "--") && len(value) <= 65536
}
