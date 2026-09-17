package feishu

import (
	"context"
	"encoding/json"
	"strings"

	"ksfassistant/core/internal/feishuprotocol"
)

func readNativeConfigurationEvidence(ctx context.Context, dataRoot string, result feishuprotocol.ConfigurationEvidence) (feishuprotocol.ConfigurationEvidence, error) {
	presence, stamp, err := configurationFileEvidenceFor(dataRoot)
	if err != nil {
		result.ApplicationState = "failed"
		result.Problems = append(result.Problems, "application_unreadable")
		return result, nil
	}
	if presence == "missing" {
		result.ApplicationState = "missing"
		result.ContextRevision = configurationDigest("missing")
		result.CreationBlocked = appCreationBusinessGuard(dataRoot) != nil
		return result, nil
	}
	credentials, err := loadPlatformOfficialCredentials(dataRoot)
	if err != nil {
		result.ApplicationState = "failed"
		result.Problems = append(result.Problems, "credential_unavailable")
		return result, nil
	}
	transport, err := NewOpenAPIMessageTransport(credentials)
	if err != nil {
		result.ApplicationState = "failed"
		result.Problems = append(result.Problems, "transport_unavailable")
		return result, nil
	}
	bot, err := transport.InspectBot(ctx)
	if err != nil {
		result.ApplicationState = "present"
		result.ApplicationID = credentials.AppID
		result.BotState = "failed"
		result.Problems = append(result.Problems, "bot_identity_unavailable")
		return result, nil
	}
	result.ApplicationState, result.ApplicationID = "present", credentials.AppID
	result.BotState, result.BotName = "present", strings.TrimSpace(bot.Name)
	result.ApplicationPermissions, result.BotPermissions, result.UserPermissions = "present", "present", "present"
	result.PermissionRevision = configurationDigest(strings.Join(BaseConnectionPermissionScopes(), "\n"))

	config, configErr := NewClientConfigStore(dataRoot).Load()
	if configErr != nil {
		result.OperatorState = "failed"
		result.Problems = append(result.Problems, "operator_policy_unreadable")
	} else if config.Operator != nil && config.Operator.AppID == credentials.AppID && config.Operator.OpenID != "" {
		result.OperatorState = "missing"
		for _, alias := range config.DirectAllowedAliases {
			target, exists := config.MessageTargets[alias]
			if exists && target.Type == "open_id" && target.ID == config.Operator.OpenID {
				result.OperatorState, result.OperatorAlias = "present", alias
				break
			}
		}
		identity, _ := json.Marshal([]string{credentials.AppID, "feishu", "default", config.Operator.OpenID})
		result.IdentityRevision = configurationDigest(string(identity))
	} else if config.Operator != nil {
		result.OperatorState = "failed"
		result.Problems = append(result.Problems, "operator_application_mismatch")
	} else {
		result.OperatorState = "missing"
	}
	status := emptyAuthStatus("authorized")
	status.ProfileValid = true
	status.IdentityValid = result.OperatorState == "present"
	if !status.IdentityValid {
		status.Status = "unauthorized"
	}
	result.Auth = &status
	currentPresence, currentStamp, currentErr := configurationFileEvidenceFor(dataRoot)
	if currentErr != nil || currentPresence != presence || currentStamp != stamp {
		result.ApplicationState, result.BotState, result.OperatorState = "stale", "unknown", "unknown"
		result.Auth = nil
		result.Problems = append(result.Problems, "application_changed_during_check")
		return result, nil
	}
	contextValue, _ := json.Marshal([]any{credentials.AppID, credentials.Brand, bot.OpenID, result.OperatorAlias, stamp})
	result.ContextRevision = configurationDigest(string(contextValue))
	return result, nil
}
