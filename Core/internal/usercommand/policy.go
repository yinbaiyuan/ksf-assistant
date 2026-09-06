package usercommand

import (
	"errors"
	"path/filepath"
	"strings"

	"ksfassistant/core/internal/capabilitypolicy"
)

func CheckPolicy(root string, review Review) (string, error) {
	if review.Identity == "local" {
		return "", nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("approval_policy_unavailable")
	}
	policy, err := capabilitypolicy.NewStore(root).Load()
	if err != nil {
		return "", errors.New("approval_policy_unavailable")
	}
	for _, effect := range reviewPolicyEffects(review) {
		if effect.CapabilityID == "" || policy.Decision(effect.CapabilityID, effect.Risk) == capabilitypolicy.Disabled {
			return "", errors.New("approval_policy_denied")
		}
	}
	return policy.Digest()
}

func CheckUnpromptedPolicy(root string, command Command, review Review) error {
	if review.Identity == "local" || review.NeedsApproval {
		return nil
	}
	policy, err := capabilitypolicy.NewStore(root).Load()
	if err != nil {
		return errors.New("approval_policy_unavailable")
	}
	confirmed := false
	for _, argument := range command.Args {
		if argument == "--yes" || argument == "--yes=true" {
			confirmed = true
		}
		if strings.HasPrefix(argument, "--yes=") && argument != "--yes=true" {
			confirmed = false
		}
	}
	for _, effect := range reviewPolicyEffects(review) {
		if policy.Decision(effect.CapabilityID, effect.Risk) == capabilitypolicy.ConfirmEach && (!RequiresCLIConfirmation(command) || !confirmed) {
			return errors.New("user_command_confirmation_required")
		}
	}
	return nil
}

func reviewPolicyEffects(review Review) []Effect {
	effects := append([]Effect(nil), review.Effects...)
	identifiers := review.CapabilityIDs
	if len(identifiers) == 0 {
		identifiers = []string{review.CapabilityID}
	}
	for _, identifier := range identifiers {
		found := false
		for _, effect := range effects {
			if effect.CapabilityID == identifier {
				found = true
				break
			}
		}
		if !found {
			effects = append(effects, Effect{CapabilityID: identifier, Risk: review.Risk})
		}
	}
	return effects
}
