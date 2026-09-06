package feishu

import (
	"context"
	"errors"

	"ksfassistant/core/internal/usercommand"
)

func (runner CapabilityExecutor) checkBusinessPolicy(definition CapabilityDefinition, review usercommand.Review) (string, error) {
	digest, err := usercommand.CheckPolicy(runner.DataRoot, review)
	if err != nil {
		return "", err
	}
	if canonical, ok := CapabilityByID(definition.ID); ok {
		legacy := usercommand.Review{Identity: review.Identity, Risk: canonical.Risk, CapabilityID: canonical.ID}
		legacyDigest, err := usercommand.CheckPolicy(runner.DataRoot, legacy)
		if err != nil {
			return "", err
		}
		if digest != legacyDigest {
			return "", errors.New("approval_policy_changed")
		}
	}
	return digest, nil
}

func (runner CapabilityExecutor) checkBusinessConfirmation(ctx context.Context, definition CapabilityDefinition, review usercommand.Review) error {
	if review.NeedsApproval {
		return nil
	}
	policy, err := NewCapabilityPolicyStore(runner.DataRoot).Load()
	if err != nil {
		return errors.New("approval_policy_unavailable")
	}
	required := false
	for _, effect := range review.Effects {
		if policy.Decision(CapabilityDefinition{ID: effect.CapabilityID, Risk: effect.Risk}) == CapabilityConfirmEach {
			required = true
		}
	}
	if canonical, ok := CapabilityByID(definition.ID); ok && policy.Decision(canonical) == CapabilityConfirmEach {
		required = true
	}
	if !required {
		return nil
	}
	boundary, bound := ctx.Value(executionBoundaryKey{}).(executionBoundary)
	if !bound || boundary.operations == nil || checkExecutionBoundary(ctx) != nil {
		return errors.New("user_command_confirmation_required")
	}
	record, err := boundary.operations.load(boundary.operationID)
	if err != nil || record.Permission != CapabilityConfirmEach || record.ChallengeHash != "" || record.PolicyRevision != policy.Revision {
		return errors.New("user_command_confirmation_required")
	}
	return nil
}
