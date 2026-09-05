package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"codexusagebar/core/internal/feishu"
)

type workflowScheduleSlot struct {
	Title string    `json:"title"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type workflowScheduleConflict struct {
	Left  workflowScheduleSlot `json:"left"`
	Right workflowScheduleSlot `json:"right"`
}

type workflowFreeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type workflowSchedule struct {
	Slots     []workflowScheduleSlot     `json:"slots"`
	Conflicts []workflowScheduleConflict `json:"conflicts"`
	Free      []workflowFreeWindow       `json:"free"`
}

type meetingWorkflowTarget struct {
	Source       string
	MeetingIDs   []string
	MinuteTokens []string
}

func runWorkflowClient(dataRoot string, service *feishu.CapabilityService, arguments []string, write clientJSONWriter) error {
	if len(arguments) == 0 {
		return errors.New("missing workflow action")
	}
	switch arguments[0] {
	case "standup-report":
		return runStandupWorkflow(dataRoot, service, arguments[1:], write)
	case "meeting-summary":
		return runMeetingSummaryWorkflow(dataRoot, service, arguments[1:], write)
	default:
		return errors.New("workflow action must be standup-report or meeting-summary")
	}
}

func runStandupWorkflow(dataRoot string, service *feishu.CapabilityService, arguments []string, write clientJSONWriter) error {
	start, err := time.Parse(time.RFC3339, clientOptionalFlag(arguments, "--start"))
	if err != nil {
		return errors.New("workflow start must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, clientOptionalFlag(arguments, "--end"))
	if err != nil || !start.Before(end) || end.Sub(start) > 14*24*time.Hour {
		return errors.New("standup workflow range must be positive and at most 14 days")
	}
	calendar, err := executeWorkflowRead(service, "calendar.shortcut.agenda", map[string]any{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)})
	if err != nil {
		return err
	}
	tasks, err := executeWorkflowRead(service, "task.shortcut.get.my.tasks", map[string]any{"page-limit": 1, "complete": false})
	if err != nil {
		return err
	}
	schedule := analyzeWorkflowSchedule(calendar, start, end)
	_ = feishu.NewAuditLog(dataRoot).Record("workflow", map[string]any{"workflow": "standup-report", "calendarItems": len(schedule.Slots), "conflicts": len(schedule.Conflicts), "publish": false})
	return write(feishu.PublicResult(map[string]any{
		"status": "ok", "workflow": "standup-report", "publish": false,
		"range": map[string]any{"start": start.UTC(), "end": end.UTC()}, "schedule": schedule, "tasks": tasks,
	}))
}

func runMeetingSummaryWorkflow(dataRoot string, service *feishu.CapabilityService, arguments []string, write clientJSONWriter) error {
	target, err := resolveMeetingWorkflowTarget(arguments)
	if err != nil {
		return err
	}
	meetings := map[string]any{}
	minuteLookup := map[string]any{}
	if target.Source == "meeting" {
		meetings, err = executeWorkflowRead(service, "vc.shortcut.detail", map[string]any{"meeting-ids": strings.Join(target.MeetingIDs, ",")})
	} else {
		minuteLookup, err = executeWorkflowRead(service, "minutes.minutes.get", map[string]any{"minute-token": target.MinuteTokens[0]})
	}
	if err != nil {
		return err
	}
	noteIDs := collectWorkflowValues(meetings, "note_id", "noteId")
	noteIDs = appendUniqueBounded(noteIDs, collectWorkflowValues(minuteLookup, "note_id", "noteId"), 10)
	minuteTokens := append([]string{}, target.MinuteTokens...)
	if len(minuteTokens) == 0 {
		minuteTokens = collectWorkflowValues(meetings, "minute_token", "minuteToken")
	}
	if len(noteIDs) > 10 || len(minuteTokens) > 10 {
		return errors.New("meeting workflow material limit exceeded")
	}
	notes := make([]map[string]any, 0, len(noteIDs))
	for _, noteID := range noteIDs {
		value, readErr := executeWorkflowRead(service, "note.shortcut.detail", map[string]any{"note-id": noteID})
		if readErr != nil {
			return readErr
		}
		notes = append(notes, value)
	}
	minutes := map[string]any{}
	if len(minuteTokens) > 0 {
		minutes, err = executeWorkflowRead(service, "minutes.shortcut.detail", map[string]any{
			"minute-tokens": strings.Join(minuteTokens, ","), "summary": true, "todo": true, "chapter": true, "keyword": true,
		})
		if err != nil {
			return err
		}
	}
	transcripts := []map[string]any{}
	if clientHasFlag(arguments, "--include-transcript") {
		maximum := boundedWorkflowInteger(clientOptionalFlag(arguments, "--max-chars"), 20_000, 1, 50_000)
		each := maximum / maxInt(1, len(minuteTokens))
		remaining := maximum
		for _, token := range minuteTokens {
			value, readErr := executeWorkflowRead(service, "minutes.shortcut.detail", map[string]any{"minute-tokens": token, "transcript": true})
			if readErr != nil {
				return readErr
			}
			text := firstWorkflowString(value, "transcript")
			runes := []rune(text)
			limit := minInt(each, remaining)
			if len(runes) > limit {
				runes = runes[:limit]
			}
			text = string(runes)
			remaining -= len(runes)
			transcripts = append(transcripts, map[string]any{"minuteFingerprint": feishu.AuditFingerprint(token), "transcript": text, "returnedCharacters": len(runes)})
			clear(runes)
			if remaining <= 0 {
				break
			}
		}
	}
	_ = feishu.NewAuditLog(dataRoot).Record("workflow", map[string]any{
		"workflow": "meeting-summary", "source": target.Source, "meetings": len(target.MeetingIDs),
		"notes": len(noteIDs), "minutes": len(minuteTokens), "transcripts": len(transcripts), "publish": false,
	})
	return write(feishu.PublicResult(map[string]any{
		"status": "ok", "workflow": "meeting-summary", "source": target.Source, "publish": false,
		"result": map[string]any{"meetings": meetings, "minuteLookup": minuteLookup, "notes": notes, "minutes": minutes, "transcripts": transcripts},
	}))
}

func executeWorkflowRead(service *feishu.CapabilityService, capabilityID string, input map[string]any) (map[string]any, error) {
	prepared, err := service.Prepare(context.Background(), capabilityID, input, "workflow")
	if err != nil {
		return nil, err
	}
	if prepared.Operation.Status != feishu.OperationSucceeded || prepared.Result == nil {
		return nil, fmt.Errorf("workflow read did not complete: %s", capabilityID)
	}
	response, ok := prepared.Result["response"].(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return response, nil
}

func resolveMeetingWorkflowTarget(arguments []string) (meetingWorkflowTarget, error) {
	meetingIDInput := clientOptionalFlag(arguments, "--meeting-ids")
	meetingIDs := splitBoundedWorkflowIDs(meetingIDInput, 10)
	if meetingIDInput != "" && (len(meetingIDs) == 0 || len(meetingIDs) > 10) {
		return meetingWorkflowTarget{}, errors.New("meeting ID count must be between 1 and 10")
	}
	minuteToken := strings.TrimSpace(clientOptionalFlag(arguments, "--minute-token"))
	minutesURL := strings.TrimSpace(clientOptionalFlag(arguments, "--minutes-url"))
	provided := 0
	if len(meetingIDs) > 0 {
		provided++
	}
	if minuteToken != "" {
		provided++
	}
	if minutesURL != "" {
		provided++
	}
	if provided != 1 {
		return meetingWorkflowTarget{}, errors.New("provide exactly one meeting workflow target")
	}
	if len(meetingIDs) > 0 {
		return meetingWorkflowTarget{Source: "meeting", MeetingIDs: meetingIDs}, nil
	}
	if minutesURL != "" {
		parsed, err := url.Parse(minutesURL)
		if err != nil || parsed == nil {
			return meetingWorkflowTarget{}, errors.New("minutes URL must be HTTPS /minutes/<token>")
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if parsed.Scheme != "https" || len(parts) != 2 || parts[0] != "minutes" {
			return meetingWorkflowTarget{}, errors.New("minutes URL must be HTTPS /minutes/<token>")
		}
		minuteToken = parts[1]
	}
	tokens := splitBoundedWorkflowIDs(minuteToken, 1)
	if len(tokens) != 1 {
		return meetingWorkflowTarget{}, errors.New("invalid minute token")
	}
	return meetingWorkflowTarget{Source: "minutes", MinuteTokens: tokens}, nil
}

func splitBoundedWorkflowIDs(value string, maximum int) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > maximum {
		return make([]string, maximum+1)
	}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 400 || strings.IndexFunc(part, func(r rune) bool {
			return !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
		}) >= 0 {
			return nil
		}
		result = append(result, part)
	}
	return result
}

func analyzeWorkflowSchedule(value map[string]any, rangeStart, rangeEnd time.Time) workflowSchedule {
	items := collectWorkflowObjects(value)
	seen := map[string]bool{}
	slots := []workflowScheduleSlot{}
	for _, item := range items {
		startText := firstMapString(item, "start_time", "startTime", "start")
		endText := firstMapString(item, "end_time", "endTime", "end")
		start, startErr := time.Parse(time.RFC3339, startText)
		end, endErr := time.Parse(time.RFC3339, endText)
		if startErr != nil || endErr != nil || !start.Before(end) {
			continue
		}
		title := firstMapString(item, "summary", "title", "name")
		key := title + "\x00" + start.String() + "\x00" + end.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		slots = append(slots, workflowScheduleSlot{Title: title, Start: start.UTC(), End: end.UTC()})
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Start.Before(slots[j].Start) })
	conflicts := []workflowScheduleConflict{}
	for left := range slots {
		for right := left + 1; right < len(slots) && slots[right].Start.Before(slots[left].End); right++ {
			conflicts = append(conflicts, workflowScheduleConflict{Left: slots[left], Right: slots[right]})
		}
	}
	free := []workflowFreeWindow{}
	cursor := rangeStart.UTC()
	for _, slot := range slots {
		if slot.End.Before(cursor) || !slot.Start.Before(rangeEnd.UTC()) {
			continue
		}
		if cursor.Before(slot.Start) {
			free = append(free, workflowFreeWindow{Start: cursor, End: minTime(slot.Start, rangeEnd.UTC())})
		}
		if slot.End.After(cursor) {
			cursor = slot.End
		}
	}
	if cursor.Before(rangeEnd.UTC()) {
		free = append(free, workflowFreeWindow{Start: cursor, End: rangeEnd.UTC()})
	}
	return workflowSchedule{Slots: slots, Conflicts: conflicts, Free: free}
}

func collectWorkflowObjects(value any) []map[string]any {
	result := []map[string]any{}
	var visit func(any)
	visit = func(current any) {
		switch item := current.(type) {
		case map[string]any:
			result = append(result, item)
			for _, child := range item {
				visit(child)
			}
		case []any:
			for _, child := range item {
				visit(child)
			}
		}
	}
	visit(value)
	return result
}

func collectWorkflowValues(value any, keys ...string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, item := range collectWorkflowObjects(value) {
		for _, key := range keys {
			text := strings.TrimSpace(fmt.Sprint(item[key]))
			if text != "" && text != "<nil>" && !seen[text] {
				seen[text] = true
				result = append(result, text)
			}
		}
	}
	return result
}

func firstWorkflowString(value any, key string) string {
	values := collectWorkflowValues(value, key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstMapString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := strings.TrimSpace(fmt.Sprint(value[key])); text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func appendUniqueBounded(existing, additions []string, maximum int) []string {
	seen := map[string]bool{}
	for _, value := range existing {
		seen[value] = true
	}
	for _, value := range additions {
		if !seen[value] {
			existing = append(existing, value)
			seen[value] = true
		}
		if len(existing) > maximum {
			break
		}
	}
	return existing
}

func boundedWorkflowInteger(value string, fallback, minimum, maximum int) int {
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscan(value, &parsed); err != nil || parsed < minimum || parsed > maximum {
		return fallback
	}
	return parsed
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
