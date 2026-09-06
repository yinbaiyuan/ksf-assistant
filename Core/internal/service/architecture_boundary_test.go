package service

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFeishuAndBusinessIntegrationDependencyBoundaries(t *testing.T) {
	roots := map[string][]string{
		"../feishu":                             {"/internal/codex", "/internal/desktop", "/internal/bridge", "/internal/service", "/internal/integration", "/internal/corebridge"},
		"../../cmd/ksf-assistant-feishu-bridge": {"/internal/codex", "/internal/desktop", "/internal/bridge", "/internal/integration", "/internal/corebridge"},
		"../integration":                        {"/internal/feishu", "/internal/feishucommands", "larksuite", "os/exec"},
		"../feishucli":                          {"/internal/feishu", "/internal/feishucommands", "/internal/integration", "larksuite", "os/exec"},
	}
	for root, forbidden := range roots {
		t.Run(root, func(t *testing.T) {
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, entry.Name()), nil, parser.ImportsOnly)
				if err != nil {
					t.Fatal(err)
				}
				for _, dependency := range file.Imports {
					name, err := strconv.Unquote(dependency.Path.Value)
					if err != nil {
						t.Fatal(err)
					}
					for _, suffix := range forbidden {
						if strings.HasSuffix(name, suffix) || suffix == "larksuite" && strings.Contains(name, suffix) {
							t.Errorf("%s imports forbidden %s", entry.Name(), name)
						}
					}
				}
			}
		})
	}
}

func TestCoreNeverOwnsFeishuConfigurationFiles(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, constructor := range []string{"NewSettingsStore(", "NewSetupStore(", "NewClientConfigStore(", "NewAuditLog(", "LoadOfficialCredentials("} {
			if strings.Contains(string(data), constructor) {
				t.Errorf("Core %s directly accesses Feishu storage via %s", entry.Name(), constructor)
			}
		}
	}
}

func TestFeishuNormalizerDoesNotInterpretBusinessCards(t *testing.T) {
	data, err := os.ReadFile("../feishu/inbound_processor.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, businessToken := range []string{"task_link_", "feishu_bridge", "questionRevision", "planRevision", "linkId", "taskKey"} {
		if strings.Contains(string(data), businessToken) {
			t.Errorf("Feishu normalizer interprets business field %s", businessToken)
		}
	}
}
