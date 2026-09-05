package main

import (
	"testing"

	"codexusagebar/core/internal/feishu"
)

func TestHelpCommandParserAcceptsLongestCommandWithOneSeparatorSpace(t *testing.T) {
	match := helpCommandLine.FindStringSubmatch("  +transcript Fetch the unified note transcript")
	if len(match) != 3 || match[1] != "+transcript" {
		t.Fatalf("command parser did not accept aligned longest command: %#v", match)
	}
}

func TestShortcutBusinessJSONFlagIsNotMistakenForGlobalJSONSwitch(t *testing.T) {
	match := helpFlagLine.FindStringSubmatch("      --json string         batch create JSON object")
	if len(match) != 4 || match[1] != "json" || match[2] != "string" || excludedShortcutFlag(match[1], match[2]) {
		t.Fatalf("business JSON flag was not parsed: %#v", match)
	}
	field := helpCapabilityField(match[1], match[2], match[3])
	if field.Type != "json" || !field.Private {
		t.Fatalf("business JSON flag is not private JSON: %#v", field)
	}
}

func TestImmediateMailCapabilityIsDistinctAndConfirmationGoverned(t *testing.T) {
	definitions := []feishu.CapabilityDefinition{{
		ID: "mail.shortcut.send", Domain: "mail", Risk: "write", Effect: "create", Reversibility: "reversible",
		Command: []string{"mail", "+send"},
	}}
	seen := map[string]bool{"mail.shortcut.send": true}
	addImmediateMailCapabilities(&definitions, seen, map[string]bool{})
	if len(definitions) != 2 {
		t.Fatalf("definitions=%#v", definitions)
	}
	immediate := definitions[1]
	if immediate.ID != "mail.shortcut.send.immediate" || immediate.Risk != "high-impact-write" || immediate.Effect != "send" || len(immediate.FixedArgs) != 1 || immediate.FixedArgs[0] != "--confirm-send" {
		t.Fatalf("unexpected immediate mail capability: %#v", immediate)
	}
}

func TestGeneratedDestructiveCapabilityGetsPreflightAndReread(t *testing.T) {
	definitions := []feishu.CapabilityDefinition{
		{ID: "mail.box.drafts.get", Risk: "read", Flags: map[string]feishu.CapabilityField{"draft-id": {Type: "string", Required: true}}},
		{ID: "mail.box.drafts.list", Risk: "read", Flags: map[string]feishu.CapabilityField{"mailbox-id": {Type: "string", Required: true}}},
		{ID: "mail.box.drafts.delete", Risk: "destructive", Transport: "typed", Flags: map[string]feishu.CapabilityField{"draft-id": {Type: "string"}, "mailbox-id": {Type: "string"}}},
	}
	attachGeneratedDestructiveGuards(definitions, nil)
	if definitions[2].Preflight == nil || definitions[2].Preflight.ID != "mail.box.drafts.get" {
		t.Fatalf("missing destructive preflight: %#v", definitions[2])
	}
	if definitions[2].Reread == nil || definitions[2].Reread.ID != "mail.box.drafts.list" {
		t.Fatalf("missing destructive reread: %#v", definitions[2])
	}
}

func TestGeneratedDomainBoundaryExcludesLiveMeetingAndContactWrites(t *testing.T) {
	if allowGeneratedCommand("vc", "+meeting-screenshot") || allowGeneratedCommand("vc", "+meeting-end") {
		t.Fatal("live meeting control entered the capability registry")
	}
	if !allowGeneratedCommand("vc", "+search") || !allowGeneratedCommand("contact", "contact.users.get") {
		t.Fatal("supported read capability was excluded")
	}
	if allowGeneratedCommand("contact", "contact.users.create") || allowGeneratedCommand("base", "+workflow-create") {
		t.Fatal("excluded administration capability was admitted")
	}
	if !allowGeneratedCommand("mail", "mail.user_mailbox.rules.create") {
		t.Fatal("governed mailbox rule capability was excluded")
	}
	for _, name := range []string{"approval.approvals.get", "approval.instances.create", "approval.tasks.approve", "approval.tasks.rollback"} {
		if !allowGeneratedCommand("approval", name) {
			t.Fatalf("reviewed approval capability %s was excluded", name)
		}
	}
	for domain, name := range map[string]string{
		"base": "+advperm-enable", "task": "task.agent.register_agent",
		"im": "im.chat.moderation.update", "drive": "+secure-label-update", "calendar": "+transfer",
	} {
		if allowGeneratedCommand(domain, name) {
			t.Fatalf("excluded %s capability %s was admitted", domain, name)
		}
	}
}

func TestCapabilityFromCLISchemaPreservesApprovalConfirmationGate(t *testing.T) {
	schema := cliSchema{
		Name: "approval tasks approve",
		InputSchema: schemaNode{Type: "object", Required: []string{"data"}, Properties: map[string]schemaNode{
			"data": {Type: "object", Carrier: "--data", Required: []string{"instance_code", "task_id"}},
		}},
		Meta: cliSchemaMeta{Risk: "high-risk-write", Danger: true, Scopes: []string{"approval:task:write"}, AccessTokens: []string{"user"}},
	}
	definition, err := capabilityFromCLISchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Risk != "high-impact-write" || !definition.CLIConfirm || definition.Effect != "update" {
		t.Fatalf("approval confirmation metadata = %#v", definition)
	}
}

func TestApprovalReadSchemaBoundsPaginationAndProtectsIdentifiers(t *testing.T) {
	schema := cliSchema{
		Name: "approval tasks query",
		InputSchema: schemaNode{Type: "object", Properties: map[string]schemaNode{
			"params": {Type: "object", Properties: map[string]schemaNode{
				"definition_code": {Type: "string", Flag: "--definition-code"},
				"page_size":       {Type: "integer", Flag: "--page-size"},
				"page_token":      {Type: "string", Flag: "--page-token"},
			}},
		}},
		Meta: cliSchemaMeta{Risk: "read", Scopes: []string{"approval:task:read"}, AccessTokens: []string{"user"}},
	}
	definition, err := capabilityFromCLISchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if !definition.Flags["definition-code"].Private || !definition.Flags["page-token"].Private || definition.Flags["page-size"].Min != 1 || definition.Flags["page-size"].Max != 100 {
		t.Fatalf("approval read hardening was lost: %#v", definition.Flags)
	}
}

func TestApprovalRollbackAndCancellationAreDestructive(t *testing.T) {
	for id, effect := range map[string]string{
		"approval.instances.cancel": "cancel",
		"approval.tasks.rollback":   "revert",
	} {
		gotEffect, _ := operationEffect(id, "high-risk-write")
		if !destructiveName(id) || gotEffect != effect {
			t.Fatalf("%s classified as destructive=%v effect=%s", id, destructiveName(id), gotEffect)
		}
	}
}

func TestOperationEffectUsesSpecificDestructiveSemanticsBeforeGenericDeletion(t *testing.T) {
	tests := map[string]struct{ effect, reversibility string }{
		"sheets.shortcut.range.move":        {"move", "potentially-reversible"},
		"slides.shortcut.replace.slide":     {"overwrite", "potentially-reversible"},
		"sheets.shortcut.history.revert":    {"revert", "potentially-reversible"},
		"mail.shortcut.scheduled.cancel":    {"cancel", "irreversible"},
		"sheets.shortcut.range.clear":       {"clear", "hard-delete"},
		"mail.user_mailbox.messages.delete": {"delete", "hard-delete"},
		"mail.user_mailbox.messages.trash":  {"delete", "soft-delete"},
	}
	for id, expected := range tests {
		effect, reversibility := operationEffect(id, "high-risk-write")
		if effect != expected.effect || reversibility != expected.reversibility {
			t.Fatalf("%s = %s/%s, want %s/%s", id, effect, reversibility, expected.effect, expected.reversibility)
		}
	}
}

func TestGeneratedDestructiveGovernanceIsComplete(t *testing.T) {
	definitions := []feishu.CapabilityDefinition{
		{ID: "base.shortcut.record.delete", Risk: "destructive", Effect: "delete", Reversibility: "hard-delete", Flags: map[string]feishu.CapabilityField{"record-id": {Type: "string"}}},
		{ID: "vc.shortcut.export", Risk: "remote-operation", Effect: "remote", Reversibility: "unknown", Flags: map[string]feishu.CapabilityField{}},
	}
	attachGeneratedDestructiveGuards(definitions, nil)
	attachGeneratedExecutionGovernance(definitions)
	if err := validateGeneratedGovernance(definitions); err != nil {
		t.Fatal(err)
	}
	if definitions[0].GuardProfile != feishu.CapabilityGuardBounded || definitions[0].Postcondition == nil || definitions[0].RetryClass != "never" || len(definitions[0].ConflictKey) != 1 || definitions[0].ConflictKey[0] != "record-id" {
		t.Fatalf("destructive governance = %#v", definitions[0])
	}
	if definitions[1].ExecutionClass != "long-remote" {
		t.Fatalf("remote execution governance = %#v", definitions[1])
	}
}

func TestCapabilityFromCLISchemaPreservesBoundsAndRisk(t *testing.T) {
	schema := cliSchema{
		Name: "mail user_mailbox.messages list",
		InputSchema: schemaNode{Type: "object", Properties: map[string]schemaNode{
			"params": {Type: "object", Required: []string{"page_size", "user_mailbox_id"}, Properties: map[string]schemaNode{
				"page_size":       {Type: "integer", Flag: "--page-size", Minimum: numberPointer(1), Maximum: numberPointer(20)},
				"user_mailbox_id": {Type: "string", Flag: "--user-mailbox-id"},
			}},
		}},
		Meta: cliSchemaMeta{Risk: "read", Scopes: []string{"mail:user_mailbox.message:readonly"}, AccessTokens: []string{"bot", "user"}},
	}
	definition, err := capabilityFromCLISchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if definition.ID != "mail.user_mailbox.messages.list" || definition.Identity != "user" || definition.Risk != "read" {
		t.Fatalf("unexpected definition: %#v", definition)
	}
	if definition.Flags["page-size"].Min != 1 || definition.Flags["page-size"].Max != 20 || !definition.Flags["page-size"].Required {
		t.Fatalf("page-size schema lost: %#v", definition.Flags["page-size"])
	}
	if len(definition.RequiredScopes) != 1 {
		t.Fatalf("scopes lost: %#v", definition.RequiredScopes)
	}
}

func TestCapabilityFromCLISchemaClassifiesBusinessDeletion(t *testing.T) {
	schema := cliSchema{
		Name: "mail user_mailbox.drafts delete",
		InputSchema: schemaNode{Type: "object", Properties: map[string]schemaNode{
			"params": {Type: "object", Required: []string{"draft_id"}, Properties: map[string]schemaNode{
				"draft_id": {Type: "string", Flag: "--draft-id"},
			}},
		}},
		Meta: cliSchemaMeta{Risk: "high-risk-write", AccessTokens: []string{"user"}},
	}
	definition, err := capabilityFromCLISchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Risk != "destructive" || definition.Effect != "delete" || definition.Reversibility != "hard-delete" {
		t.Fatalf("deletion metadata = %#v", definition)
	}
}

func numberPointer(value float64) *float64 { return &value }
