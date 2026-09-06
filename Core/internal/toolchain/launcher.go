package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ksfassistant/core/internal/usercommand"
)

type launcherSettings struct {
	SchemaVersion     int    `json:"schemaVersion"`
	ResourcesDir      string `json:"resourcesDir"`
	HomeDir           string `json:"homeDir"`
	StateDir          string `json:"stateDir"`
	Profile           string `json:"profile"`
	ConfigDir         string `json:"configDir"`
	DataRoot          string `json:"dataRoot,omitempty"`
	Brand             string `json:"brand"`
	ExecutionManifest string `json:"executionManifest,omitempty"`
}

func launchEnvironment(environment []string, config Config) []string {
	result := []string{}
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "LARK") || strings.HasPrefix(upper, "FEISHU_") || strings.HasPrefix(upper, "DYLD_") || strings.HasPrefix(upper, "LD_") || upper == "HOME" || upper == "USERPROFILE" {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "HOME="+config.HomeDir, "USERPROFILE="+config.HomeDir, "LARKSUITE_CLI_CONFIG_DIR="+config.ConfigDir, "LARKSUITE_CLI_PROFILE=default", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1", "LARKSUITE_CLI_REMOTE_META=off")
}

func launchArguments(arguments []string, profile string) ([]string, error) {
	if len(arguments) == 0 {
		return nil, errors.New("command_required")
	}
	descriptor, _ := usercommand.Resolve(arguments)
	valueFlags := map[string]bool{}
	for _, field := range descriptor.Flags {
		if field.Type != "bool" {
			valueFlags["--"+field.Name] = true
			for _, alias := range field.Aliases {
				valueFlags["--"+alias] = true
			}
		}
	}
	for index := 0; index < len(arguments); index++ {
		flag, _, equals := strings.Cut(arguments[index], "=")
		switch flag {
		case "--token", "--app-id":
			if !usercommand.SupportsFlag(arguments, strings.TrimPrefix(flag, "--")) {
				return nil, errors.New("fixed_profile_override_refused")
			}
		case "--profile", "--config-dir", "--brand", "--app-secret", "--access-token", "--user-access-token", "--tenant-access-token":
			return nil, errors.New("fixed_profile_override_refused")
		}
		if !equals && valueFlags[flag] {
			index++
		}
	}
	switch arguments[0] {
	case "update", "upgrade", "self-update", "skill", "skills", "config", "profile":
		return nil, errors.New("managed_configuration_use_auth_gui")
	}
	return append([]string{"--profile", profile}, arguments...), nil
}

func Launch(ctx context.Context, executable string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if err := noSymlinks(executable); err != nil {
		return 1, err
	}
	stateDir := filepath.Dir(filepath.Dir(executable))
	var settings launcherSettings
	if err := readJSON(filepath.Join(stateDir, "launcher.json"), &settings); err != nil || settings.SchemaVersion != 1 || settings.StateDir != stateDir || settings.Brand != "feishu" {
		return 1, errors.New("launcher_configuration_invalid")
	}
	manager, err := New(Config{ResourcesDir: settings.ResourcesDir, HomeDir: settings.HomeDir, StateDir: settings.StateDir, Profile: settings.Profile, ConfigDir: settings.ConfigDir, DataRoot: settings.DataRoot})
	if err != nil {
		return 1, err
	}
	owned, err := manager.ownership()
	if err != nil || owned == nil || manager.verifyState(owned) != nil {
		return 1, errors.New("launcher_edited")
	}
	if settings.ExecutionManifest != usercommand.ManifestDigest() || owned.ExecutionManifest != usercommand.ManifestDigest() || manager.currentLauncher(owned) != nil {
		return 1, errors.New("managed_execution_manifest_changed")
	}
	if _, err := os.Lstat(stateDir + ".lock"); err == nil {
		return 1, errors.New("transaction_pending")
	}
	binary, err := manager.binary()
	if err != nil {
		return 1, err
	}
	if len(arguments) == 3 && arguments[0] == "managed" && arguments[1] == "capabilities" && arguments[2] == "--json" {
		data, err := usercommand.CapabilitiesJSON()
		if err != nil {
			return 1, err
		}
		_, err = stdout.Write(append(data, '\n'))
		if err != nil {
			return 1, err
		}
		return 0, nil
	}
	_, err = launchArguments(arguments, settings.Profile)
	if err != nil {
		return 1, err
	}
	if err := manager.checkBrand(arguments); err != nil {
		return 1, err
	}
	frozen, err := usercommand.Freeze(arguments, stdin)
	if err != nil {
		return 1, err
	}
	return manager.launchFrozen(ctx, binary, frozen, stdout, stderr)
}

func (manager *Manager) checkBrand(arguments []string) error {
	if review, err := usercommand.Evaluate(usercommand.Command{Version: usercommand.Version, Args: arguments}); err == nil && review.Identity == "local" {
		return nil
	}
	for _, argument := range arguments {
		if argument == "--version" || argument == "--help" || argument == "-h" {
			return nil
		}
	}
	file := filepath.Join(manager.config.ConfigDir, "config.json")
	if noSymlinks(file) != nil {
		return errors.New("configuration_unavailable")
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("configuration_unavailable")
	}
	input, err := os.Open(file)
	if err != nil {
		return errors.New("configuration_unavailable")
	}
	defer input.Close()
	var metadata struct {
		Apps []struct {
			Name  string `json:"name"`
			Brand string `json:"brand"`
		} `json:"apps"`
	}
	if json.NewDecoder(input).Decode(&metadata) != nil {
		return errors.New("configuration_unavailable")
	}
	count := 0
	for _, app := range metadata.Apps {
		if app.Name != manager.config.Profile {
			continue
		}
		count++
		if app.Brand != "feishu" {
			return errors.New("brand_mismatch_use_auth_gui")
		}
	}
	if count != 1 {
		return errors.New("fixed_profile_not_configured")
	}
	return nil
}

func LaunchTask(ctx context.Context, executable string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if err := noSymlinks(executable); err != nil {
		return 1, err
	}
	stateDir := filepath.Dir(filepath.Dir(executable))
	var settings launcherSettings
	if err := readJSON(filepath.Join(stateDir, "launcher.json"), &settings); err != nil || settings.SchemaVersion != 1 || settings.StateDir != stateDir || settings.Brand != "feishu" {
		return 1, errors.New("launcher_configuration_invalid")
	}
	manager, err := New(Config{ResourcesDir: settings.ResourcesDir, HomeDir: settings.HomeDir, StateDir: settings.StateDir, Profile: settings.Profile, ConfigDir: settings.ConfigDir})
	if err != nil {
		return 1, err
	}
	owned, err := manager.ownership()
	if err != nil || owned == nil || manager.verifyState(owned) != nil {
		return 1, errors.New("launcher_edited")
	}
	if _, err := os.Lstat(stateDir + ".lock"); err == nil {
		return 1, errors.New("transaction_pending")
	}
	binary, err := manager.taskBinary()
	if err != nil {
		return 1, err
	}
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Env = launchEnvironment(os.Environ(), manager.config)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return 1, errors.New("task_start_failed")
	}
	return 0, nil
}
