package feishu

import "testing"

func TestFrozenCapabilityManifest(t *testing.T) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Capabilities) != 219 {
		t.Fatalf("got %d capabilities", len(manifest.Capabilities))
	}
	for _, id := range []string{"im.message.reply", "docs.draft.preflight", "sheets.range.move", "base.field.update", "apps.session.chat"} {
		if _, ok := CapabilityByID(id); !ok {
			t.Fatalf("missing %s", id)
		}
	}
}
