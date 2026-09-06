package usercommand

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExecutionEngineClosureAndLegacyAdapters(t *testing.T) {
	var data struct {
		Provenance struct {
			EngineFiles  map[string]string `json:"engineFiles"`
			EngineSHA256 string            `json:"engineSha256"`
		} `json:"provenance"`
		API     []ExecutionDescriptor `json:"apiDescriptors"`
		Aliases []struct {
			Path string   `json:"path"`
			IDs  []string `json:"capabilityIds"`
		} `json:"legacyPolicyAliases"`
	}
	if err := json.Unmarshal(executionManifest, &data); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "..")
	actual := map[string]string{}
	for _, folder := range []string{"Core/internal/usercommand", "Core/internal/capabilitypolicy"} {
		paths, err := filepath.Glob(filepath.Join(root, folder, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			bytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(bytes)
			actual[folder+"/"+filepath.Base(path)] = hex.EncodeToString(digest[:])
		}
	}
	if !reflect.DeepEqual(actual, data.Provenance.EngineFiles) {
		t.Fatal("non-test engine/policy source closure changed; regenerate reviewed manifest")
	}
	encoded, _ := json.Marshal(actual)
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != data.Provenance.EngineSHA256 {
		t.Fatal("engine closure aggregate changed")
	}
	if PolicyVersion != "ksfassistant-user-command-v2" {
		t.Fatal("approval policy contract version not upgraded")
	}
	if len(data.API) != len(apiSpecifications) {
		t.Fatal("raw API machine inventory incomplete")
	}
	for _, entry := range apiSpecifications {
		path := "api " + entry.method + " " + entry.pattern
		found := false
		for _, descriptor := range data.API {
			if descriptor.Path == path {
				found = true
				if descriptor.Risk != entry.spec.risk || !reflect.DeepEqual(descriptor.CapabilityIDs, strings.Fields(entry.spec.capabilities)) {
					t.Fatalf("API binding drift: %s", path)
				}
			}
		}
		if !found {
			t.Fatalf("missing API: %s", path)
		}
	}
	if len(data.Aliases) != len(legacySpecifications) {
		t.Fatal("legacy alias machine inventory incomplete")
	}
	for _, spec := range legacySpecifications {
		found := false
		for _, alias := range data.Aliases {
			if alias.Path == spec.path {
				found = true
				if !reflect.DeepEqual(alias.IDs, strings.Fields(spec.capabilities)) {
					t.Fatalf("alias drift: %s", spec.path)
				}
			}
		}
		if !found {
			t.Fatalf("missing alias: %s", spec.path)
		}
	}
}
