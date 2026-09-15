package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"ksfassistant/core/internal/toolchain"
)

func main() { os.Exit(run(os.Args, os.Stdout)) }

func run(arguments []string, output io.Writer) int {
	name := strings.TrimSuffix(filepath.Base(arguments[0]), ".exe")
	if name == "ksfas-lark" || name == "lark-cli" {
		return failure(output, "agent_feishu_middleware_removed_use_independent_cli")
	}
	if name == "ksf-assistant-task" {
		executable, err := os.Executable()
		if err != nil {
			return failure(output, "launcher_unavailable")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		code, err := toolchain.LaunchTask(ctx, executable, arguments[1:], os.Stdin, output, os.Stderr)
		if err != nil {
			var execution *toolchain.ExecutionError
			if errors.As(err, &execution) {
				_ = json.NewEncoder(output).Encode(map[string]any{"schemaVersion": 1, "ok": false, "execution": execution, "error": map[string]string{"code": execution.Code}})
				if code > 0 {
					return code
				}
				return 1
			}
			return failure(output, err.Error())
		}
		return code
	}
	if len(arguments) < 2 {
		return failure(output, "usage_status_retire_uninstall")
	}
	flags := flag.NewFlagSet("toolchain", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := toolchain.Config{}
	flags.StringVar(&config.ResourcesDir, "resources", "", "bundled Resources directory")
	flags.StringVar(&config.HomeDir, "home", "", "user home (isolated testing)")
	flags.StringVar(&config.StateDir, "state-dir", "", "per-user manager state")
	flags.StringVar(&config.DataRoot, "data-root", "", "fixed shared capability policy and approval endpoint root")
	flags.StringVar(&config.Profile, "profile", "default", "fixed configured profile")
	flags.StringVar(&config.ConfigDir, "config-dir", "", "existing official CLI configuration directory")
	flags.Bool("json", true, "JSON output")
	if flags.Parse(arguments[2:]) != nil || flags.NArg() != 0 {
		return failure(output, "invalid_arguments")
	}
	manager, err := toolchain.New(config)
	if err != nil {
		return failure(output, err.Error())
	}
	var status toolchain.Status
	switch arguments[1] {
	case "status":
		status, err = manager.Status()
	case "install":
		return failure(output, "agent_feishu_middleware_removed_use_independent_cli")
	case "uninstall":
		status, err = manager.Uninstall()
	case "retire":
		status, err = manager.Retire()
	default:
		return failure(output, "unknown_action")
	}
	if err != nil {
		return failure(output, err.Error())
	}
	if json.NewEncoder(output).Encode(map[string]any{"schemaVersion": 1, "ok": true, "status": status}) != nil {
		return 1
	}
	return 0
}

func failure(output io.Writer, code string) int {
	code = safeFailureCode(code)
	if code == "feishu_login_required" {
		_ = json.NewEncoder(output).Encode(map[string]any{"schemaVersion": 1, "ok": false, "error": map[string]string{"code": code, "message": "已退出飞书接入，请在 KSFAssistant 中登录飞书后再使用。"}})
		return 1
	}
	_ = json.NewEncoder(output).Encode(map[string]any{"schemaVersion": 1, "ok": false, "error": map[string]string{"code": code, "message": fmt.Sprintf("Toolchain action failed (%s). No credentials are included.", code)}})
	return 1
}
