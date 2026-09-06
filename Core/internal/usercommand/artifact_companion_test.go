package usercommand

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBaseArtifactCompanionsMatchPinnedExportPaths(t *testing.T) {
	for _, mode := range []string{"both", "record-only", "manifest-only", "wrong-suffix"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			command, err := FreezeAt([]string{"base", "+record-list", "--base-token", "base_fixture", "--table-id", "tbl_fixture", "--view-id", "vew_fixture", "--format", "ndjson", "--output", "data/records.ndjson", "--as", "user"}, nil, root)
			if err != nil {
				t.Fatal(err)
			}
			command.Identity = testIdentity("user")
			if len(command.ArtifactPlan.Targets) != 2 || command.ArtifactPlan.Targets[1].Path != "data/records.manifest.json" {
				t.Fatalf("not the official resolveRecordExportPaths contract: %+v", command.ArtifactPlan)
			}
			execution, err := Materialize(command)
			if err != nil {
				t.Fatal(err)
			}
			defer execution.Close()
			files := []string{"data/records.ndjson", "data/records.manifest.json"}
			switch mode {
			case "record-only":
				files = files[:1]
			case "manifest-only":
				files = files[1:]
			case "wrong-suffix":
				files[1] = "data/records.ndjson.manifest.json"
			}
			for _, name := range files {
				if err := os.WriteFile(filepath.Join(execution.Dir, name), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			artifacts, err := execution.PublishArtifacts()
			if mode == "both" {
				if err != nil || len(artifacts) != 2 {
					t.Fatalf("%+v %v", artifacts, err)
				}
			} else if err == nil || len(artifacts) != 0 {
				t.Fatalf("incomplete pair reported success: %+v %v", artifacts, err)
			}
		})
	}
}
