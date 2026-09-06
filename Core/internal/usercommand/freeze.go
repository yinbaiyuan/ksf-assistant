package usercommand

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func Freeze(args []string, stdin io.Reader) (Command, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Command{}, errors.New("user_command_workdir_unavailable")
	}
	return FreezeAt(args, stdin, cwd)
}

func FreezeAt(args []string, stdin io.Reader, cwd string) (Command, error) {
	command := Command{Version: Version}
	parsed, err := parse(args)
	if err != nil {
		return command, err
	}
	if parsed.local {
		command.Args = append([]string(nil), args...)
		return command, nil
	}
	stdinRead := false
	keys := sortedFlagNames(parsed)
	for _, name := range keys {
		flag, _ := descriptorFlag(*parsed.spec.descriptor, name)
		for index, original := range parsed.values[name] {
			value := original
			if includes("file media-file multipart", flag.Role) {
				prefix, path, existing, err := fileArgument(flag, value)
				if err != nil {
					return command, err
				}
				if existing {
					continue
				}
				data, err := readInputFile(cwd, path)
				if err != nil {
					return command, err
				}
				frozen, err := addFrozenFile(&command, path, data)
				if err != nil {
					return command, err
				}
				parsed.values[name][index] = prefix + frozen
				continue
			}
			if len(flag.Input) == 0 {
				continue
			}
			if strings.HasPrefix(value, "@@") {
				value = value[1:]
			} else if value == "-" {
				if !contains(flag.Input, "stdin") || stdinRead || stdin == nil {
					return command, errors.New("user_command_stdin_conflict")
				}
				stdinRead = true
				data, err := io.ReadAll(io.LimitReader(stdin, MaxContentBytes+1))
				if err != nil || len(data) > MaxContentBytes {
					return command, errors.New("user_command_input_too_large")
				}
				value = strings.TrimPrefix(string(data), "\ufeff")
			} else if strings.HasPrefix(value, "@") {
				if !contains(flag.Input, "file") {
					return command, errors.New("user_command_file_input_unsupported")
				}
				data, err := readInputFile(cwd, strings.TrimSpace(value[1:]))
				if err != nil {
					return command, err
				}
				value = strings.TrimPrefix(string(data), "\ufeff")
			}
			if contains(flag.Input, "stdin") && command.Stdin == nil {
				command.Stdin = []byte(value)
				parsed.values[name][index] = "-"
			} else if strings.HasPrefix(value, "@") {
				parsed.values[name][index] = "@" + value
			} else if value == "-" {
				if !contains(flag.Input, "file") {
					return command, errors.New("user_command_stdin_conflict")
				}
				frozen, err := addFrozenFile(&command, name+".txt", []byte(value))
				if err != nil {
					return command, err
				}
				parsed.values[name][index] = "@" + frozen
			} else {
				parsed.values[name][index] = value
			}
		}
		parsed.flags[name] = parsed.values[name][len(parsed.values[name])-1]
	}
	command.Args = canonicalArguments(parsed)
	command.ArtifactPlan, err = freezeArtifactPlan(parsed, cwd)
	if err != nil {
		return Command{}, err
	}
	if err := validateFrozen(command, parsed); err != nil {
		return Command{}, err
	}
	return command, nil
}

func sortedFlagNames(parsed parsed) []string {
	keys := make([]string, 0, len(parsed.values))
	for key := range parsed.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func canonicalArguments(parsed parsed) []string {
	arguments := append([]string(nil), parsed.path...)
	for _, name := range sortedFlagNames(parsed) {
		flag, _ := descriptorFlag(*parsed.spec.descriptor, name)
		for _, value := range parsed.values[name] {
			if flag.Type == "bool" {
				arguments = append(arguments, "--"+name+"="+value)
			} else {
				arguments = append(arguments, "--"+name, value)
			}
		}
	}
	return arguments
}

func fileArgument(flag FlagDescriptor, value string) (string, string, bool, error) {
	if flag.Role == "media-file" && (strings.HasPrefix(value, "img_") || strings.HasPrefix(value, "file_")) && safeIdentifier(value) {
		return "", value, true, nil
	}
	prefix := ""
	if flag.Role == "multipart" {
		field, path, ok := strings.Cut(value, "=")
		if ok {
			if !safeIdentifier(field) {
				return "", "", false, errors.New("user_command_file_field_invalid")
			}
			prefix, value = field+"=", path
		}
	}
	if value == "" || value == "-" || strings.Contains(value, "://") {
		return "", "", false, errors.New("user_command_dynamic_resource_unsupported")
	}
	return prefix, value, false, nil
}

func addFrozenFile(command *Command, original string, data []byte) (string, error) {
	total := len(command.Stdin) + len(data)
	for _, file := range command.Files {
		total += len(file.Data)
	}
	if total > MaxContentBytes {
		return "", errors.New("user_command_too_large")
	}
	name := fmt.Sprintf("frozen-%d/%s", len(command.Files), filepath.Base(original))
	command.Files = append(command.Files, File{Name: name, DisplayName: filepath.Base(original), Data: append([]byte(nil), data...)})
	return name, nil
}

func readInputFile(cwd, path string) ([]byte, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return nil, errors.New("user_command_file_invalid")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "darwin" {
		for _, prefix := range []string{"/tmp/", "/var/"} {
			if strings.HasPrefix(path, prefix) {
				path = "/private" + path
				break
			}
		}
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("user_command_file_unsafe")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > MaxContentBytes {
		return nil, errors.New("user_command_file_unsafe")
	}
	input, err := os.Open(path)
	if err != nil {
		return nil, errors.New("user_command_file_unavailable")
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("user_command_file_changed")
	}
	data, err := io.ReadAll(io.LimitReader(input, MaxContentBytes+1))
	if err != nil || len(data) > MaxContentBytes {
		return nil, errors.New("user_command_input_too_large")
	}
	after, err := input.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("user_command_file_changed")
	}
	return data, nil
}

type Execution struct {
	Args                 []string
	Stdin                []byte
	Dir                  string
	ArtifactsDir         string
	inputs               []string
	artifactPlan         *ArtifactPlan
	publicationAttempted bool
	requireArtifact      bool
}

func Materialize(command Command) (*Execution, error) {
	if _, err := Evaluate(command); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "ksfas-command-")
	if err != nil {
		return nil, errors.New("user_command_staging_failed")
	}
	result := &Execution{Args: append([]string(nil), command.Args...), Stdin: append([]byte(nil), command.Stdin...), Dir: dir}
	if command.ArtifactPlan != nil {
		plan := *command.ArtifactPlan
		plan.Targets = append([]ArtifactTarget(nil), plan.Targets...)
		result.artifactPlan = &plan
		for _, target := range plan.Targets {
			parent := filepath.Dir(filepath.Join(dir, target.Path))
			if target.Directory {
				parent = filepath.Join(dir, target.Path)
			}
			if err := os.MkdirAll(parent, 0700); err != nil {
				_ = result.Close()
				return nil, errors.New("user_command_staging_failed")
			}
		}
	}
	parsed, _ := parse(command.Args)
	result.requireArtifact = contains([]string{"im +messages-resources-download", "drive +download", "drive +export-download", "docs +media-download", "docs +media-preview", "docs +resource-download", "slides +media-download", "apps +file-download", "apps +db-data-export", "note +transcript"}, parsed.spec.path) || parsed.spec.path == "minutes +download" && parsed.flags["url-only"] != "true"
	if parsed.spec.descriptor != nil && parsed.spec.descriptor.Artifacts {
		result.ArtifactsDir = dir
	}
	if parsed.spec.descriptor != nil {
		for _, flag := range parsed.spec.descriptor.Flags {
			if flag.Role == "artifact" && parsed.flags[flag.Name] != "" {
				result.ArtifactsDir = dir
			}
		}
	}
	for _, file := range command.Files {
		result.inputs = append(result.inputs, filepath.Join(dir, file.Name))
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file.Name)), 0700); err != nil {
			_ = result.Close()
			return nil, errors.New("user_command_staging_failed")
		}
		if err := os.WriteFile(filepath.Join(dir, file.Name), file.Data, 0600); err != nil {
			_ = result.Close()
			return nil, errors.New("user_command_staging_failed")
		}
	}
	return result, nil
}

func (execution *Execution) Close() error {
	if execution == nil || execution.Dir == "" {
		return nil
	}
	err := os.RemoveAll(execution.Dir)
	execution.Dir = ""
	return err
}
