package main

import "testing"

func TestNodeCompatibilityCommandsResolveToFixedCapabilities(t *testing.T) {
	cases := map[string]string{
		"message/list":          "im.shortcut.chat.messages.list",
		"message/search":        "im.shortcut.messages.search",
		"message/thread":        "im.shortcut.threads.messages.list",
		"knowledge/search":      "docs.shortcut.search",
		"knowledge/read":        "docs.shortcut.fetch",
		"calendar/agenda":       "calendar.shortcut.agenda",
		"task/mine":             "task.shortcut.get.my.tasks",
		"task/related":          "task.shortcut.get.related.tasks",
		"task/tasklists":        "task.tasklists.list",
		"task/tasklist-search":  "task.shortcut.tasklist.search",
		"task/assign":           "task.shortcut.assign",
		"task/reminder":         "task.shortcut.reminder",
		"sheets/get":            "sheets.shortcut.cells.get",
		"sheets/cells":          "sheets.shortcut.cells.get",
		"sheets/table":          "sheets.shortcut.table.get",
		"sheets/revision":       "sheets.shortcut.revision.get",
		"sheets/create-sheet":   "sheets.shortcut.sheet.create",
		"sheets/set-cells":      "sheets.shortcut.cells.set",
		"sheets/append-table":   "sheets.shortcut.table.put",
		"base/records":          "base.shortcut.record.list",
		"meeting/search":        "vc.shortcut.search",
		"note/transcript":       "note.shortcut.transcript",
		"minutes/replace-words": "minutes.shortcut.word.replace",
	}
	for key, expected := range cases {
		family, action := splitCompatibilityKey(key)
		spec, ok := convenienceCapability(family, []string{action})
		if !ok || spec.CapabilityID != expected {
			t.Fatalf("%s resolved to %#v, found=%v", key, spec, ok)
		}
	}
	if _, ok := convenienceCapability("meeting", []string{"end"}); ok {
		t.Fatal("live meeting control entered compatibility facade")
	}
}

func TestCompatibilityPayloadNormalizesCamelCaseAndKeepsOnlyCapabilityFields(t *testing.T) {
	input, err := compatibilityPayload("calendar.shortcut.update", []byte(`{
		"calendarId":"primary","eventId":"evt_1","summary":"Review","unknownAdminField":"blocked"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if input["calendar-id"] != "primary" || input["event-id"] != "evt_1" || input["summary"] != "Review" {
		t.Fatalf("payload was not normalized: %#v", input)
	}
	if _, exists := input["unknown-admin-field"]; exists {
		t.Fatal("unknown payload field escaped fixed capability schema")
	}
}

func TestSheetsInspectKeepsTheLegacyWorkbookAndRevisionComposite(t *testing.T) {
	spec, ok := convenienceCapability("sheets", []string{"inspect"})
	if !ok {
		t.Fatal("sheets inspect compatibility entry is missing")
	}
	if spec.CapabilityID != "sheets.shortcut.workbook.info" {
		t.Fatalf("unexpected primary capability %q", spec.CapabilityID)
	}
	if spec.Additional["revision"] != "sheets.shortcut.revision.get" {
		t.Fatalf("revision read is missing: %#v", spec.Additional)
	}
}
