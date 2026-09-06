package toolchain

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"ksfassistant/core/internal/usercommand"
)

type boundedAuthOutput struct {
	bytes.Buffer
	overflow bool
}

func (output *boundedAuthOutput) Write(data []byte) (int, error) {
	if output.Len()+len(data) > 256*1024 {
		output.overflow = true
		return len(data), nil
	}
	return output.Buffer.Write(data)
}

func (manager *Manager) Identity(ctx context.Context, as string) (usercommand.Identity, error) {
	if as != "user" && as != "bot" {
		return usercommand.Identity{}, errors.New("user_command_explicit_identity_required")
	}
	binary, err := manager.binary()
	if err != nil {
		return usercommand.Identity{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "--profile", manager.config.Profile, "auth", "status", "--json")
	command.Env = launchEnvironment(os.Environ(), manager.config)
	command.Stdin = bytes.NewReader(nil)
	var output boundedAuthOutput
	command.Stdout, command.Stderr = &output, io.Discard
	if command.Run() != nil || output.overflow {
		return usercommand.Identity{}, errors.New("user_command_identity_unavailable")
	}
	return usercommand.ParseAuthStatus(output.Bytes(), as)
}

func (manager *Manager) ValidateIdentity(ctx context.Context, expected usercommand.Identity) error {
	as := "bot"
	if expected.UserID != "" {
		as = "user"
	}
	actual, err := manager.Identity(ctx, as)
	if err != nil {
		return err
	}
	return usercommand.ValidateIdentity(expected, actual)
}
