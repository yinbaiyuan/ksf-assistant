package feishu

import "ksfassistant/core/internal/capabilitypolicy"

const CapabilityPolicyVersion = capabilitypolicy.Version

type CapabilityPermission = capabilitypolicy.Permission

const (
	CapabilityDisabled    = capabilitypolicy.Disabled
	CapabilityConfirmEach = capabilitypolicy.ConfirmEach
	CapabilityAllowed     = capabilitypolicy.Allowed
)

var ErrCapabilityPolicyRevisionConflict = capabilitypolicy.ErrRevisionConflict

type CapabilityPolicy capabilitypolicy.Policy

func DefaultCapabilityPolicy() CapabilityPolicy {
	return CapabilityPolicy(capabilitypolicy.Default())
}

func validCapabilityPermission(value CapabilityPermission) bool {
	return capabilitypolicy.ValidPermission(value)
}

func (policy *CapabilityPolicy) normalize() error {
	return (*capabilitypolicy.Policy)(policy).Normalize()
}

func (policy CapabilityPolicy) Decision(definition CapabilityDefinition) CapabilityPermission {
	return capabilitypolicy.Policy(policy).Decision(definition.ID, definition.Risk)
}

type CapabilityPolicyStore struct{ store capabilitypolicy.Store }

func NewCapabilityPolicyStore(dataRoot string) CapabilityPolicyStore {
	return CapabilityPolicyStore{store: capabilitypolicy.NewStore(dataRoot)}
}

func (store CapabilityPolicyStore) Load() (CapabilityPolicy, error) {
	policy, err := store.store.Load()
	return CapabilityPolicy(policy), err
}

func (store CapabilityPolicyStore) Save(policy CapabilityPolicy, expectedRevision uint64) (CapabilityPolicy, error) {
	saved, err := store.store.Save(capabilitypolicy.Policy(policy), expectedRevision)
	return CapabilityPolicy(saved), err
}
