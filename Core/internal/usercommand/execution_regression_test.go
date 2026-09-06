package usercommand

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInlineIMContentUsesOfficialFieldContract(t *testing.T) {
	body := `{"text":"approved A/a","A":1,"a":2}`
	command := frozenTest(t, "user", "im", "+messages-send", "--chat-id", "oc_fixture", "--content", body)
	if len(command.Stdin) != 0 {
		t.Fatal("IM content was incorrectly rewritten to stdin")
	}
	parsed, err := parse(command.Args)
	if err != nil || parsed.flags["content"] != body {
		t.Fatal("IM inline content changed")
	}
	execution, err := Materialize(command)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	if !reflect.DeepEqual(command.Args, execution.Args) {
		t.Fatal("materialization changed body")
	}
}

func TestBusinessJSONAndRepeatableFlags(t *testing.T) {
	if !SupportsFlag([]string{"base", "+record-batch-create"}, "json") || SupportsFlag([]string{"im", "+messages-send"}, "token") {
		t.Fatal("command-level flag lookup failed")
	}
	command, err := Freeze([]string{"base", "+record-batch-create", "--base-token", "bascn_fixture", "--table-id", "tblfixture", "--json", `{"records":[{"fields":{"A":1,"a":2,"token":"business"}}]}`, "--as", "user"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	command.Identity = testIdentity("user")
	if _, err := Evaluate(command); err != nil {
		t.Fatal(err)
	}
	mail := frozenTest(t, "user", "mail", "+send", "--to", "a@example.test", "--to", "b@example.test", "--subject", "fixture", "--body", "body")
	parsed, err := parse(mail.Args)
	if err != nil || len(parsed.values["to"]) != 2 {
		t.Fatalf("repeatable array lost: %v", err)
	}
	if _, err := Freeze([]string{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "first", "--text", "second", "--as", "user"}, nil); err == nil {
		t.Fatal("scalar duplicate accepted")
	}
}

func TestAliasesAndExplicitBoundedPagination(t *testing.T) {
	command := frozenTest(t, "user", "sheets", "+cells-get", "--token", "shtcn_fixture", "--sheet-id", "sheet", "--range", "A1:B2")
	parsed, err := parse(command.Args)
	if err != nil || parsed.flags["spreadsheet-token"] != "shtcn_fixture" || parsed.flags["token"] != "" {
		t.Fatal("business token alias not canonicalized")
	}
	base := []string{"drive", "files", "list", "--as", "user", "--page-all"}
	if _, err := Freeze(base, nil); err == nil {
		t.Fatal("page-all silently used a default limit")
	}
	bounded := append(append([]string(nil), base...), "--page-limit", "3")
	command, err = Freeze(bounded, nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = parse(command.Args)
	if parsed.flags["page-limit"] != "3" {
		t.Fatal("explicit page budget changed")
	}
	if _, err := Freeze(append(base, "--page-limit", "0"), nil); err == nil {
		t.Fatal("unbounded pagination accepted")
	}
}

func TestFrozenUploadAllEffectsAndOriginalBasename(t *testing.T) {
	root := t.TempDir()
	name := "审阅附件.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("approved"), 0600); err != nil {
		t.Fatal(err)
	}
	command, err := FreezeAt([]string{"im", "+messages-send", "--chat-id", "oc_fixture", "--file", name, "--as", "user"}, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	command.Identity = testIdentity("user")
	review, err := Evaluate(command)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(review.CapabilityIDs, "im.files.create") {
		t.Fatal("hidden upload omitted from policy")
	}
	execution, err := Materialize(command)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	parsed, _ := parse(execution.Args)
	if filepath.Base(parsed.flags["file"]) != name {
		t.Fatal("approved attachment filename changed")
	}
	data, err := os.ReadFile(filepath.Join(execution.Dir, parsed.flags["file"]))
	if err != nil || string(data) != "approved" {
		t.Fatal("file not reconstructed")
	}
	command.Files[0].Data = append(command.Files[0].Data, '!')
	changed, _ := Evaluate(command)
	if changed.Digest == review.Digest {
		t.Fatal("upload bytes not bound")
	}
}

func TestStringReplacementUsesOverwritePolicy(t *testing.T) {
	command := frozenTest(t, "user", "docs", "+update", "--doc", "fixture", "--command", "str_replace", "--pattern", "old", "--content", "new", "--doc-format", "markdown")
	review, err := Evaluate(command)
	if err != nil || review.Risk != "destructive" || !contains(review.CapabilityIDs, "docs.service.document.overwrite") {
		t.Fatalf("replacement policy: %+v %v", review, err)
	}
}

func downloadFixture(t *testing.T, root string, output string) Command {
	t.Helper()
	args := []string{"im", "+messages-resources-download", "--message-id", "om_fixture", "--file-key", "file_fixture", "--type", "file", "--as", "user"}
	if output != "" {
		args = append(args, "--output", output)
	}
	command, err := FreezeAt(args, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	command.Identity = testIdentity("user")
	return command
}

func TestArtifactsPublishOnlyCompleteFilesToFrozenTarget(t *testing.T) {
	root := t.TempDir()
	command := downloadFixture(t, root, "report.txt")
	encoded, _ := json.Marshal(command)
	if _, err := Decode(encoded); err != nil {
		t.Fatal(err)
	}
	execution, err := Materialize(command)
	if err != nil {
		t.Fatal(err)
	}
	stage := execution.Dir
	if err := os.WriteFile(filepath.Join(stage, "report.txt"), []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	artifacts, err := execution.PublishArtifacts()
	if err != nil || len(artifacts) != 1 || artifacts[0].Path != filepath.Join(command.ArtifactPlan.Root, "report.txt") || artifacts[0].Bytes != 8 {
		t.Fatalf("%+v %v", artifacts, err)
	}
	if err := execution.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("stage not cleaned")
	}
	data, err := os.ReadFile(artifacts[0].Path)
	if err != nil || string(data) != "complete" {
		t.Fatal("published artifact destroyed")
	}
}

func TestArtifactConflictsSymlinksAndUnknownOutcomes(t *testing.T) {
	for _, mode := range []string{"conflict", "symlink", "unknown", "unplanned"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			command := downloadFixture(t, root, "report.txt")
			execution, err := Materialize(command)
			if err != nil {
				t.Fatal(err)
			}
			defer execution.Close()
			if mode == "symlink" {
				outside := filepath.Join(t.TempDir(), "outside.txt")
				_ = os.WriteFile(outside, []byte("secret fixture"), 0600)
				if err := os.Symlink(outside, filepath.Join(execution.Dir, "report.txt")); err != nil {
					t.Skip("host does not permit creating test symlinks")
				}
			} else {
				_ = os.WriteFile(filepath.Join(execution.Dir, "report.txt"), []byte("new"), 0600)
			}
			if mode == "conflict" {
				_ = os.WriteFile(filepath.Join(root, "report.txt"), []byte("original"), 0600)
			}
			if mode == "unplanned" {
				_ = os.WriteFile(filepath.Join(execution.Dir, "other.txt"), []byte("unplanned"), 0600)
			}
			if mode == "unknown" {
				_ = execution.Close()
			} else {
				if _, err := execution.PublishArtifacts(); err == nil {
					t.Fatal("unsafe artifact published")
				}
				if _, err := execution.PublishArtifacts(); err == nil {
					t.Fatal("publication retried")
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "report.txt"))
			if mode == "conflict" {
				if err != nil || string(data) != "original" {
					t.Fatal("existing output overwritten")
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("failed/unknown output published")
			}
		})
	}
}

func TestImplicitOutputDestinationAndPlanDigest(t *testing.T) {
	command := downloadFixture(t, t.TempDir(), "")
	before, err := Evaluate(command)
	if err != nil {
		t.Fatal(err)
	}
	if command.ArtifactPlan == nil || command.ArtifactPlan.Directory == "" || !strings.Contains(before.Details, command.ArtifactPlan.Directory) {
		t.Fatal("implicit destination is not exposed")
	}
	command.ArtifactPlan.Root = filepath.Join(command.ArtifactPlan.Root, "changed")
	after, err := Evaluate(command)
	if err != nil || before.Digest == after.Digest {
		t.Fatal("output plan not bound")
	}
}

func TestArtifactMissingRequiredAndEmptyOptional(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		output := ""
		if explicit {
			output = "required.txt"
		}
		execution, err := Materialize(downloadFixture(t, t.TempDir(), output))
		if err != nil {
			t.Fatal(err)
		}
		defer execution.Close()
		if _, err := execution.PublishArtifacts(); err == nil || err.Error() != "user_command_artifact_missing" {
			t.Fatalf("missing mandatory artifact accepted: %v", err)
		}
	}
	command, err := FreezeAt([]string{"im", "+chat-messages-list", "--chat-id", "oc_fixture", "--as", "user"}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command.Identity = testIdentity("user")
	execution, err := Materialize(command)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	if artifacts, err := execution.PublishArtifacts(); err != nil || len(artifacts) != 0 {
		t.Fatalf("optional empty result: %+v %v", artifacts, err)
	}
}

func TestArtifactPartialPublicationNeverRollsBackPublishedPaths(t *testing.T) {
	command := downloadFixture(t, t.TempDir(), ".")
	execution, err := Materialize(command)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	if err := os.Mkdir(filepath.Join(execution.Dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"a.txt", "nested/b.txt"} {
		if err := os.WriteFile(filepath.Join(execution.Dir, relative), []byte("complete"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(command.ArtifactPlan.Root, "nested")); err != nil {
		t.Skip("host does not permit creating test symlinks")
	}
	artifacts, err := execution.PublishArtifacts()
	if err == nil || len(artifacts) != 1 || artifacts[0].Path != filepath.Join(command.ArtifactPlan.Root, "a.txt") {
		t.Fatalf("partial delivery not reported: %+v %v", artifacts, err)
	}
	if err := os.WriteFile(artifacts[0].Path, []byte("user modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.PublishArtifacts(); err == nil {
		t.Fatal("partial publication replayed")
	}
	_ = execution.Close()
	data, err := os.ReadFile(artifacts[0].Path)
	if err != nil || string(data) != "user modified" {
		t.Fatal("published path removed or replaced during cleanup")
	}
}

func TestRestrictedCommandHelpNeverExecutesBusiness(t *testing.T) {
	for _, args := range [][]string{{"apps", "+db-sync-enable"}, {"docs", "+script"}} {
		command, err := Freeze(append(append([]string{}, args...), "--help"), nil)
		if err != nil {
			t.Fatal(err)
		}
		review, err := Evaluate(command)
		if err != nil || review.Identity != "local" || len(review.CapabilityIDs) != 0 {
			t.Fatalf("help is not local: %+v %v", review, err)
		}
		if _, err := Freeze(append(append([]string{}, args...), "--as", "user"), nil); err == nil {
			t.Fatal("restricted business admitted")
		}
		if _, err := Freeze(append(append([]string{}, args...), "--help=false"), nil); err == nil {
			t.Fatal("false help admitted")
		}
	}
}

func TestManifestGroupsAllowOnlyLocalHelp(t *testing.T) {
	manifest, err := loadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range manifest.LocalDiagnostics {
		if group.Path == "" {
			continue
		}
		for _, help := range []string{"--help", "-h", "--help=true"} {
			args := append(strings.Fields(group.Path), help)
			command, err := Freeze(args, nil)
			if err != nil {
				t.Fatalf("group help %v: %v", args, err)
			}
			review, err := Evaluate(command)
			if err != nil || review.Identity != "local" || len(review.Effects) != 0 || review.NeedsApproval {
				t.Fatalf("group help acquired execution effects: %+v %v", review, err)
			}
		}
		for _, suffix := range [][]string{nil, {"--as", "user"}, {"--help=false"}, {"--help", "--as", "user"}, {"unknown", "--help"}} {
			if _, err := Freeze(append(strings.Fields(group.Path), suffix...), nil); err == nil {
				t.Fatalf("group escaped help-only contract: %s %v", group.Path, suffix)
			}
		}
	}
	if _, err := Freeze([]string{"unknown-fixture", "--help"}, nil); err == nil {
		t.Fatal("unknown group help accepted")
	}
}

func TestReviewRendersEveryFrozenRepeatAndCountsAllValues(t *testing.T) {
	flag := FlagDescriptor{Name: "record-ids", Type: "string_slice", Input: []string{"file", "stdin"}}
	descriptor := ExecutionDescriptor{Path: "fixture +read", Flags: []FlagDescriptor{flag}}
	parsed := parsed{spec: specificationFor(descriptor), path: []string{"fixture", "+read"}, flags: map[string]string{"record-ids": "@@literal"}, values: map[string][]string{"record-ids": {"-", "@frozen-0/ids.txt", "@@literal"}}}
	command := Command{Stdin: []byte("rec_first,rec_second"), Files: []File{{Name: "frozen-0/ids.txt", DisplayName: "ids.txt", Data: []byte("rec_third")}}}
	_, details := describe(command, parsed)
	if !strings.Contains(details, "record-ids: rec_first,rec_second | rec_third | @literal") || !strings.Contains(details, "record-ids 数量: 4") {
		t.Fatalf("review lost repeated frozen values: %s", details)
	}
}

func TestBaseNDJSONAndBoundedSharePolicy(t *testing.T) {
	command, err := FreezeAt([]string{"base", "+record-list", "--base-token", "bascn_fixture", "--table-id", "tblfixture", "--format", "ndjson", "--output", "records.ndjson", "--as", "user"}, nil, t.TempDir())
	if err != nil || command.ArtifactPlan == nil || command.ArtifactPlan.Targets[0].Path != "records.ndjson" {
		t.Fatalf("official Base export contract rejected: %+v %v", command, err)
	}
	shared := frozenTest(t, "user", "base", "+record-share-link-create", "--base-token", "bascn_fixture", "--table-id", "tblfixture", "--record-ids", "rec_first,rec_second")
	review, err := Evaluate(shared)
	if err != nil || review.Risk != "high-impact-write" || !contains(review.CapabilityIDs, "base.shortcut.record.share.link.create") {
		t.Fatalf("share policy: %+v %v", review, err)
	}
	if _, err := Freeze([]string{"base", "+record-share-link-create", "--base-token", "bascn_fixture", "--table-id", "tblfixture", "--record-id", strings.Repeat("rec_fixture,", 100) + "rec_last", "--as", "user"}, nil); err == nil {
		t.Fatal("unbounded share targets accepted")
	}
}

func TestBoundedSheetsBatchChecksEveryChildPolicy(t *testing.T) {
	base := []string{"sheets", "+batch-update", "--spreadsheet-token", "shtcn_fixture", "--as", "user", "--operations"}
	body := `[{"shortcut":"+cells-set","input":{"sheet_id":"fixture","range":"A1:A1","cells":[["A/a"]]}},{"shortcut":"+sheet-delete","input":{"sheet_id":"other"}}]`
	command, err := Freeze(append(append([]string{}, base...), body), nil)
	if err != nil {
		t.Fatal(err)
	}
	command.Identity = testIdentity("user")
	review, err := Evaluate(command)
	if err != nil || !contains(review.CapabilityIDs, "sheets.shortcut.cells.set") || !contains(review.CapabilityIDs, "sheets.shortcut.sheet.delete") || review.Risk != "destructive" {
		t.Fatalf("child effects missing: %+v %v", review, err)
	}
	for _, bad := range []string{
		`[{"shortcut":"+batch-update","input":{}}]`,
		`[{"shortcut":"+cells-set","input":{"sheet_id":"fixture","range":"A1","cells":"@local.json"}}]`,
		`[{"shortcut":"+sheet-delete","input":{"sheet_id":"fixture","as":"bot"}}]`,
		`[{"shortcut":"+sheet-delete","input":{"sheet_id":"fixture","spreadsheet_token":"other"}}]`,
		`[{"shortcut":"+sheet-delete","input":{"sheet_id":"fixture","sheet-id":"other"}}]`,
		`[{"shortcut":"+chart-create","input":{}}]`,
		`[]`,
	} {
		if _, err := Freeze(append(append([]string{}, base...), bad), nil); err == nil {
			t.Fatalf("unsafe child accepted: %s", bad)
		}
	}
}

func TestMailSendIncludesImplicitDraftEffect(t *testing.T) {
	command := frozenTest(t, "user", "mail", "+send", "--to", "a@example.test", "--subject", "fixture", "--body", "body")
	review, err := Evaluate(command)
	if err != nil || !contains(review.CapabilityIDs, "mail.user_mailbox.drafts.create") {
		t.Fatalf("implicit draft omitted: %+v %v", review, err)
	}
}
