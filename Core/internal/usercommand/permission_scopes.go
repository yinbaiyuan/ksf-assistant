package usercommand

import (
	"encoding/json"
	"sort"
	"strings"
)

// PermissionScopes uses the same pinned descriptors that expose commands to Skills.
// Legacy capability aliases are deliberately excluded.
func PermissionScopes() (map[string][]string, error) {
	var manifest struct {
		Descriptors []struct {
			Status     string              `json:"status"`
			Identities []string            `json:"identities"`
			Scopes     map[string][]string `json:"scopes"`
		} `json:"descriptors"`
	}
	if err := json.Unmarshal(executionManifest, &manifest); err != nil {
		return nil, err
	}
	sets := map[string]map[string]bool{"user": {}, "bot": {}}
	for _, d := range manifest.Descriptors {
		if d.Status != "supported" {
			continue
		}
		for _, identity := range d.Identities {
			set, ok := sets[identity]
			if !ok {
				continue
			}
			prefix := "User"
			if identity == "bot" {
				prefix = "Bot"
			}
			keys := []string{"ConditionalScopes", "Conditional" + prefix + "Scopes"}
			// Identity-specific scopes replace the default identity scope list.
			if len(d.Scopes[prefix+"Scopes"]) > 0 {
				keys = append(keys, prefix+"Scopes")
			} else {
				keys = append(keys, "Scopes")
			}
			for _, key := range keys {
				for _, scope := range d.Scopes[key] {
					if scope = strings.TrimSpace(scope); scope != "" {
						set[scope] = true
					}
				}
			}
		}
	}
	result := map[string][]string{}
	for identity, set := range sets {
		for scope := range set {
			result[identity] = append(result[identity], scope)
		}
		sort.Strings(result[identity])
	}
	return result, nil
}
