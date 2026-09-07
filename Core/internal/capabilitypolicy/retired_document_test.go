package capabilitypolicy

import "testing"

func TestRetiredDocumentPolicyRestrictionsRemainEffective(t *testing.T) {
	for _, pair := range [][2]string{{"docs.shortcut.create", "docs.service.document.create"}, {"docs.shortcut.update", "docs.service.document.append"}, {"docs.shortcut.overwrite", "docs.service.document.overwrite"}, {"drive.file.version.create", "docbox.version"}} {
		for _, old := range []Permission{Disabled, ConfirmEach, Allowed} {
			for _, current := range []Permission{Disabled, ConfirmEach, Allowed} {
				policy := Default()
				policy.CapabilityOverrides[pair[0]] = current
				policy.CapabilityOverrides[pair[1]] = old
				if got := policy.Decision(pair[0], "write"); got != stricterPermission(old, current) {
					t.Fatalf("%s old=%s current=%s got=%s", pair[0], old, current, got)
				}
			}
		}
	}
	policy := Default()
	policy.RiskDefaults["write"] = ConfirmEach
	policy.CapabilityOverrides["docs.shortcut.create"] = Allowed
	if policy.Decision("docs.shortcut.create", "write") != ConfirmEach {
		t.Fatal("removing legacy effect weakened risk default")
	}
	policy.CapabilityOverrides["docs.shortcut.update"] = Allowed
	if policy.Decision("docs.shortcut.overwrite", "destructive") != Disabled {
		t.Fatal("ordinary update authorization enabled overwrite")
	}
}
