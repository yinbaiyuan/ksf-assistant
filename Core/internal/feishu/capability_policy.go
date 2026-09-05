package feishu

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const CapabilityPolicyVersion = 1

type CapabilityPermission string

const (
	CapabilityDisabled    CapabilityPermission = "disabled"
	CapabilityConfirmEach CapabilityPermission = "confirm_each"
	CapabilityAllowed     CapabilityPermission = "allowed"
)

var ErrCapabilityPolicyRevisionConflict = errors.New("capability_policy_revision_conflict")

type CapabilityPolicy struct {
	Version             int                             `json:"version"`
	Revision            uint64                          `json:"revision"`
	RiskDefaults        map[string]CapabilityPermission `json:"riskDefaults"`
	CapabilityOverrides map[string]CapabilityPermission `json:"capabilityOverrides"`
	UpdatedAt           time.Time                       `json:"updatedAt"`
}

func DefaultCapabilityPolicy() CapabilityPolicy {
	return CapabilityPolicy{
		Version:  CapabilityPolicyVersion,
		Revision: 1,
		RiskDefaults: map[string]CapabilityPermission{
			"read":              CapabilityAllowed,
			"write":             CapabilityAllowed,
			"high-impact-write": CapabilityConfirmEach,
			"remote-operation":  CapabilityConfirmEach,
			"destructive":       CapabilityDisabled,
		},
		CapabilityOverrides: map[string]CapabilityPermission{},
	}
}

func validCapabilityPermission(value CapabilityPermission) bool {
	return value == CapabilityDisabled || value == CapabilityConfirmEach || value == CapabilityAllowed
}

func (policy *CapabilityPolicy) normalize() error {
	if policy.Version == 0 {
		policy.Version = CapabilityPolicyVersion
	}
	if policy.Version != CapabilityPolicyVersion {
		return fmt.Errorf("unsupported capability policy version %d", policy.Version)
	}
	if policy.Revision == 0 {
		policy.Revision = 1
	}
	defaults := DefaultCapabilityPolicy().RiskDefaults
	if policy.RiskDefaults == nil {
		policy.RiskDefaults = map[string]CapabilityPermission{}
	}
	for risk, fallback := range defaults {
		if policy.RiskDefaults[risk] == "" {
			policy.RiskDefaults[risk] = fallback
		}
	}
	for risk, permission := range policy.RiskDefaults {
		if _, ok := defaults[risk]; !ok {
			return fmt.Errorf("invalid capability risk %q", risk)
		}
		if !validCapabilityPermission(permission) {
			return fmt.Errorf("invalid capability permission %q for risk %q", permission, risk)
		}
	}
	if policy.CapabilityOverrides == nil {
		policy.CapabilityOverrides = map[string]CapabilityPermission{}
	}
	for id, permission := range policy.CapabilityOverrides {
		if id == "" || !validCapabilityPermission(permission) {
			return fmt.Errorf("invalid capability override %q", id)
		}
	}
	return nil
}

func (policy CapabilityPolicy) Decision(definition CapabilityDefinition) CapabilityPermission {
	if value := policy.CapabilityOverrides[definition.ID]; validCapabilityPermission(value) {
		return value
	}
	if value := policy.RiskDefaults[definition.Risk]; validCapabilityPermission(value) {
		return value
	}
	return CapabilityDisabled
}

type CapabilityPolicyStore struct{ path string }

func NewCapabilityPolicyStore(dataRoot string) CapabilityPolicyStore {
	return CapabilityPolicyStore{path: filepath.Join(dataRoot, "feishu-capability-policy-v1.json")}
}

func (store CapabilityPolicyStore) Load() (CapabilityPolicy, error) {
	policy := DefaultCapabilityPolicy()
	missing, err := readPrivateJSON(store.path, &policy)
	if missing {
		return policy, nil
	}
	if err != nil {
		return CapabilityPolicy{}, err
	}
	if err := policy.normalize(); err != nil {
		return CapabilityPolicy{}, err
	}
	return policy, nil
}

func (store CapabilityPolicyStore) Save(policy CapabilityPolicy, expectedRevision uint64) (CapabilityPolicy, error) {
	var saved CapabilityPolicy
	err := withProcessFileLock(store.path+".lock", func() error {
		current, err := store.Load()
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrCapabilityPolicyRevisionConflict
		}
		if err := policy.normalize(); err != nil {
			return err
		}
		policy.Revision = current.Revision + 1
		policy.UpdatedAt = time.Now().UTC()
		if err := writePrivateJSON(store.path, policy); err != nil {
			return err
		}
		saved = policy
		return nil
	})
	return saved, err
}
