package feishucommands

import (
	"strings"
	"testing"
	"time"
)

func TestStandupScheduleSortsDeduplicatesAndFindsConflicts(t *testing.T) {
	start, _ := time.Parse(time.RFC3339, "2026-09-02T08:00:00+08:00")
	end, _ := time.Parse(time.RFC3339, "2026-09-02T18:00:00+08:00")
	result := analyzeWorkflowSchedule(map[string]any{"data": map[string]any{"items": []any{
		map[string]any{"summary": "B", "start_time": "2026-09-02T10:30:00+08:00", "end_time": "2026-09-02T11:30:00+08:00"},
		map[string]any{"summary": "A", "start_time": "2026-09-02T09:00:00+08:00", "end_time": "2026-09-02T11:00:00+08:00"},
		map[string]any{"summary": "A", "start_time": "2026-09-02T09:00:00+08:00", "end_time": "2026-09-02T11:00:00+08:00"},
	}}}, start, end)
	if len(result.Slots) != 2 || result.Slots[0].Title != "A" || result.Slots[1].Title != "B" {
		t.Fatalf("unexpected slots: %#v", result.Slots)
	}
	if len(result.Conflicts) != 1 || len(result.Free) != 2 {
		t.Fatalf("unexpected schedule: %#v", result)
	}
}

func TestMeetingSummaryTargetRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	if _, err := resolveMeetingWorkflowTarget([]string{"--meeting-ids", "one,two", "--minute-token", "token"}); err == nil {
		t.Fatal("ambiguous target accepted")
	}
	values := make([]string, 11)
	for index := range values {
		values[index] = "meeting"
	}
	if _, err := resolveMeetingWorkflowTarget([]string{"--meeting-ids", strings.Join(values, ",")}); err == nil {
		t.Fatal("unbounded meeting target accepted")
	}
}
