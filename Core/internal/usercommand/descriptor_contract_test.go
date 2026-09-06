package usercommand

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Keep this reader separate from the execution loader: dropped JSON fields and
// incomplete provenance must fail the contract even if execution still loads.
type contractManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Version       string `json:"version"`
	Digest        string `json:"digest"`
	Counts        struct {
		Typed     int `json:"typed"`
		Shortcuts int `json:"shortcuts"`
	} `json:"counts"`
	Provenance struct {
		BinaryArtifacts      map[string]string `json:"binaryArtifacts"`
		SchemaSHA256         string            `json:"schemaSha256"`
		SourceArchiveSHA256  string            `json:"sourceArchiveSha256"`
		SkillsSHA256         string            `json:"skillsSha256"`
		OverlaySHA256        string            `json:"overlaySha256"`
		ProductCatalogSHA256 string            `json:"productCatalogSha256"`
		GeneratorSHA256      string            `json:"generatorSha256"`
		ASTExtractorSHA256   string            `json:"astExtractorSha256"`
		ReviewAdapterSHA256  string            `json:"reviewAdapterSha256"`
	} `json:"provenance"`
	Descriptors []struct {
		ExecutionDescriptor
		HelpSHA256 string `json:"helpSha256"`
		RiskSource string `json:"riskSource"`
	} `json:"descriptors"`
	SkillCallCounts  map[string]int `json:"skillCallCounts"`
	LocalDiagnostics []struct {
		Path       string `json:"path"`
		HelpSHA256 string `json:"helpSha256"`
	} `json:"localDiagnostics"`
	UpstreamMissingChecks []struct {
		Path               string   `json:"path"`
		HelpSHA256         string   `json:"helpSha256"`
		ObservedUsagePaths []string `json:"observedUsagePaths"`
	} `json:"upstreamMissingChecks"`
	Skills []struct {
		Name  string `json:"name"`
		Files []struct {
			File           string   `json:"file"`
			SHA256         string   `json:"sha256"`
			Classification string   `json:"classification"`
			Commands       []string `json:"commands"`
			Calls          []struct {
				Invocation     string `json:"invocation"`
				Canonical      string `json:"canonical"`
				Classification string `json:"classification"`
				Alias          bool   `json:"alias"`
				Reason         string `json:"reason"`
				Line           int    `json:"line"`
				ExampleContext string `json:"exampleContext"`
				RawLine        string `json:"rawLine"`
				RawInvocation  string `json:"rawInvocation"`
				DiagnosticPath string `json:"diagnosticPath"`
				MissingPath    string `json:"missingPath"`
				Evidence       string `json:"evidence"`
			} `json:"calls"`
		} `json:"files"`
	} `json:"skills"`
}

func readContractManifest(t *testing.T) contractManifest {
	t.Helper()
	data, err := CapabilitiesJSON()
	if err != nil {
		t.Fatal(err)
	}
	var manifest contractManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func contractSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func contractFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Only these six reviewed corrections may lower an official write label. Each
// must retain both a published read classification and an explicit overlay rule.
func contractReviewedReadCorrections(t *testing.T) map[string]string {
	t.Helper()
	corrections := map[string]string{
		"approval approvals search":            "approval.approvals.search",
		"attendance user_tasks query":          "attendance.user_tasks.query",
		"mail multi_entity search":             "mail.multi_entity.search",
		"mail user_mailbox.messages batch_get": "mail.user_mailbox.messages.batch_get",
		"mail user_mailboxes search":           "mail.user_mailboxes.search",
		"im +messages-resources-download":      "im.shortcut.messages.resources.download",
	}
	var catalog struct {
		Catalog []struct {
			ID   string `json:"id"`
			Risk string `json:"risk"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(contractFile(t, filepath.Join("..", "feishucli", "catalog.json")), &catalog); err != nil {
		t.Fatal(err)
	}
	readIDs := map[string]bool{}
	for _, item := range catalog.Catalog {
		if item.Risk == "read" {
			readIDs[item.ID] = true
		}
	}
	var overlay struct {
		Rules []struct {
			Paths       []string `json:"paths"`
			Risk        string   `json:"risk"`
			Limitations []string `json:"limitations"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(contractFile(t, "execution-overlay.json"), &overlay); err != nil {
		t.Fatal(err)
	}
	explicit := map[string]bool{}
	for _, rule := range overlay.Rules {
		if rule.Risk == "read" && len(rule.Limitations) > 0 {
			for _, path := range rule.Paths {
				explicit[path] = true
			}
		}
	}
	for path, id := range corrections {
		if (!readIDs[id] && path != "im +messages-resources-download") || !explicit[path] {
			t.Errorf("reviewed read correction %q lacks a published read source or explicit explanatory overlay", path)
		}
	}
	return corrections
}

func TestDescriptorManifestContract(t *testing.T) {
	m := readContractManifest(t)
	readCorrections := contractReviewedReadCorrections(t)
	if m.SchemaVersion != 1 || m.Version != Version || m.Counts.Typed != 250 || m.Counts.Shortcuts != 526 || len(m.Descriptors) != 776 {
		t.Fatalf("unexpected manifest inventory: schema=%d version=%q typed=%d shortcuts=%d descriptors=%d", m.SchemaVersion, m.Version, m.Counts.Typed, m.Counts.Shortcuts, len(m.Descriptors))
	}
	if m.Digest != ManifestDigest() || m.Digest != contractSHA256(executionManifest) {
		t.Fatal("capabilities digest does not identify the embedded manifest bytes")
	}
	shaPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	namePattern := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	paths := map[string]bool{}
	kinds := map[string]int{}
	for _, d := range m.Descriptors {
		t.Run(d.Path, func(t *testing.T) {
			for _, path := range append([]string{d.Path}, d.Aliases...) {
				if path == "" || strings.Join(strings.Fields(path), " ") != path || paths[path] {
					t.Fatalf("empty, noncanonical or duplicate command path %q", path)
				}
				paths[path] = true
			}
			kinds[d.Kind]++
			if d.Source == "" || !shaPattern.MatchString(d.SourceSHA256) || !shaPattern.MatchString(d.HelpSHA256) {
				t.Error("missing source or pinned source/help digest")
			}
			switch d.Kind {
			case "typed":
				if d.Source != "fixed-binary:schema "+d.Path || d.SourceSHA256 != m.Provenance.SchemaSHA256 || !json.Valid(d.InputSchema) {
					t.Error("typed descriptor is not backed by pinned binary schema")
				}
			case "shortcut":
				if !strings.HasPrefix(d.Source, "shortcuts/") || !strings.HasSuffix(d.Source, ".go") || strings.Contains(d.Source, "..") {
					t.Error("shortcut source is not a contained Go source path")
				}
			default:
				t.Errorf("unclassified command kind %q", d.Kind)
			}
			if d.Status != "supported" && d.Status != "restricted" {
				t.Errorf("unclassified status %q", d.Status)
			}
			if d.Status == "restricted" && len(d.Limitations) == 0 {
				t.Error("restricted command lacks an explanation")
			}
			rank := map[string]int{"read": 1, "write": 2, "high-impact-write": 3, "remote-operation": 3, "destructive": 4}
			official := map[string]int{"read": 1, "write": 2, "high-risk-write": 3}[d.OfficialRisk]
			correction := d.OfficialRisk == "write" && d.Risk == "read" && readCorrections[d.Path] != "" && d.RiskSource == "frozen product capability catalog: "+readCorrections[d.Path] && len(d.Limitations) > 0
			if d.Path == "im +messages-resources-download" {
				correction = d.OfficialRisk == "write" && d.Risk == "read" && d.Artifacts && d.Source == "shortcuts/im/im_messages_resources_download.go" && strings.Contains(d.RiskSource, d.Source) && len(d.Limitations) > 0
			}
			if official == 0 || rank[d.Risk] == 0 || rank[d.Risk] < official && !correction {
				t.Errorf("unresolved or downgraded risk: official=%q effective=%q", d.OfficialRisk, d.Risk)
			}
			if len(d.CapabilityIDs) == 0 || len(d.Identities) == 0 && d.Status == "supported" || len(d.Flags) == 0 {
				t.Error("missing capabilities, identities or flags")
			}
			seenIDs := map[string]bool{}
			for _, id := range d.CapabilityIDs {
				if id == "" || seenIDs[id] {
					t.Errorf("empty or duplicate capability %q", id)
				}
				seenIDs[id] = true
			}
			seenIdentities := map[string]bool{}
			for _, identity := range d.Identities {
				if (identity != "user" && identity != "bot") || seenIdentities[identity] {
					t.Errorf("invalid or duplicate identity %q", identity)
				}
				seenIdentities[identity] = true
			}
			seenFlags := map[string]bool{}
			for _, flag := range d.Flags {
				if !includes("bool string int int64 integer uint float float64 duration string_array string_slice int_array", flag.Type) || flag.Type == "" {
					t.Errorf("flag %s has an unclassified type %q", flag.Name, flag.Type)
				}
				if flag.Role != "" && !includes("control output-format json artifact file restricted media-file resource-text document-text multipart", flag.Role) {
					t.Errorf("flag %s has an unclassified execution role %q", flag.Name, flag.Role)
				}
				for _, input := range flag.Input {
					if input != "file" && input != "stdin" {
						t.Errorf("flag %s has an unclassified input channel %q", flag.Name, input)
					}
				}
				for _, name := range append([]string{flag.Name}, flag.Aliases...) {
					if !namePattern.MatchString(name) || seenFlags[name] {
						t.Errorf("invalid or duplicate flag/alias %q", name)
					}
					seenFlags[name] = true
					if d.Status == "supported" && !SupportsFlag(strings.Fields(d.Path), name) {
						t.Errorf("declared flag/alias is not command-supported: %s", name)
					}
				}
			}
			if SupportsFlag(strings.Fields(d.Path), "not-a-real-contract-flag") {
				t.Error("unknown flag is supported")
			}
		})
	}
	if kinds["typed"] != 250 || kinds["shortcut"] != 526 {
		t.Fatalf("actual kind counts: %v", kinds)
	}
	if SupportsFlag([]string{"not-a-real-contract-command"}, "help") {
		t.Error("unknown command inherited global flags")
	}
}

func contractVerifyBinaryProvenance(t *testing.T, m contractManifest) {
	t.Helper()
	var pinned struct {
		Version   string `json:"version"`
		Artifacts map[string]struct {
			SHA256 string `json:"executableSha256"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(contractFile(t, filepath.Join("..", "..", "..", "runtime", "lark-cli-runtime.json")), &pinned); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	for platform, artifact := range pinned.Artifacts {
		expected[platform] = artifact.SHA256
	}
	if pinned.Version != m.Version || len(expected) != 4 || !reflect.DeepEqual(expected, m.Provenance.BinaryArtifacts) {
		t.Fatal("binary provenance does not cover all four pinned runtime artifacts")
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(m.Provenance.SchemaSHA256) {
		t.Fatal("missing canonical fixed-schema digest")
	}
}

func TestDescriptorSkillsAndProvenanceContract(t *testing.T) {
	m := readContractManifest(t)
	root := filepath.Join("..", "..", "..")
	skillsBytes := contractFile(t, filepath.Join(root, "runtime", "lark-skills.json"))
	var skills struct {
		Version string `json:"version"`
		Source  struct {
			SHA256 string `json:"sha256"`
		} `json:"source"`
		Skills []struct {
			Name  string            `json:"name"`
			Files map[string]string `json:"files"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(skillsBytes, &skills); err != nil {
		t.Fatal(err)
	}
	if skills.Version != m.Version || contractSHA256(skillsBytes) != m.Provenance.SkillsSHA256 || skills.Source.SHA256 != m.Provenance.SourceArchiveSHA256 {
		t.Fatal("Skills provenance does not match the pinned runtime Skills manifest")
	}
	if contractSHA256(contractFile(t, "execution-overlay.json")) != m.Provenance.OverlaySHA256 {
		t.Fatal("overlay digest is stale")
	}
	for path, digest := range map[string]string{
		filepath.Join("Core", "internal", "feishucli", "catalog.json"): m.Provenance.ProductCatalogSHA256,
		filepath.Join("scripts", "generate-execution-manifest.py"):     m.Provenance.GeneratorSHA256,
		filepath.Join("scripts", "execution-source-ast.go"):            m.Provenance.ASTExtractorSHA256,
		filepath.Join("Core", "internal", "usercommand", "catalog.go"): m.Provenance.ReviewAdapterSHA256,
	} {
		if contractSHA256(contractFile(t, filepath.Join(root, path))) != digest {
			t.Errorf("stale generation provenance: %s", path)
		}
	}
	contractVerifyBinaryProvenance(t, m)
	expected := map[string]map[string]string{}
	for _, skill := range skills.Skills {
		expected[skill.Name] = skill.Files
	}
	actual := map[string]map[string]string{}
	commands := map[string]bool{}
	statuses := map[string]string{}
	aliases := map[string]string{}
	for _, d := range m.Descriptors {
		commands[d.Path] = true
		statuses[d.Path] = d.Status
		for _, alias := range d.Aliases {
			aliases[alias] = d.Path
		}
	}
	diagnostics := map[string]bool{}
	for _, group := range m.LocalDiagnostics {
		if len(group.HelpSHA256) != 64 || diagnostics[group.Path] {
			t.Fatalf("invalid group diagnostic: %q", group.Path)
		}
		diagnostics[group.Path] = true
	}
	missing := map[string]bool{}
	for _, check := range m.UpstreamMissingChecks {
		if len(check.HelpSHA256) != 64 || len(check.ObservedUsagePaths) == 0 || contains(check.ObservedUsagePaths, check.Path) || commands[check.Path] {
			t.Fatalf("missing command lacks exact binary evidence: %s", check.Path)
		}
		missing[check.Path] = true
	}
	callCounts := map[string]int{"supported": 0, "restricted": 0, "upstream-missing": 0, "local-diagnostic": 0, "non-executable-example/prose": 0}
	for _, skill := range m.Skills {
		if _, duplicate := actual[skill.Name]; duplicate {
			t.Fatalf("duplicate Skill %q", skill.Name)
		}
		actual[skill.Name] = map[string]string{}
		for _, file := range skill.Files {
			if _, duplicate := actual[skill.Name][file.File]; duplicate {
				t.Fatalf("duplicate Skill file %s/%s", skill.Name, file.File)
			}
			actual[skill.Name][file.File] = file.SHA256
			want := "no-cli-invocation"
			if len(file.Calls) > 0 {
				want = "command-reference"
			}
			if file.Classification != want {
				t.Errorf("unclassified Skill file %s/%s", skill.Name, file.File)
			}
			seen := map[string]bool{}
			for _, command := range file.Commands {
				if !commands[command] || seen[command] {
					t.Errorf("invalid/duplicate Skill command %q in %s/%s", command, skill.Name, file.File)
				}
				seen[command] = true
			}
			calledCommands := map[string]bool{}
			for _, call := range file.Calls {
				if _, known := callCounts[call.Classification]; !known {
					t.Errorf("unclassified CLI invocation %q in %s/%s", call.Invocation, skill.Name, file.File)
				}
				callCounts[call.Classification]++
				if call.Invocation == "" || call.Line <= 0 || !strings.Contains(call.RawLine, call.RawInvocation) || !strings.HasPrefix(call.RawInvocation, "lark-cli") || (call.ExampleContext != "negative-or-warning" && call.ExampleContext != "example-or-reference") {
					t.Errorf("missing CLI invocation context in %s/%s", skill.Name, file.File)
				}
				if call.Canonical != "" {
					calledCommands[call.Canonical] = true
					if !commands[call.Canonical] || statuses[call.Canonical] != call.Classification || (call.Alias && aliases[call.Invocation] != call.Canonical) || (!call.Alias && call.Invocation != call.Canonical) {
						t.Errorf("CLI call does not match canonical classification: %q", call.Invocation)
					}
				} else if call.Classification == "supported" || call.Reason == "" {
					t.Errorf("unmapped CLI call lacks a restriction/missing reason: %q", call.Invocation)
				}
				switch call.Classification {
				case "upstream-missing":
					if !missing[call.MissingPath] || call.Invocation != call.MissingPath || strings.Contains(call.Reason, "may be") {
						t.Errorf("unproven upstream absence: %s", call.Invocation)
					}
				case "local-diagnostic":
					if !diagnostics[call.DiagnosticPath] && !commands[call.DiagnosticPath] && aliases[call.DiagnosticPath] == "" {
						t.Errorf("unverified local diagnostic: %s", call.Invocation)
					}
				case "non-executable-example/prose":
					if !includes("placeholder-or-ellipsis slash-alternatives natural-language-command-position literal-diagnostic-or-narrative bare-group-reference", call.Evidence) {
						t.Errorf("non-executable classification lacks lexical evidence: %s", call.Invocation)
					}
				}
			}
			if !reflect.DeepEqual(seen, calledCommands) {
				t.Errorf("Skill file command index differs from classified calls: %s/%s", skill.Name, file.File)
			}
		}
	}
	if len(expected) == 0 || !reflect.DeepEqual(expected, actual) {
		t.Fatal("execution manifest must classify every pinned Skill file exactly once, with the same SHA256")
	}
	if !reflect.DeepEqual(callCounts, m.SkillCallCounts) {
		t.Fatalf("Skill call summary does not match classified invocations: actual=%v declared=%v", callCounts, m.SkillCallCounts)
	}
	fixtures := map[string]string{
		"approval --help":                   "local-diagnostic",
		"apps --help":                       "local-diagnostic",
		"mail user_mailbox.messages --help": "local-diagnostic",
		"apps +<cmd> --help":                "non-executable-example/prose",
		"calendar <resource> <method>":      "non-executable-example/prose",
		"自身鉴权用，如":                           "non-executable-example/prose",
		"设置协作者，并引导其在妙搭后台的权限设置中操作。": "non-executable-example/prose",
		"base +app-list":                            "upstream-missing",
		"drive files upload_prepare":                "upstream-missing",
		"drive files upload_finish":                 "upstream-missing",
		"mail user_mailbox.messages modify_message": "upstream-missing",
	}
	for _, skill := range m.Skills {
		for _, file := range skill.Files {
			for _, call := range file.Calls {
				if expected, exists := fixtures[call.Invocation]; exists {
					if call.Classification != expected {
						t.Errorf("real Skills example %q: got %s want %s", call.Invocation, call.Classification, expected)
					}
					delete(fixtures, call.Invocation)
				}
			}
		}
	}
	if len(fixtures) != 0 {
		t.Fatalf("actual Skills classification fixtures disappeared: %v", fixtures)
	}
}

// These are in-process fixtures. They never invoke the official CLI or resolve
// real identities, credentials, network resources or approval services.
func TestDescriptorEffectsOfflineContract(t *testing.T) {
	cases := []struct {
		name string
		args []string
		risk string
	}{
		{"read", []string{"im", "+messages-search", "--query", "fixture"}, "read"},
		{"send", []string{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "fixture"}, "write"},
		{"delete", []string{"im", "messages", "delete", "--message-id", "om_fixture"}, "destructive"},
		{"overwrite", []string{"docs", "+update", "--doc", "fixture", "--command", "overwrite", "--content", "fixture", "--doc-format", "markdown"}, "destructive"},
		{"export creates job", []string{"drive", "+export", "--token", "fixture", "--doc-type", "docx", "--file-extension", "pdf"}, "write"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, identity := range []string{"user", "bot"} {
				args := append(append([]string(nil), tc.args...), "--as", identity)
				command, err := Freeze(args, nil)
				if err != nil {
					t.Fatal(err)
				}
				command.Identity = Identity{AppID: "cli_fixture", ApplicationName: "Fixture app", Brand: "feishu", Profile: "default"}
				if identity == "user" {
					command.Identity.UserID = "ou_fixture"
					command.Identity.UserName = "Fixture user"
				}
				review, err := Evaluate(command)
				if err != nil {
					t.Fatal(err)
				}
				if review.Risk != tc.risk || review.NeedsApproval != (identity == "user" && tc.risk != "read") {
					t.Fatalf("incorrect review: %+v", review)
				}
				if len(review.Effects) == 0 {
					t.Fatal("review omitted effects")
				}
				ids := map[string]bool{}
				for _, id := range review.CapabilityIDs {
					ids[id] = true
				}
				for _, effect := range review.Effects {
					if !ids[effect.CapabilityID] || !includes("read write high-impact-write remote-operation destructive", effect.Risk) {
						t.Errorf("unclassified effect: %+v", effect)
					}
				}
			}
		})
	}
}

func TestDescriptorCompoundEffectsOfflineContract(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fixture.png")
	approved := []byte("offline fixture bytes")
	if err := os.WriteFile(path, approved, 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{path, "img_existing_fixture"} {
		command, err := Freeze([]string{"im", "+messages-send", "--chat-id", "oc_fixture", "--image", source, "--as", "user"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		command.Identity = Identity{AppID: "cli_fixture", UserID: "ou_fixture", UserName: "Fixture user", Brand: "feishu", Profile: "default"}
		review, err := Evaluate(command)
		if err != nil {
			t.Fatal(err)
		}
		upload := false
		for _, effect := range review.Effects {
			if effect.CapabilityID == "im.images.create" && effect.Risk == "write" {
				upload = true
			}
		}
		if upload != (source == path) {
			t.Fatalf("image upload dependency incorrectly classified for %q: %+v", source, review.Effects)
		}
		if source != path {
			continue
		}
		if len(command.Files) != 1 {
			t.Fatal("local image was not frozen exactly once")
		}
		if err := os.WriteFile(path, []byte("changed after approval"), 0600); err != nil {
			t.Fatal(err)
		}
		execution, err := Materialize(command)
		if err != nil {
			t.Fatal(err)
		}
		defer execution.Close()
		if !bytes.Equal(contractFile(t, filepath.Join(execution.Dir, command.Files[0].Name)), approved) {
			t.Fatal("materialization reread an image after its upload effect was reviewed")
		}
	}
	if _, err := Freeze([]string{"im", "+messages-send", "--chat-id", "oc_fixture", "--image", "https://example.invalid/image.png", "--as", "user"}, nil); err == nil {
		t.Fatal("implicit remote image fetch accepted")
	}
}

// --help omits hidden flags and compatibility aliases. Check every public flag
// against the descriptor and pin the complete help bytes; do not claim that an
// absent hidden flag is invalid, or execute a business command to probe it.
var contractHelpFlag = regexp.MustCompile(`(?m)^\s+(?:-[A-Za-z0-9],\s+)?--([a-z0-9][a-z0-9-]*)(?:[ \t]+(stringArray|strings|intSlice|string|int64|int|float64|float|uint|duration))?[ \t]{2,}.+$`)

func TestFixedBinaryOfflineContract(t *testing.T) {
	binary := os.Getenv("KSF_USERCOMMAND_PINNED_CLI")
	if binary == "" {
		t.Skip("set KSF_USERCOMMAND_PINNED_CLI to an explicitly selected, pinned official binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("fixed CLI is not an existing regular file: %s", binary)
	}
	m := readContractManifest(t)
	if len(m.Descriptors) != 776 {
		t.Fatalf("fixed binary contract requires all 776 descriptors; got %d", len(m.Descriptors))
	}
	binarySHA256 := contractSHA256(contractFile(t, binary))
	matched := false
	for _, digest := range m.Provenance.BinaryArtifacts {
		matched = matched || digest == binarySHA256
	}
	if !matched {
		t.Fatal("fixed CLI SHA256 is absent from execution provenance")
	}
	// Verify both pinned manifests before starting even a --version subprocess.
	contractVerifyBinaryProvenance(t, m)
	home := t.TempDir()
	env := []string{
		"HOME=" + home, "USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "xdg-config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, "xdg-cache"),
		"LARKSUITE_CLI_CONFIG_DIR=" + filepath.Join(home, "config"),
		"LARKSUITE_CLI_REMOTE_META=off", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1",
		"LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1", "NO_COLOR=1",
	}
	run := func(t *testing.T, args ...string) []byte {
		t.Helper()
		allowed := len(args) == 1 && args[0] == "--version" || len(args) == 2 && args[0] == "schema" && args[1] == "--json" || len(args) > 0 && args[len(args)-1] == "--help"
		if !allowed {
			t.Fatal("fixed binary contract attempted a non-diagnostic invocation")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Env, command.Dir = env, home
		out, err := command.Output()
		if err != nil {
			t.Fatalf("offline diagnostic %q failed: %v", args, err)
		}
		return out
	}
	if !strings.Contains(string(run(t, "--version")), Version) {
		t.Fatal("fixed binary version mismatch")
	}
	schemaBytes := run(t, "schema", "--json")
	var canonicalSchemas any
	decoder := json.NewDecoder(bytes.NewReader(schemaBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&canonicalSchemas); err != nil {
		t.Fatal(err)
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonicalSchemas); err != nil {
		t.Fatal(err)
	}
	if contractSHA256(bytes.TrimSuffix(canonical.Bytes(), []byte("\n"))) != m.Provenance.SchemaSHA256 {
		t.Fatal("fixed binary schemas differ from canonical schema provenance")
	}
	var schemas []struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(schemaBytes, &schemas); err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 250 {
		t.Fatalf("fixed binary schema inventory: got %d, want 250", len(schemas))
	}
	schemaPaths := map[string]json.RawMessage{}
	for _, schema := range schemas {
		if _, duplicate := schemaPaths[schema.Name]; duplicate {
			t.Fatalf("duplicate binary schema %q", schema.Name)
		}
		schemaPaths[schema.Name] = schema.InputSchema
	}
	for _, d := range m.Descriptors {
		if d.Kind == "typed" {
			var expected, actual any
			if json.Unmarshal(schemaPaths[d.Path], &expected) != nil || json.Unmarshal(d.InputSchema, &actual) != nil || !reflect.DeepEqual(expected, actual) {
				t.Errorf("typed input schema differs from fixed binary: %s", d.Path)
			}
		}
	}
	// The parent subtest keeps the isolated home alive until all parallel help
	// checks finish. Release verification sets -parallel=8 explicitly.
	t.Run("descriptors", func(t *testing.T) {
		for _, d := range m.Descriptors {
			t.Run(d.Path, func(t *testing.T) {
				t.Parallel()
				out := run(t, append(strings.Fields(d.Path), "--help")...)
				if contractSHA256(out) != d.HelpSHA256 {
					t.Error("fixed help bytes differ from manifest provenance")
				}
				flags := map[string]FlagDescriptor{}
				for _, flag := range d.Flags {
					flags[flag.Name] = flag
					for _, alias := range flag.Aliases {
						flags[alias] = flag
					}
				}
				matches := contractHelpFlag.FindAllStringSubmatch(string(out), -1)
				if len(matches) == 0 {
					t.Fatal("command help has no parseable public flags")
				}
				for _, match := range matches {
					flag, exists := flags[match[1]]
					if !exists {
						t.Errorf("public flag --%s is absent from descriptor", match[1])
						continue
					}
					kind := match[2]
					switch kind {
					case "":
						kind = "bool"
					case "strings":
						kind = "string_slice"
					case "stringArray":
						kind = "string_array"
					case "intSlice":
						kind = "int_array"
					case "float":
						// pflag renders Float64 flags as "float" in help.
						kind = "float64"
					}
					if flag.Type != kind {
						t.Errorf("public flag --%s type: descriptor=%s binary=%s", match[1], flag.Type, kind)
					}
				}
			})
		}
	})
	t.Run("local_diagnostics", func(t *testing.T) {
		if len(m.LocalDiagnostics) == 0 {
			t.Fatal("group help inventory is empty")
		}
		for _, group := range m.LocalDiagnostics {
			t.Run(group.Path, func(t *testing.T) {
				t.Parallel()
				out := run(t, append(strings.Fields(group.Path), "--help")...)
				if contractSHA256(out) != group.HelpSHA256 {
					t.Fatal("fixed group help differs from manifest")
				}
				path := group.Path
				if path == "slide" {
					path = "slides"
				}
				if path != "" && !regexp.MustCompile(`(?m)^  lark-cli `+regexp.QuoteMeta(path)+`(?: \[|$)`).Match(out) {
					t.Fatal("help silently fell back to a different group")
				}
			})
		}
	})
	t.Run("upstream_missing", func(t *testing.T) {
		for _, check := range m.UpstreamMissingChecks {
			t.Run(check.Path, func(t *testing.T) {
				t.Parallel()
				out := run(t, append(strings.Fields(check.Path), "--help")...)
				if contractSHA256(out) != check.HelpSHA256 || regexp.MustCompile(`(?m)^  lark-cli `+regexp.QuoteMeta(check.Path)+`(?: \[|$)`).Match(out) {
					t.Fatal("concrete absence is no longer supported by fixed Usage evidence")
				}
			})
		}
	})
}
