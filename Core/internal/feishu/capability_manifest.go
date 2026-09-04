package feishu

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

//go:embed contracts/capability-manifest-1.0.0.json
var capabilityContracts embed.FS

const CapabilityCount = 219

type CapabilityManifest struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	BridgeVersion  string                 `json:"bridgeVersion"`
	LarkCLIVersion string                 `json:"larkCliVersion"`
	Capabilities   []CapabilityDefinition `json:"capabilities"`
}

type CapabilityDefinition struct {
	ID                string                     `json:"id"`
	Domain            string                     `json:"domain"`
	Risk              string                     `json:"risk"`
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
	Redaction         map[string]any             `json:"redaction"`
	Transform         string                     `json:"transform"`
	CLIConfirm        bool                       `json:"cliConfirm"`
}

type CapabilityStep struct {
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
		frozenManifestError = validateCapabilityManifest(frozenManifest)
	})
	return frozenManifest, frozenManifestError
}

func validateCapabilityManifest(manifest CapabilityManifest) error {
	if manifest.SchemaVersion != 1 || manifest.BridgeVersion != "1.0.0" || manifest.LarkCLIVersion != "1.0.92" {
		return errors.New("unsupported Feishu capability manifest")
	}
	if len(manifest.Capabilities) != CapabilityCount {
		return fmt.Errorf("capability manifest contains %d entries, expected %d", len(manifest.Capabilities), CapabilityCount)
	}
	seen := map[string]bool{}
	for _, capability := range manifest.Capabilities {
		if capability.ID == "" || capability.Domain == "" || seen[capability.ID] {
			return fmt.Errorf("invalid or duplicate capability id %q", capability.ID)
		}
		seen[capability.ID] = true
		if capability.Risk != "read" && capability.Risk != "write" && capability.Risk != "high-impact-write" && capability.Risk != "remote-operation" {
			return fmt.Errorf("invalid risk for %s", capability.ID)
		}
		if len(capability.Command) == 0 {
			return fmt.Errorf("missing command for %s", capability.ID)
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
