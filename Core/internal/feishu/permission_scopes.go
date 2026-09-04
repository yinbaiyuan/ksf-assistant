package feishu

import (
	_ "embed"
	"encoding/json"
	"fmt"
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
	return value, nil
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
