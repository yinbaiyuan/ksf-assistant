package taskruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCurrentKSFVerifierWithIsolatedSyntheticWorkspace(t *testing.T) {
	sourceRoot := os.Getenv("KSF_TASK_TEST_VERIFIER_SOURCE")
	if sourceRoot == "" {
		t.Skip("set KSF_TASK_TEST_VERIFIER_SOURCE to copy only the current KSF verifier and scripts into a synthetic temp workspace")
	}
	value := newFixture(t)
	t.Setenv("KSF_ROOT", filepath.Join(t.TempDir(), "must-not-be-used"))
	name := "ksf-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, relative := range []string{".agents/bin/" + name, ".agents/runtime-requirements.yaml", ".agents/runtime/ksf/scripts/catalog.js", ".agents/runtime/ksf/scripts/panel.js", ".agents/runtime/ksf/scripts/route.js"} {
		data, err := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if relative == ".agents/bin/"+name {
			mode = 0o700
		}
		writeTestFile(t, filepath.Join(value.root, filepath.FromSlash(relative)), data, mode)
	}
	writeTestFile(t, filepath.Join(value.root, "30知识/02工作能力体系/工作类别/Development.md"), []byte("---\ntype: work-category\nstatus: active\nvalidation_status: 草案\npractice: Development\ncategory_id: development\nksf_contract_version: 2\n---\n# Development\n"), 0o600)
	writeTestFile(t, filepath.Join(value.root, "30知识/02工作能力体系/工作岗位/Engineer/岗位卡.md"), []byte("---\ntype: work-job\nstatus: active\nvalidation_status: 草案\npractice: Engineer\njob_id: engineer\ncategory_id: development\nksf_contract_version: 2\n---\n# Engineer\n\n## 责任—基本功映射\n\n| R1 | `testing` | [[30知识/02工作能力体系/基本功/Testing/能力卡|Testing]] | 草案 |\n"), 0o600)
	writeTestFile(t, filepath.Join(value.root, "30知识/02工作能力体系/基本功/Testing/能力卡.md"), []byte("---\ntype: basic-skill-card\nstatus: active\nvalidation_status: 草案\nability_id: testing\nability_name: Testing\nksf_contract_version: 2\nskill_relations: []\n---\n# Testing\n"), 0o600)
	for _, project := range []string{"Alpha", "Beta"} {
		writeTestFile(t, filepath.Join(value.root, "10项目", project, "项目记忆卡.md"), []byte("# Synthetic project root\n"), 0o600)
	}
	args := []string{"route", "--verify-route", "--category", "Development", "--task-intent", "Synthetic independent runtime verification", "--main-job", "Engineer", "--ability", "Engineer·Testing", "--context-file", "10项目/Alpha/项目记忆卡.md", "--context-file", "10项目/Beta/项目记忆卡.md"}
	receipt, err := value.store.runVerifier(context.Background(), args)
	if err != nil {
		t.Fatal("real current KSF verifier did not accept synthetic workspace", err)
	}
	parsed, err := ParseReceipt(receipt)
	if err != nil || parsed.Projection.Category.ContextPolicy != "route-only" {
		t.Fatal("real receipt schema or governance mismatch", err)
	}
	request := reportRequest("synthetic-private-thread", "create", 0, "project", receipt)
	request.State.ProjectCard = "10项目/Beta/项目记忆卡.md"
	first, _, _, err := value.store.Report(context.Background(), request)
	if err != nil || first.ProjectCard != request.State.ProjectCard || first.RouteFreshness != "current" {
		t.Fatal("real receipt report failed", err)
	}
	var forged map[string]any
	_ = json.Unmarshal(receipt, &forged)
	forged["catalog_sha256"] = "sha256:" + digest([]byte("self-consistent fabrication"))
	forged["receipt_sha256"] = digest(canonicalJSON(forged))
	request.EventID = "forged"
	revision := uint64(1)
	request.ExpectedRevision = &revision
	request.State.Receipt = canonicalJSON(forged)
	_, _, _, err = value.store.Report(context.Background(), request)
	requireCode(t, err, "route_invalid")
	governance, err := os.ReadFile(filepath.Join(value.root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(value.root, "AGENTS.md"), append(governance, []byte("synthetic governance changed\n")...), 0o600)
	view, _, err := value.store.Get(context.Background(), first.TaskID)
	if err != nil || view.RouteFreshness != "stale" {
		t.Fatal("current verifier did not invalidate changed root governance", err)
	}
}
