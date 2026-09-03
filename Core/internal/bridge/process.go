package bridge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func run(ctx context.Context, directory, executable string, arguments []string, input []byte) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = directory
	command.Env = bridgeEnvironment()
	if runtime.GOOS == "windows" {
		setHiddenWindow(command)
	}
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("%s", detail)
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("command returned no JSON")
	}
	return stdout.Bytes(), nil
}

func bridgeEnvironment() []string {
	environment := os.Environ()
	if strings.TrimSpace(os.Getenv("CODEX_USAGE_BAR_SUPPORT_DIR")) != "" {
		return environment
	}
	configurationRoot, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configurationRoot) == "" {
		return environment
	}
	return replaceEnvironmentValue(environment, "CODEX_USAGE_BAR_SUPPORT_DIR", filepath.Join(configurationRoot, "CodexUsageBar"))
}

func replaceEnvironmentValue(environment []string, key, value string) []string {
	prefix := strings.ToUpper(key) + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(strings.ToUpper(entry), prefix) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, key+"="+value)
}
