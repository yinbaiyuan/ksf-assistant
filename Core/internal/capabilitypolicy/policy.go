package capabilitypolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"ksfassistant/core/internal/privatestore"
)

const Version = 1

type Permission string

const (
	Disabled    Permission = "disabled"
	ConfirmEach Permission = "confirm_each"
	Allowed     Permission = "allowed"
)

var ErrRevisionConflict = errors.New("capability_policy_revision_conflict")

type Policy struct {
	Version             int                   `json:"version"`
	Revision            uint64                `json:"revision"`
	RiskDefaults        map[string]Permission `json:"riskDefaults"`
	CapabilityOverrides map[string]Permission `json:"capabilityOverrides"`
	UpdatedAt           time.Time             `json:"updatedAt"`
}

func Default() Policy {
	return Policy{Version: Version, Revision: 1, RiskDefaults: map[string]Permission{
		"read": Allowed, "write": Allowed, "high-impact-write": ConfirmEach,
		"remote-operation": ConfirmEach, "destructive": Disabled,
	}, CapabilityOverrides: map[string]Permission{}}
}

func ValidPermission(value Permission) bool {
	return value == Disabled || value == ConfirmEach || value == Allowed
}

func (policy *Policy) Normalize() error {
	if policy.Version == 0 {
		policy.Version = Version
	}
	if policy.Version != Version {
		return fmt.Errorf("unsupported capability policy version %d", policy.Version)
	}
	if policy.Revision == 0 {
		policy.Revision = 1
	}
	defaults := Default().RiskDefaults
	if policy.RiskDefaults == nil {
		policy.RiskDefaults = map[string]Permission{}
	}
	for risk, fallback := range defaults {
		if policy.RiskDefaults[risk] == "" {
			policy.RiskDefaults[risk] = fallback
		}
	}
	for risk, permission := range policy.RiskDefaults {
		if _, known := defaults[risk]; !known {
			return errors.New("invalid capability risk")
		}
		if !ValidPermission(permission) {
			return errors.New("invalid capability permission")
		}
	}
	if policy.CapabilityOverrides == nil {
		policy.CapabilityOverrides = map[string]Permission{}
	}
	for identifier, permission := range policy.CapabilityOverrides {
		if identifier == "" || !ValidPermission(permission) {
			return errors.New("invalid capability override")
		}
	}
	return nil
}

// Retired queue identifiers are read-only policy compatibility, never executable
// capabilities. Existing restrictions remain effective until explicitly migrated.
func (policy Policy) Decision(identifier, risk string) Permission {
	result := policy.decision(identifier, risk)
	switch identifier {
	case "docs.shortcut.create", "docs.shortcut.update":
		legacy := "docs.service.document.create"
		if identifier == "docs.shortcut.update" {
			legacy = "docs.service.document.append"
		}
		// These were separate effects of the same command; both decisions applied.
		return stricterPermission(result, policy.decision(legacy, "write"))
	case "docs.shortcut.overwrite", "drive.file.version.create":
		legacy := "docs.service.document.overwrite"
		if identifier == "drive.file.version.create" {
			legacy = "docbox.version"
		}
		if previous, ok := policy.CapabilityOverrides[legacy]; ok {
			if _, current := policy.CapabilityOverrides[identifier]; current {
				return stricterPermission(result, previous)
			}
			return previous
		}
	}
	return result
}

func stricterPermission(left, right Permission) Permission {
	if left == Disabled || right == Disabled {
		return Disabled
	}
	if left == ConfirmEach || right == ConfirmEach {
		return ConfirmEach
	}
	return Allowed
}

func (policy Policy) decision(identifier, risk string) Permission {
	if value := policy.CapabilityOverrides[identifier]; ValidPermission(value) {
		return value
	}
	if value := policy.RiskDefaults[risk]; ValidPermission(value) {
		return value
	}
	return Disabled
}

func (policy Policy) Digest() (string, error) {
	encoded, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type Store struct{ path string }

func NewStore(root string) Store {
	return Store{path: filepath.Join(root, "feishu-capability-policy-v1.json")}
}

func (store Store) Load() (Policy, error) {
	policy := Default()
	var encoded json.RawMessage
	missing, err := privatestore.ReadJSON(store.path, &encoded)
	if err != nil {
		return Policy{}, err
	}
	if !missing {
		if err := decodePolicy(encoded, &policy); err != nil {
			return Policy{}, err
		}
		if err := policy.Normalize(); err != nil {
			return Policy{}, err
		}
	}
	return policy, nil
}

func decodePolicy(data []byte, policy *Policy) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return errors.New("invalid capability policy object")
	}
	for key := range object {
		switch key {
		case "version", "revision", "riskDefaults", "capabilityOverrides", "updatedAt":
		default:
			return errors.New("invalid capability policy field")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 8 {
			return errors.New("invalid capability policy depth")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if token == json.Delim('{') {
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return errors.New("duplicate capability policy field")
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		if token == json.Delim('[') {
			return errors.New("invalid capability policy array")
		}
		return nil
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("invalid capability policy trailing input")
	}
	return json.Unmarshal(data, policy)
}

func (store Store) Save(policy Policy, expectedRevision uint64) (Policy, error) {
	var saved Policy
	err := privatestore.WithFileLock(store.path+".lock", func() error {
		current, err := store.Load()
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrRevisionConflict
		}
		if err := policy.Normalize(); err != nil {
			return err
		}
		policy.Revision = current.Revision + 1
		policy.UpdatedAt = time.Now().UTC()
		if err := privatestore.WriteJSON(store.path, policy); err != nil {
			return err
		}
		saved = policy
		return nil
	})
	return saved, err
}
