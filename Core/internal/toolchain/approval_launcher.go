package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/userapproval"
	"ksfassistant/core/internal/usercommand"
)

func (manager *Manager) launchFrozen(ctx context.Context, binary string, command usercommand.Command, stdout, stderr io.Writer) (int, error) {
	as := ""
	for index, value := range command.Args {
		if value == "--as" && index+1 < len(command.Args) {
			as = command.Args[index+1]
		}
		if strings.HasPrefix(value, "--as=") {
			as = strings.TrimPrefix(value, "--as=")
		}
	}
	if review, err := usercommand.Evaluate(command); err == nil && review.Identity == "local" {
		as = ""
	}
	if as != "" {
		identity, err := manager.Identity(ctx, as)
		if err != nil {
			return 1, err
		}
		command.Identity = identity
	}
	review, err := usercommand.Evaluate(command)
	if err != nil {
		return 1, err
	}
	root := manager.config.DataRoot
	policyDigest, err := usercommand.CheckPolicy(root, review)
	if err != nil {
		return 1, err
	}
	if err := usercommand.CheckUnpromptedPolicy(root, command, review); err != nil {
		return 1, err
	}
	var caller usercommand.CallerFunc
	var releaseLease func()
	defer func() {
		if releaseLease != nil {
			releaseLease()
		}
	}()
	id := ""
	if review.NeedsApproval {
		if !filepath.IsAbs(root) {
			return 1, errors.New("user_approval_data_root_invalid")
		}
		dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		session, err := localipc.Open(dialCtx, root)
		cancel()
		if err != nil {
			return 1, errors.New("user_approval_desktop_unavailable")
		}
		defer session.Close()
		caller = func(callCtx context.Context, method string, params, target any) error {
			if method == "userApproval/consume" {
				if releaseLease != nil {
					return errors.New("user_approval_already_consumed")
				}
				var err error
				releaseLease, err = userapproval.TryExecutionLease(root)
				if err != nil {
					return err
				}
				if err := manager.ValidateIdentity(callCtx, command.Identity); err != nil {
					return err
				}
			}
			return session.Call(callCtx, method, params, target)
		}
		id, err = usercommand.CallGate(ctx, caller, command)
		if err != nil {
			return 1, err
		}
	}
	outcome := "not_started"
	defer func() {
		if id != "" {
			reportCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = usercommand.ReportResult(reportCtx, caller, id, outcome)
		}
	}()
	if err := ctx.Err(); err != nil {
		return 1, errors.New("user_command_cancelled")
	}
	if review.Identity != "local" && !review.NeedsApproval {
		releaseLease, err = userapproval.TryExecutionLease(root)
		if err != nil {
			return 1, err
		}
		if err := manager.ValidateIdentity(ctx, command.Identity); err != nil {
			return 1, err
		}
	}
	currentPolicy, err := usercommand.CheckPolicy(root, review)
	if err != nil {
		return 1, err
	}
	if currentPolicy != policyDigest {
		return 1, errors.New("approval_policy_changed")
	}
	if review.NeedsApproval {
		if err := manager.ValidateIdentity(ctx, command.Identity); err != nil {
			return 1, err
		}
	}
	execution, err := usercommand.Materialize(command)
	if err != nil {
		return 1, err
	}
	defer execution.Close()
	args := append([]string{"--profile", manager.config.Profile}, execution.Args...)
	if review.NeedsApproval && usercommand.RequiresCLIConfirmation(command) {
		args = append(args, "--yes")
	}
	process := exec.CommandContext(ctx, binary, args...)
	process.Env = launchEnvironment(os.Environ(), manager.config)
	process.Dir = execution.Dir
	process.Stdin, process.Stdout, process.Stderr = bytes.NewReader(execution.Stdin), stdout, stderr
	var artifactOutput artifactOutputBuffer
	if execution.ArtifactsDir != "" {
		process.Stdout = &artifactOutput
	}
	if err := process.Start(); err != nil {
		return 1, &ExecutionError{Code: "cli_start_failed", ExitCode: -1, Result: "not_started"}
	}
	outcome = "unknown"
	if err := process.Wait(); err != nil {
		if artifactOutput.exceeded {
			return 1, &ExecutionError{Code: "cli_output_limit_exceeded_after_execution", Started: true, ExitCode: -1, Result: "unknown"}
		}
		if review.NeedsApproval {
			_, _ = io.WriteString(stderr, "KSFAssistant: user operation outcome unknown; verify read-only and do not replay.\n")
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), &ExecutionError{Code: "cli_execution_failed", Started: true, ExitCode: exit.ExitCode(), Result: "unknown"}
		}
		return 1, &ExecutionError{Code: "cli_execution_unknown", Started: true, ExitCode: -1, Result: "unknown"}
	}
	if execution.ArtifactsDir != "" {
		artifacts, err := execution.PublishArtifacts()
		if err != nil {
			return 1, &ExecutionError{Code: "cli_artifact_delivery_failed", Started: true, ExitCode: 0, Result: "unknown", Artifacts: artifacts}
		}
		if err := writeArtifactOutput(stdout, artifactOutput.Bytes(), artifacts); err != nil {
			return 1, &ExecutionError{Code: "cli_output_delivery_failed_after_execution", Started: true, ExitCode: 0, Result: "unknown"}
		}
	}
	outcome = "succeeded"
	return 0, nil
}

type ExecutionError struct {
	Code      string                 `json:"code"`
	Started   bool                   `json:"started"`
	ExitCode  int                    `json:"exitCode"`
	Result    string                 `json:"result"`
	Artifacts []usercommand.Artifact `json:"partialArtifacts,omitempty"`
}

func (failure *ExecutionError) Error() string { return failure.Code }

type artifactOutputBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (output *artifactOutputBuffer) Write(value []byte) (int, error) {
	if output.Len()+len(value) > 8*1024*1024 {
		output.exceeded = true
		return 0, errors.New("cli_output_limit_exceeded")
	}
	return output.Buffer.Write(value)
}

func writeArtifactOutput(output io.Writer, upstream []byte, artifacts []usercommand.Artifact) error {
	var result map[string]any
	if json.Unmarshal(upstream, &result) != nil || result == nil {
		result = map[string]any{"schemaVersion": 1, "upstreamOutput": string(upstream)}
	}
	result["artifacts"] = artifacts
	result["artifactDelivery"] = map[string]any{"status": "published", "upstreamPaths": "staging_removed", "canonicalPaths": "artifacts"}
	return json.NewEncoder(output).Encode(result)
}
