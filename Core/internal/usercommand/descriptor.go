package usercommand

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
)

//go:embed execution-manifest.json
var executionManifest []byte

type ExecutionDescriptor struct {
	Path               string              `json:"path"`
	Kind               string              `json:"kind"`
	Description        string              `json:"description"`
	OfficialRisk       string              `json:"officialRisk"`
	Risk               string              `json:"risk"`
	Status             string              `json:"status"`
	Limitations        []string            `json:"limitations,omitempty"`
	CapabilityIDs      []string            `json:"capabilityIds"`
	Flags              []FlagDescriptor    `json:"flags"`
	Aliases            []string            `json:"aliases,omitempty"`
	Identities         []string            `json:"identities,omitempty"`
	Source             string              `json:"source"`
	SourceSHA256       string              `json:"sourceSha256,omitempty"`
	InputSchema        json.RawMessage     `json:"inputSchema,omitempty"`
	Artifacts          bool                `json:"artifacts,omitempty"`
	ArtifactCompanions []ArtifactCompanion `json:"artifactCompanions,omitempty"`
}

type ArtifactCompanion struct {
	Flag       string `json:"flag"`
	TrimSuffix string `json:"trimSuffix"`
	Suffix     string `json:"suffix"`
}

type FlagDescriptor struct {
	Hidden      bool     `json:"hidden,omitempty"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Default     string   `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Input       []string `json:"input,omitempty"`
	Role        string   `json:"role,omitempty"`
}

type manifest struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	Version          string                `json:"version"`
	Descriptors      []ExecutionDescriptor `json:"descriptors"`
	LocalDiagnostics []struct {
		Path       string `json:"path"`
		HelpSHA256 string `json:"helpSha256"`
	} `json:"localDiagnostics"`
}

func supportsGroupHelp(args []string) bool {
	if len(args) < 2 || !includes("--help -h --help=true", args[len(args)-1]) {
		return false
	}
	data, err := loadManifest()
	if err != nil {
		return false
	}
	for _, group := range data.LocalDiagnostics {
		parts := strings.Fields(group.Path)
		if len(group.HelpSHA256) == 64 && len(parts) == len(args)-1 && group.Path == strings.Join(args[:len(args)-1], " ") {
			return true
		}
	}
	return false
}

var manifestOnce sync.Once
var manifestData manifest
var manifestError error

func loadManifest() (manifest, error) {
	manifestOnce.Do(func() {
		manifestError = json.Unmarshal(executionManifest, &manifestData)
		if manifestError == nil && (manifestData.SchemaVersion != 1 || manifestData.Version != Version || len(manifestData.Descriptors) == 0) {
			manifestError = errors.New("user_command_manifest_invalid")
		}
	})
	return manifestData, manifestError
}

func ManifestDigest() string {
	digest := sha256.Sum256(executionManifest)
	return hex.EncodeToString(digest[:])
}

func CapabilitiesJSON() ([]byte, error) {
	if _, err := loadManifest(); err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(executionManifest, &object); err != nil {
		return nil, err
	}
	object["digest"], _ = json.Marshal(ManifestDigest())
	return json.Marshal(object)
}

func lookupDescriptor(args []string) (ExecutionDescriptor, int, bool) {
	data, err := loadManifest()
	if err != nil {
		return ExecutionDescriptor{}, 0, false
	}
	for _, descriptor := range data.Descriptors {
		for _, path := range append([]string{descriptor.Path}, descriptor.Aliases...) {
			parts := strings.Fields(path)
			if len(args) >= len(parts) && strings.Join(args[:len(parts)], " ") == path {
				return descriptor, len(parts), true
			}
		}
	}
	return ExecutionDescriptor{}, 0, false
}

func SupportsFlag(args []string, name string) bool {
	descriptor, _, ok := resolveDescriptor(args)
	if !ok || descriptor.Status == "restricted" {
		return false
	}
	name = strings.TrimPrefix(name, "--")
	for _, flag := range descriptor.Flags {
		if flag.Name == name {
			return true
		}
		for _, alias := range flag.Aliases {
			if alias == name {
				return true
			}
		}
	}
	return false
}

func Resolve(args []string) (ExecutionDescriptor, error) {
	if len(args) >= 3 && args[0] == "api" {
		for _, item := range []struct{ method, path, id string }{{"POST", "/open-apis/spark/v1/apps", "apps.app.create"}, {"PATCH", "/open-apis/spark/v1/apps/{app-id}", "apps.app.update"}, {"POST", "/open-apis/spark/v1/apps/{app-id}/releases", "apps.release.create"}} {
			if args[1] == item.method && args[2] == item.path {
				return ExecutionDescriptor{Path: strings.Join(args[:3], " "), Kind: "api", Status: "restricted", Risk: "unknown", CapabilityIDs: []string{item.id}, Source: "existing product CapabilityPublished=false"}, nil
			}
		}
	}
	if len(args) >= 3 && args[0] == "api" && strings.Contains(args[2], "{") {
		args = append([]string(nil), args...)
		args[2] = regexp.MustCompile(`\{[a-zA-Z0-9_-]+\}`).ReplaceAllString(args[2], "fixture")
	}
	descriptor, _, ok := resolveDescriptor(args)
	if !ok {
		return ExecutionDescriptor{}, ErrUnsupported
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return ExecutionDescriptor{}, err
	}
	var result ExecutionDescriptor
	err = json.Unmarshal(encoded, &result)
	return result, err
}

func ValidateArguments(args []string) error {
	_, err := parse(args)
	return err
}
