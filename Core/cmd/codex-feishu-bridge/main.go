package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"codexusagebar/core/internal/feishu"
)

const version = "0.10.0-preview.1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Println(version)
		return nil
	}
	dataRoot, err := privateDataRoot()
	if err != nil {
		return err
	}
	settings, err := feishu.NewSettingsStore(dataRoot).Load()
	if err != nil {
		return fmt.Errorf("load Feishu settings: %w", err)
	}
	if len(arguments) == 1 && arguments[0] == "--check" {
		if err := settings.Validate(); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}
	if len(arguments) != 0 {
		return errors.New("unsupported codex-feishu-bridge argument")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	parentClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(parentClosed)
	}()
	select {
	case <-ctx.Done():
	case <-parentClosed:
	}
	return nil
}

func privateDataRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("FEISHU_BRIDGE_DATA_DIR")); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("FEISHU_BRIDGE_DATA_DIR must be absolute")
		}
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("unable to locate the current user home directory")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".config", "feishu-bridge"), nil
	}
	return filepath.Join(home, ".config", "feishu-bridge"), nil
}
