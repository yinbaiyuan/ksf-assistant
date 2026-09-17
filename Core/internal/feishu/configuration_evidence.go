package feishu

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ksfassistant/core/internal/feishuprotocol"
)

func ReadConfigurationEvidence(ctx context.Context, runner CapabilityExecutor, dataRoot string) (feishuprotocol.ConfigurationEvidence, error) {
	result := feishuprotocol.ConfigurationEvidence{
		SchemaVersion: 1, ApplicationState: "unknown", BotState: "unknown", ApplicationPermissions: "unknown",
		UserPermissions: "unknown", BotPermissions: "unknown", OperatorState: "unknown",
		CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Problems: []string{}, Flow: configurationFlow(dataRoot),
	}
	if _, err := os.Lstat(logoutCleanupPath(dataRoot)); !errors.Is(err, os.ErrNotExist) {
		result.CleanupPending = true
		if err != nil {
			result.Problems = append(result.Problems, "cleanup_state_unreadable")
		}
	}
	if runner.Binary == "" {
		return readNativeConfigurationEvidence(ctx, dataRoot, result)
	}
	if !result.CleanupPending {
		if err := MigrateOwnedLegacyProfile(ctx, runner, dataRoot); err != nil {
			result.Problems = append(result.Problems, "legacy_migration_pending")
		}
		if err := RecoverRegistrationOperator(dataRoot); err != nil {
			result.Problems = append(result.Problems, "operator_recovery_pending")
		}
	}
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
	raw, err := runner.RunAuthJSON(ctx, []string{"auth", "status", "--verify", "--json"}, nil, 15*time.Second)
	if err != nil {
		result.ApplicationState = "failed"
		result.Problems = append(result.Problems, "identity_check_failed")
		return result, nil
	}
	appID, _ := raw["appId"].(string)
	if raw["brand"] != "feishu" || !strings.HasPrefix(appID, "cli_") || len(appID) > 128 || strings.IndexFunc(appID, func(character rune) bool {
		return character != '_' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9')
	}) >= 0 {
		result.ApplicationState = "failed"
		result.Problems = append(result.Problems, "application_context_invalid")
		return result, nil
	}
	identities, _ := raw["identities"].(map[string]any)
	user, _ := identities["user"].(map[string]any)
	bot, _ := identities["bot"].(map[string]any)
	result.BotName, _ = bot["appName"].(string)
	if len([]rune(result.BotName)) > 120 || strings.ContainsAny(result.BotName, "\x00\r\n") {
		result.BotName = ""
	}
	userID, _ := user["openId"].(string)
	if userID == "" {
		userID, _ = user["open_id"].(string)
	}
	if userID != "" {
		identityBytes, _ := json.Marshal([]string{appID, "feishu", "default", userID})
		result.IdentityRevision = configurationDigest(string(identityBytes))
		if name, ok := user["userName"].(string); ok && len([]rune(name)) <= 120 && !strings.ContainsAny(name, "\x00\r\n\t") {
			result.UserName = name
		}
	}
	result.ApplicationState = "present"
	result.ApplicationID = appID
	result.BotState = configurationIdentityState(bot)
	status := emptyAuthStatus("unknown")
	status.ProfileValid = true
	switch configurationIdentityState(user) {
	case "present":
		status.Status, status.IdentityValid = "authorized", true
	case "missing":
		status.Status = "unauthorized"
	case "failed":
		status.Status = "failed"
	}
	result.Auth = &status
	var applicationUserScopes []string
	result.PermissionRevision = configurationDigest(fmt.Sprint(BaseConnectionPermissionScopes()))
	required := []string{"contact:user.base:readonly"}
	if scope, exists := user["scope"]; exists && validPermissionScopeValue(scope) {
		granted := stringList(scope)
		status.GrantedScopeCount = len(comparePermissionScopes(nil, granted).Excess)
		result.UserPermissions = configurationScopeState(scope, required)
		result.MissingUserScopes = comparePermissionScopes(required, granted).Missing
	}
	scopes, scopeErr := runner.RunAuthJSON(ctx, []string{"auth", "scopes", "--json"}, nil, 15*time.Second)
	if scopeErr != nil {
		result.Problems = append(result.Problems, "application_permissions_unavailable")
	} else if botScope, exists := scopes["botScopes"]; exists && validPermissionScopeValue(botScope) && validPermissionScopeValue(scopes["userScopes"]) && scopes["appId"] == appID && scopes["brand"] == "feishu" && scopes["tokenType"] == "user" {
		base := BaseConnectionPermissionScopes()
		result.ApplicationPermissions = configurationScopeState(botScope, base)
		result.BotPermissions = result.ApplicationPermissions
		result.MissingApplicationScopes = comparePermissionScopes(base, stringList(botScope)).Missing
		applicationUserScopes = stringList(scopes["userScopes"])
		requestedUserScopes := []string{}
		if result.AuthorizationRequest != nil {
			requestedUserScopes = result.AuthorizationRequest.Scopes
		}
		result.MissingApplicationScopes = sortedScopeSet(func() map[string]bool {
			missing := map[string]bool{}
			for _, scope := range append(result.MissingApplicationScopes, comparePermissionScopes(requestedUserScopes, applicationUserScopes).Missing...) {
				missing[scope] = true
			}
			return missing
		}())
	}
	if result.UserPermissions == "missing" {
		status.MissingCapabilities = append(status.MissingCapabilities, "用户授权范围")
	}
	if result.ApplicationPermissions == "missing" {
		status.MissingCapabilities = append(status.MissingCapabilities, "应用功能权限")
	}
	config, configErr := NewClientConfigStore(dataRoot).Load()
	if configErr != nil {
		result.OperatorState = "failed"
		result.Problems = append(result.Problems, "operator_policy_unreadable")
	} else if config.Operator != nil && config.Operator.AppID == appID && config.Operator.OpenID != "" {
		result.OperatorState = "missing"
		for _, alias := range config.DirectAllowedAliases {
			target, exists := config.MessageTargets[alias]
			if exists && target.Type == "open_id" && target.ID == config.Operator.OpenID {
				result.OperatorState, result.OperatorAlias = "present", alias
				break
			}
		}
		if userID == "" {
			userID = config.Operator.OpenID
			identityBytes, _ := json.Marshal([]string{appID, "feishu", "default", userID})
			result.IdentityRevision = configurationDigest(string(identityBytes))
		}
	} else if config.Operator != nil {
		result.OperatorState = "failed"
		result.Problems = append(result.Problems, "operator_application_mismatch")
	} else {
		result.OperatorState = "missing"
	}
	if result.OperatorState == "missing" && applicationUserScopes != nil {
		missing := map[string]bool{}
		for _, scope := range append(result.MissingApplicationScopes, comparePermissionScopes([]string{"contact:user.base:readonly"}, applicationUserScopes).Missing...) {
			missing[scope] = true
		}
		result.MissingApplicationScopes = sortedScopeSet(missing)
	}
	currentPresence, currentStamp, currentErr := configurationFileEvidenceFor(dataRoot)
	if currentErr != nil || currentPresence != presence || currentStamp != stamp {
		result.ApplicationState = "stale"
		result.Auth = nil
		result.BotState, result.UserPermissions, result.ApplicationPermissions, result.OperatorState = "unknown", "unknown", "unknown", "unknown"
		result.Problems = append(result.Problems, "application_changed_during_check")
		return result, nil
	}
	identityBytes, _ := json.Marshal([]any{appID, raw["brand"], "default", userID, user["available"], user["verified"], user["scope"], config.DirectAllowedAliases, config.MessageTargets})
	result.ContextRevision = configurationDigest(string(identityBytes))
	return result, nil
}

func configurationIdentityState(value map[string]any) string {
	if value["verified"] == false || value["status"] == "verify_failed" {
		return "failed"
	}
	available, exists := value["available"].(bool)
	if !exists {
		return "unknown"
	}
	if !available {
		if status, ok := value["status"].(string); ok && status != "missing" && status != "not_configured" {
			return "unknown"
		}
		return "missing"
	}
	verified, exists := value["verified"].(bool)
	if !exists {
		return "unknown"
	}
	if !verified {
		return "failed"
	}
	return "present"
}

func configurationScopeState(value any, required []string) string {
	if !validPermissionScopeValue(value) {
		return "unknown"
	}
	if comparePermissionScopes(required, stringList(value)).Complete {
		return "present"
	}
	return "missing"
}

func configurationFileEvidenceFor(dataRoot string) (string, string, error) {
	native := officialCredentialPath(dataRoot)
	if info, err := os.Lstat(native); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumPrivateJSONBytes {
			return "", "", errors.New("unsafe native configuration file")
		}
		return "present", fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	root, err := ManagedLarkCLIConfigDir(dataRoot)
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", "", errors.New("invalid configuration directory")
	}
	if info, err := os.Lstat(root); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", "", errors.New("unsafe configuration directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	info, err := os.Lstat(filepath.Join(root, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "missing", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximumPrivateJSONBytes {
		return "", "", errors.New("unsafe configuration file")
	}
	return "present", fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size()), nil
}

func configurationFileEvidence() (string, string, error) {
	return configurationFileEvidenceFor("")
}

func configurationDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func configurationSessionFlow(session *userAuthSession, kind string) *feishuprotocol.ConfigurationFlow {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.configurationID == "" {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return nil
		}
		session.configurationID = hex.EncodeToString(bytes[:])
	}
	state := session.status.Status
	select {
	case <-session.done:
		if session.err != nil {
			state = "failed"
		} else if state == "pending" {
			state = "completed"
		}
	default:
	}
	flow := &feishuprotocol.ConfigurationFlow{ID: session.configurationID, Kind: kind, State: state}
	if !session.configurationStartedAt.IsZero() {
		flow.ExpiresAt = session.configurationStartedAt.Add(maximumAuthSessionDuration).UTC().Format(time.RFC3339Nano)
		if state == "pending" && time.Now().After(session.configurationStartedAt.Add(maximumAuthSessionDuration)) {
			state, flow.State = "expired", "expired"
		}
	}
	if state == "pending" {
		flow.VerificationURL, flow.UserCode, flow.QRDataURL = session.status.VerificationURL, session.status.UserCode, session.status.QRDataURL
	}
	return flow
}

func configurationFlow(dataRoot string) *feishuprotocol.ConfigurationFlow {
	appConfigurationSessions.Lock()
	app := appConfigurationSessions.items[dataRoot]
	appConfigurationSessions.Unlock()
	user := currentUserAuthSession(dataRoot)
	if app != nil && (user == nil || !app.configurationStartedAt.Before(user.configurationStartedAt)) {
		return configurationSessionFlow(app.userAuthSession, "app")
	}
	if user != nil {
		return configurationSessionFlow(user, "user")
	}
	return nil
}

func ReadConfigurationFlow(dataRoot string) *feishuprotocol.ConfigurationFlow {
	return configurationFlow(dataRoot)
}

func CancelConfigurationFlow(dataRoot, flowID, kind string) (feishuprotocol.ConfigurationFlow, error) {
	var session *userAuthSession
	if kind == "app" {
		appConfigurationSessions.Lock()
		if app := appConfigurationSessions.items[dataRoot]; app != nil {
			session = app.userAuthSession
		}
		appConfigurationSessions.Unlock()
	} else if kind == "user" {
		session = currentUserAuthSession(dataRoot)
	}
	if session == nil {
		return feishuprotocol.ConfigurationFlow{}, errors.New("configuration_session_missing")
	}
	flow := configurationSessionFlow(session, kind)
	if flow == nil || flow.ID != flowID || flowID == "" {
		return feishuprotocol.ConfigurationFlow{}, errors.New("configuration_session_changed")
	}
	session.cancel()
	session.mu.Lock()
	session.status.Status = "cancelled"
	session.status.VerificationURL, session.status.UserCode, session.status.QRDataURL = "", "", ""
	session.mu.Unlock()
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		return feishuprotocol.ConfigurationFlow{}, errors.New("configuration_cancellation_unknown")
	}
	if kind == "app" {
		appConfigurationSessions.Lock()
		if app := appConfigurationSessions.items[dataRoot]; app != nil && app.userAuthSession == session {
			delete(appConfigurationSessions.items, dataRoot)
		}
		appConfigurationSessions.Unlock()
	} else {
		userAuthSessions.Lock()
		if userAuthSessions.items[dataRoot] == session {
			delete(userAuthSessions.items, dataRoot)
		}
		userAuthSessions.Unlock()
	}
	return feishuprotocol.ConfigurationFlow{ID: flowID, Kind: kind, State: "cancelled"}, nil
}
