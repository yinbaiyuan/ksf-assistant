package feishu

import "testing"

func TestFrozenCapabilityManifest(t *testing.T) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Capabilities) != CapabilityRegistryV2Count {
		t.Fatalf("v2 manifest count = %d, want %d", len(manifest.Capabilities), CapabilityRegistryV2Count)
	}
	for _, id := range []string{
		"im.message.reply", "docs.draft.preflight", "sheets.range.move", "base.field.update", "apps.session.chat",
		"mail.user_mailbox.messages.list", "slides.xml_presentations.get", "attendance.user_tasks.query", "okr.cycles.list",
		"mail.user_mailbox.rules.list", "mail.user_mailbox.rules.create", "mail.user_mailbox.rules.update", "mail.user_mailbox.rules.reorder", "mail.user_mailbox.rules.delete",
		"approval.approvals.get", "approval.approvals.search", "approval.instances.create", "approval.instances.get", "approval.instances.initiated",
		"approval.tasks.query", "approval.tasks.approve", "approval.tasks.reject", "approval.tasks.transfer", "approval.instances.cancel", "approval.tasks.rollback",
		"approval.events.instance.subscribe", "approval.events.instance.unsubscribe", "approval.events.task.subscribe", "approval.events.task.unsubscribe",
	} {
		if _, ok := CapabilityByID(id); !ok {
			t.Fatalf("missing %s", id)
		}
	}
}

func TestCapabilityManifestPublishesGovernanceMetadata(t *testing.T) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range manifest.Capabilities {
		if definition.Backend == "" || definition.Effect == "" || definition.Reversibility == "" {
			t.Fatalf("missing governance metadata: %#v", definition)
		}
		if definition.Transport == "raw" && len(definition.Command) > 0 && definition.Command[0] == "api" && definition.APIPath == "" {
			t.Fatalf("arbitrary raw API escaped registry: %s", definition.ID)
		}
		if definition.Domain == "application" {
			t.Fatalf("unsupported governance domain escaped registry: %s", definition.ID)
		}
	}
}

func TestApprovalCapabilitiesArePublishedAndGoverned(t *testing.T) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatal(err)
	}
	approvalCount := 0
	for _, definition := range manifest.Capabilities {
		if definition.Domain == "approval" && CapabilityPublished(definition) {
			approvalCount++
		}
	}
	if approvalCount != 18 {
		t.Fatalf("published approval capability count = %d, want 18", approvalCount)
	}
	cases := map[string]struct {
		risk       string
		permission CapabilityPermission
		confirmCLI bool
	}{
		"approval.approvals.get":               {risk: "read", permission: CapabilityAllowed},
		"approval.tasks.query":                 {risk: "read", permission: CapabilityAllowed},
		"approval.instances.create":            {risk: "high-impact-write", permission: CapabilityConfirmEach, confirmCLI: true},
		"approval.tasks.approve":               {risk: "high-impact-write", permission: CapabilityConfirmEach, confirmCLI: true},
		"approval.tasks.reject":                {risk: "high-impact-write", permission: CapabilityConfirmEach, confirmCLI: true},
		"approval.tasks.transfer":              {risk: "high-impact-write", permission: CapabilityConfirmEach, confirmCLI: true},
		"approval.events.instance.subscribe":   {risk: "high-impact-write", permission: CapabilityConfirmEach},
		"approval.events.instance.unsubscribe": {risk: "high-impact-write", permission: CapabilityConfirmEach},
		"approval.instances.cancel":            {risk: "destructive", permission: CapabilityDisabled, confirmCLI: true},
		"approval.tasks.rollback":              {risk: "destructive", permission: CapabilityDisabled, confirmCLI: true},
	}
	for id, expected := range cases {
		definition, ok := CapabilityByID(id)
		if !ok || !CapabilityPublished(definition) {
			t.Fatalf("approval capability %s is unavailable", id)
		}
		if definition.Risk != expected.risk || DefaultCapabilityPolicy().Decision(definition) != expected.permission || definition.CLIConfirm != expected.confirmCLI {
			t.Fatalf("%s governance = risk:%s policy:%s cliConfirm:%v", id, definition.Risk, DefaultCapabilityPolicy().Decision(definition), definition.CLIConfirm)
		}
		if definition.Risk == "destructive" && (definition.Preflight == nil || definition.Reread == nil) {
			t.Fatalf("destructive approval capability %s lacks preflight/reread", id)
		}
	}
}

func TestExcludedManagementCapabilitiesRemainRegisteredButUnpublished(t *testing.T) {
	definition, ok := CapabilityByID("apps.app.create")
	if !ok {
		t.Fatal("frozen capability disappeared from registry")
	}
	if CapabilityPublished(definition) {
		t.Fatal("application management capability was published")
	}
	if err := ValidateCapabilityInput(definition.ID, map[string]any{"name": "test"}); err == nil || err.Error() != "capability_not_published" {
		t.Fatalf("unpublished capability escaped execution gate: %v", err)
	}
	session, ok := CapabilityByID("apps.session.chat")
	if !ok || !CapabilityPublished(session) {
		t.Fatal("reviewed Apps session capability was excluded with application management")
	}
}

func TestSDKMessageSendUsesGovernedGoBackend(t *testing.T) {
	definition, ok := CapabilityByID("im.sdk.message.send")
	if !ok || !CapabilityPublished(definition) {
		t.Fatal("Go SDK message capability is unavailable")
	}
	if definition.Backend != "go-sdk" || definition.Risk != "high-impact-write" || definition.Effect != "send" || definition.Queue != "actionbox" {
		t.Fatalf("unexpected SDK message governance: %#v", definition)
	}
	if DefaultCapabilityPolicy().Decision(definition) != CapabilityConfirmEach {
		t.Fatal("immediate SDK send does not require per-operation confirmation")
	}
}

func TestDocumentServiceEntryPointsAreFixedAndGoverned(t *testing.T) {
	cases := map[string]string{
		"docs.service.document.create":    "write",
		"docs.service.document.append":    "write",
		"docs.service.document.overwrite": "destructive",
	}
	for id, risk := range cases {
		definition, ok := CapabilityByID(id)
		if !ok || definition.Queue != "docbox" || definition.Risk != risk {
			t.Fatalf("%s definition = %#v found=%v", id, definition, ok)
		}
	}
}

func TestOverwriteMoveAndHistoryRollbackAreDestructiveByDefault(t *testing.T) {
	for _, id := range []string{
		"sheets.shortcut.range.move",
		"slides.shortcut.replace.slide",
		"sheets.shortcut.history.revert",
	} {
		definition, ok := CapabilityByID(id)
		if !ok {
			t.Fatalf("missing destructive capability %s", id)
		}
		if definition.Risk != "destructive" || DefaultCapabilityPolicy().Decision(definition) != CapabilityDisabled {
			t.Fatalf("%s governance = risk:%s effect:%s", id, definition.Risk, definition.Effect)
		}
	}
}

func TestAllPublishedDestructiveCapabilitiesCanBeIndividuallyGoverned(t *testing.T) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, definition := range manifest.Capabilities {
		if !CapabilityPublished(definition) || definition.Risk != "destructive" {
			continue
		}
		count++
		if definition.GuardProfile != CapabilityGuardStrong && definition.GuardProfile != CapabilityGuardBounded {
			t.Fatalf("%s has no guard profile", definition.ID)
		}
		if definition.Preflight == nil || definition.Preflight.ID == "" || definition.Postcondition == nil || definition.Postcondition.Kind == "" || definition.RetryClass != "never" || len(definition.ConflictKey) == 0 {
			t.Fatalf("%s has incomplete governance: %#v", definition.ID, definition)
		}
		if definition.GuardProfile == CapabilityGuardStrong && definition.Reread == nil && definition.Poll == nil {
			t.Fatalf("%s claims a strong guard without authoritative verification", definition.ID)
		}
		if (definition.Effect == "move" || definition.Effect == "overwrite" || definition.Effect == "revert" || definition.Effect == "cancel") && definition.Reversibility == "hard-delete" {
			t.Fatalf("%s retains incorrect hard-delete metadata", definition.ID)
		}
	}
	if count != 101 {
		t.Fatalf("published destructive count = %d, want 101", count)
	}
}
