package feishu

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"ksfassistant/core/internal/usercommand"
	"sort"
	"strings"
)

//go:embed contracts/permission-scopes-v1.json
var permissionScopesJSON []byte

type PermissionScopes struct {
	SchemaVersion int      `json:"schemaVersion"`
	Bot           []string `json:"bot"`
	User          []string `json:"user"`
}

func RequiredPermissionScopes() (PermissionScopes, error) {
	var value PermissionScopes
	if err := json.Unmarshal(permissionScopesJSON, &value); err != nil {
		return PermissionScopes{}, fmt.Errorf("decode embedded Feishu permission scopes: %w", err)
	}
	if value.SchemaVersion != 1 || len(value.Bot) != 17 || len(value.User) != 119 {
		return PermissionScopes{}, fmt.Errorf("invalid embedded Feishu permission scope contract")
	}
	scopes, err := usercommand.PermissionScopes()
	if err != nil {
		return PermissionScopes{}, err
	}
	bot, user := map[string]bool{}, map[string]bool{}
	for _, scope := range append(value.Bot, scopes["bot"]...) {
		bot[scope] = true
	}
	for _, scope := range append(value.User, scopes["user"]...) {
		user[scope] = true
	}
	// Typed approval/mail operations have no upstream scope metadata in the
	// pinned schema; these explicit permissions supplement the shared descriptors.
	for _, scope := range []string{"approval:approval:read", "approval:instance:read", "approval:instance:write", "approval:task:read", "approval:task:write", "mail:user_mailbox.message:readonly"} {
		user[scope] = true
	}
	bot["mail:event"] = true
	user["contact:user.base:readonly"] = true
	value.Bot = sortedScopeSet(bot)
	value.User = sortedScopeSet(user)
	return value, nil
}

func sortedScopeSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

type scopeComparison struct {
	RequiredCount int      `json:"requiredCount"`
	GrantedCount  int      `json:"grantedCount"`
	Missing       []string `json:"missing"`
	Excess        []string `json:"excess"`
	Complete      bool     `json:"complete"`
}

func comparePermissionScopes(required, granted []string) scopeComparison {
	requiredSet, grantedSet := map[string]bool{}, map[string]bool{}
	for _, value := range required {
		if value = strings.TrimSpace(value); value != "" {
			requiredSet[value] = true
		}
	}
	for _, value := range granted {
		if value = strings.TrimSpace(value); value != "" {
			grantedSet[value] = true
		}
	}
	missing, excess := []string{}, []string{}
	for value := range requiredSet {
		if !grantedSet[value] {
			missing = append(missing, value)
		}
	}
	for value := range grantedSet {
		if !requiredSet[value] {
			excess = append(excess, value)
		}
	}
	sort.Strings(missing)
	sort.Strings(excess)
	return scopeComparison{RequiredCount: len(requiredSet), GrantedCount: len(grantedSet), Missing: missing, Excess: excess, Complete: len(missing) == 0}
}

func stringList(value any) []string {
	result := []string{}
	switch item := value.(type) {
	case string:
		result = strings.Fields(item)
	case []any:
		for _, raw := range item {
			if text, ok := raw.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
	case []string:
		result = append(result, item...)
	}
	return result
}

func loginPermissionScopes(available []string) ([]string, error) {
	contract, err := RequiredPermissionScopes()
	if err != nil {
		return nil, err
	}
	offered := map[string]bool{}
	for _, scope := range available {
		offered[scope] = true
	}
	if !offered["contact:user.base:readonly"] {
		return nil, fmt.Errorf("application_permissions_missing")
	}
	requested := map[string]bool{"contact:user.base:readonly": true}
	for _, scope := range contract.User {
		if offered[scope] {
			requested[scope] = true
		}
	}
	return sortedScopeSet(requested), nil
}
