package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"ksfassistant/core/internal/userapproval"
	"ksfassistant/core/internal/usercommand"
)

type UserApprovalError struct {
	Code string
}

type businessAuthorizationKey struct{}

func (err *UserApprovalError) Error() string { return err.Code }

func isUserApprovalError(err error) bool {
	var approvalError *UserApprovalError
	return errors.As(err, &approvalError)
}

type UserApprovalGate struct {
	mu          sync.RWMutex
	caller      usercommand.CallerFunc
	testExecute func(context.Context, CapabilityDefinition, []string, []byte) error
}

func (gate *UserApprovalGate) SetCaller(caller usercommand.CallerFunc) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.caller = caller
}

func (gate *UserApprovalGate) configured() bool {
	if gate == nil {
		return false
	}
	gate.mu.RLock()
	defer gate.mu.RUnlock()
	return gate.caller != nil || gate.testExecute != nil
}

func (runner CapabilityExecutor) DesktopApprovalEnabled() bool {
	return runner.UserApproval.configured()
}

func (executor UnifiedCapabilityExecutor) DesktopApprovalEnabled() bool {
	return executor.LongTail.DesktopApprovalEnabled()
}

func (runner CapabilityExecutor) runBusinessCommand(ctx context.Context, definition CapabilityDefinition, args []string, stdin []byte, cwd string, timeout time.Duration) (map[string]any, error) {
	if len(args) >= 3 && args[0] == "api" && strings.HasPrefix(args[2], "/open-apis/cardkit/") && ctx.Value(cardKitAuthorizationKey{}) != true {
		return nil, errors.New("cardkit_internal_transport_required")
	}
	args = capabilityOutputArguments(args)
	var caller usercommand.CallerFunc
	if gate := runner.UserApproval; gate != nil {
		gate.mu.RLock()
		caller = gate.caller
		testExecute := gate.testExecute
		gate.mu.RUnlock()
		if testExecute != nil {
			if err := testExecute(ctx, definition, args, stdin); err != nil {
				return nil, err
			}
			result, err := runner.runBusinessProcess(ctx, args, stdin, cwd, timeout)
			if err == nil {
				err = consumeBusinessArtifacts(ctx, cwd, result)
			}
			return result, err
		}
	}
	if definition.Identity != "user" && definition.Identity != "bot" {
		return nil, &UserApprovalError{Code: "approval_identity_required"}
	}
	if runner.Profile != "" && runner.Profile != "default" {
		return nil, &UserApprovalError{Code: "approval_identity_changed"}
	}
	frozen, err := usercommand.FreezeAt(args, bytes.NewReader(stdin), cwd)
	if err != nil {
		return nil, approvalCommandError(err)
	}
	frozen.Identity, err = runner.businessIdentity(ctx, definition.Identity)
	if err != nil {
		return nil, approvalCommandError(err)
	}
	review, err := usercommand.Evaluate(frozen)
	if err != nil {
		return nil, approvalCommandError(err)
	}
	if review.Identity != definition.Identity || definition.Risk == "read" && review.Risk != "read" {
		return nil, &UserApprovalError{Code: "approval_semantics_mismatch"}
	}
	policyDigest, err := runner.checkBusinessPolicy(definition, review)
	if err != nil {
		return nil, approvalCommandError(err)
	}
	if err := runner.checkBusinessConfirmation(ctx, definition, review); err != nil {
		return nil, approvalCommandError(err)
	}
	var approvalID string
	var releaseAuthorization func()
	defer func() {
		if releaseAuthorization != nil {
			releaseAuthorization()
		}
	}()
	if review.NeedsApproval {
		if caller == nil {
			return nil, &UserApprovalError{Code: "approval_desktop_unavailable"}
		}
		checkedCaller := func(callCtx context.Context, method string, input, output any) error {
			if method == "userApproval/consume" {
				if releaseAuthorization != nil {
					return errors.New("approval_already_consumed")
				}
				var leaseErr error
				releaseAuthorization, leaseErr = userapproval.TryExecutionLease(runner.DataRoot)
				if leaseErr != nil {
					return leaseErr
				}
				actual, err := runner.businessIdentity(callCtx, definition.Identity)
				if err != nil {
					return err
				}
				if err := usercommand.ValidateIdentity(frozen.Identity, actual); err != nil {
					return err
				}
				currentDigest, err := runner.checkBusinessPolicy(definition, review)
				if err != nil {
					return err
				}
				if currentDigest != policyDigest {
					return errors.New("approval_policy_changed")
				}
				if boundary, bound := callCtx.Value(executionBoundaryKey{}).(executionBoundary); bound {
					if err := checkExecutionBoundary(callCtx); err != nil {
						return err
					}
					record, err := boundary.operations.load(boundary.operationID)
					if err != nil {
						return err
					}
					if record.PreflightFingerprint != "" {
						preflightCtx := context.WithValue(callCtx, businessAuthorizationKey{}, runner.DataRoot)
						value, err := (UnifiedCapabilityExecutor{LongTail: runner, DataRoot: runner.DataRoot}).ReadPreflight(preflightCtx, boundary.capabilityID, boundary.input)
						if err != nil {
							return errors.New("approval_preflight_unavailable")
						}
						fingerprint, err := evidenceFingerprint(value)
						if err != nil || fingerprint != record.PreflightFingerprint {
							return errors.New("approval_preflight_changed")
						}
					}
				}
			}
			return caller(callCtx, method, input, output)
		}
		approvalCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		approvalID, err = usercommand.CallGate(approvalCtx, checkedCaller, frozen)
		cancel()
		if err != nil {
			return nil, approvalCommandError(err)
		}
	}
	if !review.NeedsApproval && ctx.Value(businessAuthorizationKey{}) != runner.DataRoot {
		if ctx.Value(cardKitAuthorizationKey{}) == true {
			releaseAuthorization, err = cardKitExecutionLease(ctx, runner.DataRoot)
		} else {
			releaseAuthorization, err = userapproval.TryExecutionLease(runner.DataRoot)
		}
		if err != nil {
			return nil, approvalCommandError(err)
		}
	}
	material, err := usercommand.Materialize(frozen)
	if err != nil {
		if approvalID != "" {
			reportBusinessApprovalResult(caller, approvalID, "not_started")
		}
		return nil, approvalCommandError(err)
	}
	defer material.Close()
	if _, internal := ctx.Value(businessArtifactConsumerKey{}).(businessArtifactConsumer); internal {
		defer os.RemoveAll(material.Dir)
	}
	if _, bound := ctx.Value(executionBoundaryKey{}).(executionBoundary); bound && definition.Risk != "read" {
		if err := beforeApprovedRemoteWrite(ctx); err != nil {
			if approvalID != "" {
				reportBusinessApprovalResult(caller, approvalID, "not_started")
			}
			return nil, approvalCommandError(err)
		}
	}
	if (approvalID != "" || review.Identity == "bot") && usercommand.RequiresCLIConfirmation(frozen) {
		material.Args = append(material.Args, "--yes")
	}
	actual, identityErr := runner.businessIdentity(ctx, definition.Identity)
	if identityErr == nil {
		identityErr = usercommand.ValidateIdentity(frozen.Identity, actual)
	}
	if identityErr != nil {
		if approvalID != "" {
			reportBusinessApprovalResult(caller, approvalID, "not_started")
		}
		return nil, approvalCommandError(identityErr)
	}
	currentDigest, err := runner.checkBusinessPolicy(definition, review)
	if err == nil && currentDigest != policyDigest {
		err = errors.New("approval_policy_changed")
	}
	if err != nil {
		if approvalID != "" {
			reportBusinessApprovalResult(caller, approvalID, "not_started")
		}
		return nil, approvalCommandError(err)
	}
	result, runErr := runner.runBusinessProcess(ctx, material.Args, material.Stdin, material.Dir, timeout)
	if runErr == nil {
		if artifactErr := consumeBusinessArtifacts(ctx, material.Dir, result); artifactErr != nil {
			runErr = commandExecutionError("lark_cli_artifact_unavailable", 0, true, artifactErr)
			result = cliFailureResult(result, runErr)
		} else if _, internal := ctx.Value(businessArtifactConsumerKey{}).(businessArtifactConsumer); !internal {
			artifacts, artifactErr := material.PublishArtifacts()
			result, runErr = businessArtifactPublicationResult(result, artifacts, artifactErr)
		}
	}
	if approvalID != "" {
		outcome := "succeeded"
		if runErr != nil {
			outcome = "unknown"
			var failure *CLIExecutionError
			if errors.As(runErr, &failure) && !failure.Started {
				outcome = "not_started"
			}
		}
		reportBusinessApprovalResult(caller, approvalID, outcome)
	}
	return result, runErr
}

func businessArtifactPublicationResult(result map[string]any, artifacts []usercommand.Artifact, publicationErr error) (map[string]any, error) {
	if len(artifacts) > 0 {
		result = cloneInput(result)
		status := "published"
		if publicationErr != nil {
			status = "partial"
		}
		result["artifacts"] = append([]usercommand.Artifact(nil), artifacts...)
		result["artifactDelivery"] = map[string]any{"status": status, "upstreamPaths": "staging_removed", "canonicalPaths": "artifacts"}
	}
	if publicationErr != nil {
		failure := commandExecutionError("lark_cli_artifact_delivery_failed", 0, true, publicationErr)
		return cliFailureResult(result, failure), failure
	}
	return result, nil
}

func (runner CapabilityExecutor) businessIdentity(ctx context.Context, identity string) (usercommand.Identity, error) {
	result, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--json"}, nil, 10*time.Second)
	if err != nil {
		return usercommand.Identity{}, errors.New("approval_identity_unavailable")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return usercommand.Identity{}, errors.New("approval_identity_unavailable")
	}
	return usercommand.ParseAuthStatus(encoded, identity)
}

func approvalCommandError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &UserApprovalError{Code: "approval_expired"}
	}
	if errors.Is(err, context.Canceled) {
		return &UserApprovalError{Code: "approval_cancelled"}
	}
	code := strings.SplitN(err.Error(), ":", 2)[0]
	if safeCode := safeUserCommandErrorCode(err); safeCode != "" {
		code = safeCode
	} else if !(strings.HasPrefix(code, "approval_") || strings.HasPrefix(code, "user_approval_")) || !cliErrorToken.MatchString(code) {
		code = "approval_unavailable"
	}
	return &UserApprovalError{Code: code}
}

func reportBusinessApprovalResult(caller usercommand.CallerFunc, id, outcome string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = usercommand.ReportResult(ctx, caller, id, outcome)
}
