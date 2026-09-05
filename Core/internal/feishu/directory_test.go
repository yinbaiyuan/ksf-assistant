package feishu

import "testing"

func TestDirectoryResolutionRequiresUniqueExactName(t *testing.T) {
	candidates := []DirectoryCandidate{
		{Name: "示例用户", Aliases: []string{"示例用户", "Example"}, Target: MessageTarget{Type: "open_id", ID: "ou_one"}},
		{Name: "示例用户", Aliases: []string{"示例用户"}, Target: MessageTarget{Type: "open_id", ID: "ou_two"}},
		{Name: "示例用户甲", Aliases: []string{"示例用户甲"}, Target: MessageTarget{Type: "open_id", ID: "ou_three"}},
	}
	result := resolveDirectoryCandidates("示例用户", candidates, nil)
	if result.Status != "ambiguous" || len(result.Candidates) != 2 {
		t.Fatalf("duplicate exact names were not treated as ambiguous: %#v", result)
	}
	result = resolveDirectoryCandidates("Example", candidates, nil)
	if result.Status != "resolved" || result.Target.ID != "ou_one" {
		t.Fatalf("unique alias was not resolved: %#v", result)
	}
	result = resolveDirectoryCandidates("示例用户乙", candidates, nil)
	if result.Status != "not_found" {
		t.Fatalf("partial name auto-resolved: %#v", result)
	}
}

func TestDirectoryBindingMustStillMatchVisibleExactCandidate(t *testing.T) {
	candidates := []DirectoryCandidate{{Name: "研发群", Target: MessageTarget{Type: "chat_id", ID: "oc_live"}}}
	stale := map[string]any{"id": "oc_stale", "type": "chat_id"}
	result := resolveDirectoryCandidates("研发群", candidates, stale)
	if result.Status != "binding_stale" || result.Target.ID != "" {
		t.Fatalf("stale binding was accepted: %#v", result)
	}
}
