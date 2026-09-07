package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestLegacyDocumentQueueIsNotPublishedOrConfigured(t *testing.T) {
	for _, id := range []string{"docs.service.document.create", "docs.service.document.append", "docs.service.document.overwrite", "docs.whiteboard.insert"} {
		if definition, ok := CapabilityByID(id); ok && CapabilityPublished(definition) {
			t.Errorf("retired document capability remains callable: %s", id)
		}
	}
	data, err := json.Marshal(DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"docbox"`)) {
		t.Fatal("retired document queue remains in configuration")
	}
}

func TestSavingSettingsRetiresProductQueueSwitches(t *testing.T) {
	store := NewSettingsStore(t.TempDir())
	original := []byte(`{"version":1,"docbox":{"enabled":true,"dryRun":false},"actionbox":{"enabled":true,"dryRun":false},"future":{"keep":true}}`)
	if err := os.WriteFile(store.path, original, 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Actionbox.Enabled || settings.Actionbox.DryRun {
		t.Fatal("active queue changed")
	}
	if err = store.Save(settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		t.Fatal("invalid settings")
	}
	if _, ok := result["docbox"]; ok {
		t.Fatal("legacy settings survived save")
	}
	if result["future"] == nil || result["actionbox"] != nil {
		t.Fatal("unrelated settings lost")
	}
}

func TestPolicyUpdatePreservesRetiredRestrictions(t *testing.T) {
	service := NewCapabilityService(t.TempDir(), nil, nil)
	policy, _ := service.ReadPolicy()
	policy.CapabilityOverrides["docs.service.document.create"] = CapabilityDisabled
	saved, err := service.policy.Save(policy, policy.Revision)
	if err != nil {
		t.Fatal(err)
	}
	saved.CapabilityOverrides["im.sdk.message.send"] = CapabilityAllowed
	updated, err := service.UpdatePolicy(saved, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := CapabilityByID("docs.shortcut.create")
	if updated.Decision(definition) != CapabilityDisabled {
		t.Fatal("old restriction lost")
	}
	updated.CapabilityOverrides["docs.service.document.create"] = CapabilityAllowed
	if _, err := service.UpdatePolicy(updated, updated.Revision); err == nil {
		t.Fatal("retired permission newly granted")
	}
}

func TestRetiredDocumentRequestsNeverReachExecutor(t *testing.T) {
	executor := &recordingCapabilityServiceExecutor{}
	box := NewActionbox(t.TempDir())
	request := ActionRequest{ID: "ACT-retired-document", Type: "feishu_capability", Domain: "capability", Action: "execute", Identity: "user", CapabilityID: "docs.service.document.create", ExplicitAuthorization: true}
	if err := box.Submit(request); err == nil {
		t.Fatal("retired request accepted")
	}
	result := box.executeRequest(context.Background(), executor, request)
	if result.Status != "failed" || result.Error != "capability_not_published" {
		t.Fatalf("historical request not stopped: %+v", result)
	}
	if executor.action.calls != 0 {
		t.Fatal("retired request reached remote executor")
	}
}

func TestManagedDocumentPolicyEffectsRemainConfigurable(t *testing.T) {
	service := NewCapabilityService(t.TempDir(), nil, nil)
	policy, _ := service.ReadPolicy()
	policy.CapabilityOverrides["docs.shortcut.overwrite"] = CapabilityConfirmEach
	policy.CapabilityOverrides["drive.file.version.create"] = CapabilityAllowed
	saved, err := service.UpdatePolicy(policy, policy.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Decision(CapabilityDefinition{ID: "docs.shortcut.overwrite", Risk: "destructive"}) != CapabilityConfirmEach {
		t.Fatal("canonical overwrite policy unavailable")
	}
	saved.CapabilityOverrides["docs.shortcut.overwrite"] = CapabilityAllowed
	if _, err := service.UpdatePolicy(saved, saved.Revision); err == nil {
		t.Fatal("overwrite no longer requires confirmation")
	}
}
