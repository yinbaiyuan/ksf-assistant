package feishu

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed contracts/capability-manifest-1.0.0.json contracts/capability-additions-2.0.0.json contracts/capability-governance-2.0.0.json
var capabilityContracts embed.FS

const (
	LegacyCapabilityCount     = 218
	CapabilityRegistryV2Count = 812
)

type CapabilityManifest struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	BridgeVersion  string                 `json:"bridgeVersion"`
	LarkCLIVersion string                 `json:"larkCliVersion"`
	Capabilities   []CapabilityDefinition `json:"capabilities"`
}

type capabilityGovernanceManifest struct {
	SchemaVersion int                          `json:"schemaVersion"`
	BridgeVersion string                       `json:"bridgeVersion"`
	Capabilities  []capabilityGovernanceRecord `json:"capabilities"`
}

type capabilityGovernanceRecord struct {
	ID             string                   `json:"id"`
	Backend        string                   `json:"backend"`
	Risk           string                   `json:"risk"`
	Effect         string                   `json:"effect"`
	Reversibility  string                   `json:"reversibility"`
	GuardProfile   CapabilityGuardProfile   `json:"guardProfile"`
	Preflight      *CapabilityStep          `json:"preflight,omitempty"`
	Reread         *CapabilityStep          `json:"reread,omitempty"`
	Postcondition  *CapabilityPostcondition `json:"postcondition,omitempty"`
	ConflictKey    []string                 `json:"conflictKey"`
	RetryClass     string                   `json:"retryClass"`
	ExecutionClass string                   `json:"executionClass"`
}

type CapabilityDefinition struct {
	ID                string                     `json:"id"`
	Domain            string                     `json:"domain"`
	Risk              string                     `json:"risk"`
	Backend           string                     `json:"backend,omitempty"`
	Effect            string                     `json:"effect,omitempty"`
	Reversibility     string                     `json:"reversibility,omitempty"`
	GuardProfile      CapabilityGuardProfile     `json:"guardProfile,omitempty"`
	Postcondition     *CapabilityPostcondition   `json:"postcondition,omitempty"`
	ConflictKey       []string                   `json:"conflictKey,omitempty"`
	RetryClass        string                     `json:"retryClass,omitempty"`
	ExecutionClass    string                     `json:"executionClass,omitempty"`
	Identity          string                     `json:"identity"`
	Queue             string                     `json:"queue"`
	Transport         string                     `json:"transport"`
	Command           []string                   `json:"command"`
	FixedArgs         []string                   `json:"fixedArgs"`
	APIPath           string                     `json:"apiPath"`
	Flags             map[string]CapabilityField `json:"flags"`
	FlagOrder         []string                   `json:"flagOrder"`
	Scope             CapabilityScope            `json:"scope"`
	Preflight         *CapabilityStep            `json:"preflight"`
	Reread            *CapabilityStep            `json:"reread"`
	Poll              *CapabilityPoll            `json:"poll"`
	ResultIdentifiers []json.RawMessage          `json:"resultIdentifiers"`
	RequiredScopes    []string                   `json:"requiredScopes,omitempty"`
	Redaction         map[string]any             `json:"redaction"`
	Transform         string                     `json:"transform"`
	CLIConfirm        bool                       `json:"cliConfirm"`
}

type CapabilityGuardProfile string

const (
	CapabilityGuardNone    CapabilityGuardProfile = "none"
	CapabilityGuardStrong  CapabilityGuardProfile = "strong"
	CapabilityGuardBounded CapabilityGuardProfile = "bounded"
)

// CapabilityPostcondition describes the reviewed strength of the evidence
// available after a side effect. It is deliberately declarative: callers may
// not provide an arbitrary expression or remote method.
type CapabilityPostcondition struct {
	Kind string `json:"kind"`
}

type CapabilityStep struct {
	Kind     string            `json:"kind,omitempty"`
	ID       string            `json:"id"`
	Map      map[string]string `json:"map"`
	Defaults map[string]any    `json:"defaults"`
}

type CapabilityPoll struct {
	CapabilityStep
	Statuses []string `json:"statuses"`
}

type CapabilityPattern struct {
	Regex string `json:"regex"`
	Flags string `json:"flags"`
}
type CapabilityField struct {
	Type        string            `json:"type"`
	Required    bool              `json:"required"`
	Private     bool              `json:"private"`
	Stdin       bool              `json:"stdin"`
	Output      bool              `json:"output"`
	Path        bool              `json:"path"`
	Max         int               `json:"max"`
	Min         int               `json:"min"`
	MaxItems    int               `json:"maxItems"`
	MaxBytes    int               `json:"maxBytes"`
	Values      []string          `json:"values"`
	Body        any               `json:"body"`
	Pattern     CapabilityPattern `json:"pattern"`
	ItemPattern CapabilityPattern `json:"itemPattern"`
}
type CapabilityScope struct {
	Bounded           bool       `json:"bounded"`
	RequireAny        []string   `json:"requireAny"`
	RequireExactlyOne []string   `json:"requireExactlyOne"`
	MutuallyExclusive []string   `json:"mutuallyExclusive"`
	RequiredGroups    [][]string `json:"requiredGroups"`
}

var manifestOnce sync.Once
var frozenManifest CapabilityManifest
var frozenManifestError error

func LoadCapabilityManifest() (CapabilityManifest, error) {
	manifestOnce.Do(func() {
		data, err := capabilityContracts.ReadFile("contracts/capability-manifest-1.0.0.json")
		if err != nil {
			frozenManifestError = err
			return
		}
		if err := json.Unmarshal(data, &frozenManifest); err != nil {
			frozenManifestError = err
			return
		}
		var additions CapabilityManifest
		data, err = capabilityContracts.ReadFile("contracts/capability-additions-2.0.0.json")
		if err != nil {
			frozenManifestError = err
			return
		}
		if err := json.Unmarshal(data, &additions); err != nil {
			frozenManifestError = err
			return
		}
		if additions.SchemaVersion != 2 || additions.BridgeVersion != "2.0.0" || additions.LarkCLIVersion != frozenManifest.LarkCLIVersion {
			frozenManifestError = errors.New("unsupported Feishu capability additions")
			return
		}
		frozenManifest.SchemaVersion = 2
		frozenManifest.BridgeVersion = "2.0.0"
		frozenManifest.Capabilities = append(frozenManifest.Capabilities, additions.Capabilities...)
		frozenManifest.Capabilities = append(frozenManifest.Capabilities, serviceCapabilityDefinitions()...)
		data, err = capabilityContracts.ReadFile("contracts/capability-governance-2.0.0.json")
		if err != nil {
			frozenManifestError = err
			return
		}
		var governance capabilityGovernanceManifest
		if err := json.Unmarshal(data, &governance); err != nil {
			frozenManifestError = err
			return
		}
		if err := applyCapabilityGovernance(&frozenManifest, governance); err != nil {
			frozenManifestError = err
			return
		}
		frozenManifestError = validateCapabilityManifest(frozenManifest)
	})
	return frozenManifest, frozenManifestError
}

func applyCapabilityGovernance(manifest *CapabilityManifest, governance capabilityGovernanceManifest) error {
	if governance.SchemaVersion != 2 || governance.BridgeVersion != "2.0.0" {
		return errors.New("unsupported Feishu capability governance")
	}
	if len(governance.Capabilities) != len(manifest.Capabilities) {
		return fmt.Errorf("capability governance contains %d entries, expected %d", len(governance.Capabilities), len(manifest.Capabilities))
	}
	definitions := make(map[string]*CapabilityDefinition, len(manifest.Capabilities))
	for index := range manifest.Capabilities {
		definition := &manifest.Capabilities[index]
		if definition.ID == "" || definitions[definition.ID] != nil {
			return fmt.Errorf("invalid capability definition %q", definition.ID)
		}
		definitions[definition.ID] = definition
	}
	seen := map[string]bool{}
	for _, record := range governance.Capabilities {
		definition := definitions[record.ID]
		if definition == nil || seen[record.ID] {
			return fmt.Errorf("unknown or duplicate capability governance %q", record.ID)
		}
		seen[record.ID] = true
		definition.Backend = record.Backend
		definition.Risk = record.Risk
		definition.Effect = record.Effect
		definition.Reversibility = record.Reversibility
		definition.GuardProfile = record.GuardProfile
		definition.Preflight = record.Preflight
		definition.Reread = record.Reread
		definition.Postcondition = record.Postcondition
		definition.ConflictKey = append([]string(nil), record.ConflictKey...)
		definition.RetryClass = record.RetryClass
		definition.ExecutionClass = record.ExecutionClass
	}
	return nil
}

func serviceCapabilityDefinitions() []CapabilityDefinition {
	identifier := CapabilityField{Type: "string", Required: true, Private: true, Max: 400, Pattern: CapabilityPattern{Regex: `^[A-Za-z0-9_-]{3,400}$`}}
	definitions := []CapabilityDefinition{{
		ID: "im.sdk.message.send", Domain: "im", Risk: "high-impact-write", Backend: "go-sdk", Effect: "send", Reversibility: "irreversible",
		Identity: "bot", Queue: "actionbox", Transport: "sdk", Command: []string{"sdk", "message-send"},
		Flags: map[string]CapabilityField{
			"request-id":  {Type: "string", Required: true, Pattern: CapabilityPattern{Regex: `^OUT-[A-Za-z0-9_-]{3,120}$`}},
			"target-type": {Type: "enum", Required: true, Values: []string{"chat_id", "open_id"}},
			"target-id":   identifier,
			"format":      {Type: "enum", Required: true, Values: []string{"text", "markdown", "card", "image", "file"}},
			"text":        {Type: "string", Private: true, Max: 500_000},
			"file-path":   {Type: "path", Private: true},
			"source":      {Type: "string", Required: true, Max: 100},
			"dry-run":     {Type: "boolean"},
		},
		FlagOrder:      []string{"request-id", "target-type", "target-id", "format", "text", "file-path", "source", "dry-run"},
		Scope:          CapabilityScope{Bounded: true, RequireAny: []string{"text", "file-path"}},
		RequiredScopes: []string{"im:message"}, Redaction: map[string]any{"body": true, "identifiers": true},
	}}
	definitions = append(definitions, approvalEventSubscriptionDefinitions()...)
	return definitions
}

// BuiltinServiceCapabilityDefinitions exposes the fixed service-backed
// definitions to the asset generator. Runtime callers should use the loaded
// manifest, whose governance is pinned by the versioned governance asset.
func BuiltinServiceCapabilityDefinitions() []CapabilityDefinition {
	return serviceCapabilityDefinitions()
}

func approvalEventSubscriptionDefinitions() []CapabilityDefinition {
	subscriptionType := CapabilityField{Type: "enum", Required: true, Values: []string{"INVOLVED_APPROVAL", "MANAGED_APPROVAL"}}
	definition := func(id, method, path, effect, scope string, body bool) CapabilityDefinition {
		field := subscriptionType
		if body {
			field.Body = "subscription_type"
		}
		return CapabilityDefinition{
			ID: id, Domain: "approval", Risk: "high-impact-write", Backend: "lark-cli", Effect: effect, Reversibility: "reversible",
			Identity: "user", Queue: "actionbox", Transport: "raw", Command: []string{"api", method}, APIPath: path,
			Flags: map[string]CapabilityField{"subscription-type": field}, FlagOrder: []string{"subscription-type"},
			Scope: CapabilityScope{Bounded: true}, RequiredScopes: []string{scope},
			Redaction: map[string]any{"identifiers": true, "body": true}, ResultIdentifiers: []json.RawMessage{},
		}
	}
	return []CapabilityDefinition{
		definition("approval.events.instance.subscribe", "POST", "/open-apis/approval/v4/instances/subscription", "subscribe", "approval:instance:read", true),
		definition("approval.events.instance.unsubscribe", "DELETE", "/open-apis/approval/v4/instances/subscription", "unsubscribe", "approval:instance:read", false),
		definition("approval.events.task.subscribe", "POST", "/open-apis/approval/v4/tasks/subscription", "subscribe", "approval:task:read", true),
		definition("approval.events.task.unsubscribe", "DELETE", "/open-apis/approval/v4/tasks/subscription", "unsubscribe", "approval:task:read", false),
	}
}

func validateCapabilityManifest(manifest CapabilityManifest) error {
	if manifest.SchemaVersion != 2 || manifest.BridgeVersion != "2.0.0" || (manifest.LarkCLIVersion != "1.0.92" && manifest.LarkCLIVersion != PinnedLarkCLIVersion) {
		return errors.New("unsupported Feishu capability manifest")
	}
	if len(manifest.Capabilities) != CapabilityRegistryV2Count {
		return fmt.Errorf("capability manifest contains %d entries, expected frozen v2 count %d", len(manifest.Capabilities), CapabilityRegistryV2Count)
	}
	seen := map[string]bool{}
	for _, capability := range manifest.Capabilities {
		if capability.ID == "" || capability.Domain == "" || seen[capability.ID] {
			return fmt.Errorf("invalid or duplicate capability id %q", capability.ID)
		}
		seen[capability.ID] = true
		if capability.Risk != "read" && capability.Risk != "write" && capability.Risk != "high-impact-write" && capability.Risk != "remote-operation" && capability.Risk != "destructive" {
			return fmt.Errorf("invalid risk for %s", capability.ID)
		}
		if len(capability.Command) == 0 {
			return fmt.Errorf("missing command for %s", capability.ID)
		}
		if capability.Backend != "lark-cli" && capability.Backend != "go-sdk" {
			return fmt.Errorf("invalid backend for %s", capability.ID)
		}
		if capability.Effect == "" || capability.Reversibility == "" {
			return fmt.Errorf("missing governance metadata for %s", capability.ID)
		}
		if capability.RetryClass == "" || capability.ExecutionClass == "" || len(capability.ConflictKey) == 0 {
			return fmt.Errorf("missing execution governance metadata for %s", capability.ID)
		}
		if capability.Risk == "destructive" {
			if capability.GuardProfile != CapabilityGuardStrong && capability.GuardProfile != CapabilityGuardBounded {
				return fmt.Errorf("missing destructive guard profile for %s", capability.ID)
			}
			if capability.Postcondition == nil || capability.Postcondition.Kind == "" {
				return fmt.Errorf("missing destructive postcondition for %s", capability.ID)
			}
			if capability.Preflight == nil || capability.Preflight.ID == "" {
				return fmt.Errorf("missing destructive preflight for %s", capability.ID)
			}
			if capability.GuardProfile == CapabilityGuardStrong && capability.Reread == nil && capability.Poll == nil {
				return fmt.Errorf("strong destructive guard cannot verify %s", capability.ID)
			}
			if capability.RetryClass != "never" {
				return fmt.Errorf("destructive capability may not be replayed: %s", capability.ID)
			}
		}
		if capability.Transport == "raw" && capability.Command[0] == "api" && capability.APIPath == "" {
			return fmt.Errorf("arbitrary raw API is forbidden for %s", capability.ID)
		}
		if len(capability.FlagOrder) != len(capability.Flags) {
			return fmt.Errorf("invalid flag order for %s", capability.ID)
		}
		flagSeen := map[string]bool{}
		for _, name := range capability.FlagOrder {
			if _, ok := capability.Flags[name]; !ok || flagSeen[name] {
				return fmt.Errorf("invalid flag order for %s", capability.ID)
			}
			flagSeen[name] = true
		}
	}
	return nil
}

func CapabilityByID(id string) (CapabilityDefinition, bool) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		return CapabilityDefinition{}, false
	}
	for _, capability := range manifest.Capabilities {
		if capability.ID == id {
			return capability, true
		}
	}
	return CapabilityDefinition{}, false
}

// CapabilityPublished is the final service-side allowlist. Frozen registry
// entries remain readable for replay compatibility, but excluded management
// surfaces can never be executed through CLI, RPC, queues, or inbound events.
func CapabilityPublished(definition CapabilityDefinition) bool {
	id := strings.ToLower(definition.ID)
	if id == "apps.app.create" || id == "apps.app.update" || id == "apps.release.create" {
		return false
	}
	for _, token := range []string{".permission.", ".permissions.", ".admin.", ".administrator.", ".role.", ".automation.", ".credential.", ".secret.", ".urgent.", ".sms.", ".phone."} {
		if strings.Contains(id, token) {
			return false
		}
	}
	return true
}

func orderedCapabilityFlags(definition CapabilityDefinition) []string {
	if len(definition.FlagOrder) > 0 {
		return definition.FlagOrder
	}
	values := make([]string, 0, len(definition.Flags))
	for name := range definition.Flags {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}
