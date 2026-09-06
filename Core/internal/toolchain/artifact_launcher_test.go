package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ksfassistant/core/internal/usercommand"
)

func TestLauncherPublishesDownloadBeforeSuccess(t *testing.T) {
	for _, mode := range []string{"success", "conflict", "upstream_failure", "missing"} {
		t.Run(mode, func(t *testing.T) {
			manager, _, _, _ := approvalFixture(t)
			root := t.TempDir()
			command, err := usercommand.FreezeAt([]string{"drive", "+download", "--file-token", "fixture", "--output", "report.txt", "--as", "user"}, nil, root)
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(t.TempDir(), "fake-cli")
			script := "#!/bin/sh\nprintf complete > report.txt\nprintf '{\"ok\":true,\"file\":\"report.txt\"}\\n'\n"
			if mode == "upstream_failure" {
				script += "exit 3\n"
			}
			if mode == "missing" {
				script = "#!/bin/sh\nprintf '{\"ok\":true}\\n'\n"
			}
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "report.txt")
			if mode == "conflict" {
				if err := os.WriteFile(destination, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var output, stderr bytes.Buffer
			code, err := manager.launchFrozen(context.Background(), binary, command, &output, &stderr)
			if mode == "success" {
				if code != 0 || err != nil {
					t.Fatalf("%d %v", code, err)
				}
				var result struct {
					Artifacts []usercommand.Artifact `json:"artifacts"`
				}
				if err := json.Unmarshal(output.Bytes(), &result); err != nil || len(result.Artifacts) != 1 {
					t.Fatalf("%s %v", output.String(), err)
				}
				data, err := os.ReadFile(result.Artifacts[0].Path)
				if err != nil || string(data) != "complete" {
					t.Fatal("download did not survive staging cleanup")
				}
			} else {
				if code == 0 || output.Len() != 0 {
					t.Fatalf("false success: %d %v %s", code, err, output.String())
				}
				data, readErr := os.ReadFile(destination)
				if mode == "conflict" && (readErr != nil || string(data) != "original") {
					t.Fatal("existing file changed")
				}
				if mode != "conflict" && !os.IsNotExist(readErr) {
					t.Fatal("failed execution published output")
				}
			}
		})
	}
}

func TestArtifactOutputBoundedAndNonJSONPreserved(t *testing.T) {
	var buffer artifactOutputBuffer
	if _, err := buffer.Write(bytes.Repeat([]byte("x"), 8*1024*1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("x")); err == nil || !buffer.exceeded {
		t.Fatal("output limit not enforced")
	}
	var output bytes.Buffer
	if err := writeArtifactOutput(&output, []byte("first\nsecond\n"), nil); err != nil || !strings.Contains(output.String(), `"upstreamOutput":"first\nsecond\n"`) {
		t.Fatal("non-JSON output lost")
	}
}

func TestLauncherPreservesBaseViewAndProjectedArtifact(t *testing.T) {
	manager, _, _, _ := approvalFixture(t)
	root := t.TempDir()
	command, err := usercommand.FreezeAt([]string{
		"base", "+record-list", "--base-token", "base_fixture", "--table-id", "tbl_fixture",
		"--view-id", "vew_fixture", "--field-id", "A", "--field-id", "a",
		"--format", "ndjson", "--output", "records.ndjson", "--limit", "2", "--offset", "0", "--as", "user",
	}, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "fake-cli")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >&2\nprintf '{\"A\":1,\"a\":2}\\n' > records.ndjson\nprintf '{\"has_more\":true,\"next_offset\":2}' > records.manifest.json\nprintf '{\"ok\":true,\"data\":{\"has_more\":true,\"next_offset\":2,\"query_context\":{\"view_id\":\"vew_fixture\",\"record_scope\":\"view_filtered_records\"}}}\\n'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	if code, err := manager.launchFrozen(context.Background(), binary, command, &output, &stderr); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	for _, expected := range []string{"--view-id\nvew_fixture", "--field-id\nA\n--field-id\na", "--format\nndjson", "--limit\n2", "--offset\n0"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("changed query: missing %q", expected)
		}
	}
	var result struct {
		Data struct {
			HasMore      bool `json:"has_more"`
			NextOffset   int  `json:"next_offset"`
			QueryContext struct {
				ViewID string `json:"view_id"`
			} `json:"query_context"`
		} `json:"data"`
		Artifacts []usercommand.Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || !result.Data.HasMore || result.Data.NextOffset != 2 || result.Data.QueryContext.ViewID != "vew_fixture" || len(result.Artifacts) != 2 {
		t.Fatalf("scope or pagination lost: %s", output.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "records.ndjson"))
	if err != nil || string(data) != "{\"A\":1,\"a\":2}\n" {
		t.Fatal("case-sensitive business fields changed")
	}
}
