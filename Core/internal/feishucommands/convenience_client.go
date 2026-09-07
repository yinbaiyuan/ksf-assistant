package feishucommands

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ksfassistant/core/internal/feishu"
)

type convenienceSpec struct {
	CapabilityID string
	Aliases      map[string]string
	ResultName   string
	Additional   map[string]string
}

var convenienceCapabilities = map[string]convenienceSpec{
	"message/list":            {CapabilityID: "im.shortcut.chat.messages.list", Aliases: map[string]string{"limit": "page-size"}},
	"message/search":          {CapabilityID: "im.shortcut.messages.search", Aliases: map[string]string{"limit": "page-size"}},
	"message/thread":          {CapabilityID: "im.shortcut.threads.messages.list", Aliases: map[string]string{"limit": "page-size"}},
	"knowledge/search":        {CapabilityID: "docs.shortcut.search", Aliases: map[string]string{"types": "filter", "limit": "page-size"}},
	"knowledge/read":          {CapabilityID: "docs.shortcut.fetch", Aliases: map[string]string{"target": "doc", "format": "doc-format"}},
	"knowledge/comments/list": {CapabilityID: "drive.shortcut.list.comments", Aliases: map[string]string{"target": "url", "limit": "page-size"}},
	"knowledge/comments/add":  {CapabilityID: "drive.shortcut.add.comment", Aliases: map[string]string{"target": "doc"}},
	"calendar/agenda":         {CapabilityID: "calendar.shortcut.agenda"},
	"calendar/search":         {CapabilityID: "calendar.shortcut.search.event", Aliases: map[string]string{"limit": "page-size"}},
	"calendar/get":            {CapabilityID: "calendar.shortcut.get"},
	"calendar/freebusy":       {CapabilityID: "calendar.shortcut.freebusy"},
	"calendar/create":         {CapabilityID: "calendar.shortcut.create"},
	"calendar/update":         {CapabilityID: "calendar.shortcut.update"},
	"calendar/rsvp":           {CapabilityID: "calendar.shortcut.rsvp"},
	"task/mine":               {CapabilityID: "task.shortcut.get.my.tasks", Aliases: map[string]string{"limit": "page-limit"}},
	"task/related":            {CapabilityID: "task.shortcut.get.related.tasks", Aliases: map[string]string{"limit": "page-limit"}},
	"task/search":             {CapabilityID: "task.shortcut.search", Aliases: map[string]string{"limit": "page-limit"}},
	"task/get":                {CapabilityID: "task.tasks.get"},
	"task/tasklists":          {CapabilityID: "task.tasklists.list", Aliases: map[string]string{"limit": "page-size"}},
	"task/tasklist-search":    {CapabilityID: "task.shortcut.tasklist.search", Aliases: map[string]string{"limit": "page-limit"}},
	"task/create":             {CapabilityID: "task.shortcut.create"},
	"task/update":             {CapabilityID: "task.shortcut.update"},
	"task/complete":           {CapabilityID: "task.shortcut.complete"},
	"task/reopen":             {CapabilityID: "task.shortcut.reopen"},
	"task/assign":             {CapabilityID: "task.shortcut.assign"},
	"task/reminder":           {CapabilityID: "task.shortcut.reminder"},
	"sheets/get":              {CapabilityID: "sheets.shortcut.cells.get"},
	"sheets/inspect":          {CapabilityID: "sheets.shortcut.workbook.info", ResultName: "workbook", Additional: map[string]string{"revision": "sheets.shortcut.revision.get"}, Aliases: map[string]string{"target": "url"}},
	"sheets/cells":            {CapabilityID: "sheets.shortcut.cells.get", Aliases: map[string]string{"target": "url"}},
	"sheets/table":            {CapabilityID: "sheets.shortcut.table.get", Aliases: map[string]string{"target": "url"}},
	"sheets/search":           {CapabilityID: "sheets.shortcut.cells.search"},
	"sheets/put":              {CapabilityID: "sheets.shortcut.cells.set"},
	"sheets/create":           {CapabilityID: "sheets.shortcut.workbook.create"},
	"sheets/export":           {CapabilityID: "sheets.shortcut.workbook.export"},
	"sheets/import":           {CapabilityID: "sheets.shortcut.workbook.import"},
	"sheets/revision":         {CapabilityID: "sheets.shortcut.revision.get", Aliases: map[string]string{"target": "url"}},
	"sheets/create-sheet":     {CapabilityID: "sheets.shortcut.sheet.create", Aliases: map[string]string{"target": "url"}},
	"sheets/set-cells":        {CapabilityID: "sheets.shortcut.cells.set", Aliases: map[string]string{"target": "url"}},
	"sheets/append-table":     {CapabilityID: "sheets.shortcut.table.put", Aliases: map[string]string{"target": "url"}},
	"base/inspect":            {CapabilityID: "base.shortcut.url.resolve"},
	"base/schema":             {CapabilityID: "base.shortcut.field.list"},
	"base/records":            {CapabilityID: "base.shortcut.record.list", Aliases: map[string]string{"limit": "limit"}},
	"base/search":             {CapabilityID: "base.shortcut.record.search"},
	"base/get":                {CapabilityID: "base.shortcut.record.get"},
	"base/create-records":     {CapabilityID: "base.shortcut.record.batch.create"},
	"base/update-records":     {CapabilityID: "base.shortcut.record.batch.update"},
	"meeting/search":          {CapabilityID: "vc.shortcut.search", Aliases: map[string]string{"limit": "page-size"}},
	"meeting/active":          {CapabilityID: "vc.shortcut.meeting.list.active"},
	"meeting/get":             {CapabilityID: "vc.meeting.get"},
	"meeting/detail":          {CapabilityID: "vc.shortcut.detail"},
	"meeting/events":          {CapabilityID: "vc.shortcut.meeting.events", Aliases: map[string]string{"limit": "page-size"}},
	"meeting/recording":       {CapabilityID: "vc.shortcut.recording"},
	"note/detail":             {CapabilityID: "note.shortcut.detail"},
	"note/transcript":         {CapabilityID: "note.shortcut.transcript"},
	"minutes/search":          {CapabilityID: "minutes.shortcut.search", Aliases: map[string]string{"limit": "page-size"}},
	"minutes/get":             {CapabilityID: "minutes.minutes.get"},
	"minutes/detail":          {CapabilityID: "minutes.shortcut.detail"},
	"minutes/transcript":      {CapabilityID: "minutes.shortcut.detail"},
	"minutes/upload":          {CapabilityID: "minutes.shortcut.upload"},
	"minutes/update-title":    {CapabilityID: "minutes.shortcut.update"},
	"minutes/replace-summary": {CapabilityID: "minutes.shortcut.summary"},
	"minutes/todos":           {CapabilityID: "minutes.shortcut.todo"},
	"minutes/replace-words":   {CapabilityID: "minutes.shortcut.word.replace"},
	"minutes/replace-speaker": {CapabilityID: "minutes.shortcut.speaker.replace"},
}

func splitCompatibilityKey(value string) (string, string) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return value, ""
	}
	return parts[0], parts[1]
}

func convenienceCapability(family string, arguments []string) (convenienceSpec, bool) {
	if len(arguments) == 0 {
		return convenienceSpec{}, false
	}
	key := family + "/" + arguments[0]
	if family == "knowledge" && arguments[0] == "comments" && len(arguments) > 1 {
		key += "/" + arguments[1]
	}
	value, ok := convenienceCapabilities[key]
	return value, ok
}

func (call *invocation) runConvenienceClient(dataRoot string, settings feishu.Settings, service *feishu.CapabilityService, family string, arguments []string, write clientJSONWriter) error {
	spec, ok := convenienceCapability(family, arguments)
	if !ok {
		return fmt.Errorf("unsupported %s action", family)
	}
	definition, ok := feishu.CapabilityByID(spec.CapabilityID)
	if !ok {
		return errors.New("compatibility capability unavailable")
	}
	flagArguments := arguments[1:]
	if family == "knowledge" && arguments[0] == "comments" {
		flagArguments = arguments[2:]
	}
	input := map[string]any{}
	if clientOptionalFlag(flagArguments, "--payload-file") != "" {
		payload, err := call.clientPayload(flagArguments)
		if err != nil {
			return err
		}
		input, err = compatibilityPayload(spec.CapabilityID, payload)
		if err != nil {
			return err
		}
	}
	for oldName, newName := range spec.Aliases {
		if value := clientOptionalFlag(flagArguments, "--"+oldName); value != "" {
			input[newName] = compatibilityFieldValue(definition.Flags[newName], value)
		}
	}
	for name, field := range definition.Flags {
		if _, exists := input[name]; exists {
			continue
		}
		if field.Type == "boolean" {
			if clientHasFlag(flagArguments, "--"+name) {
				input[name] = true
			}
			continue
		}
		if value := clientOptionalFlag(flagArguments, "--"+name); value != "" {
			input[name] = compatibilityFieldValue(field, value)
		}
	}
	if err := call.applyCompatibilityPrivateInputs(family, arguments, flagArguments, input); err != nil {
		return err
	}
	if err := applyCompatibilityTarget(dataRoot, family, input, flagArguments); err != nil {
		return err
	}
	if len(spec.Additional) > 0 {
		results := map[string]any{}
		primary, prepareErr := call.prepare(service, call.ctx, spec.CapabilityID, input, "codex")
		if prepareErr != nil {
			return prepareErr
		}
		results[spec.ResultName] = primary.Result
		labels := make([]string, 0, len(spec.Additional))
		for label := range spec.Additional {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		for _, label := range labels {
			capabilityID := spec.Additional[label]
			additionalDefinition, exists := feishu.CapabilityByID(capabilityID)
			if !exists || additionalDefinition.Risk != "read" || !feishu.CapabilityPublished(additionalDefinition) {
				return errors.New("compatibility composite capability unavailable")
			}
			additional, additionalErr := call.prepare(service, call.ctx, capabilityID, filterCompatibilityInput(additionalDefinition, input), "codex")
			if additionalErr != nil {
				return additionalErr
			}
			results[label] = additional.Result
		}
		return write(feishu.PublicResult(map[string]any{"status": "ok", "result": results}))
	}
	result, err := call.prepare(service, call.ctx, spec.CapabilityID, input, "codex")
	if err != nil {
		return err
	}
	if result.Submitted {
		_ = feishu.WakeQueue(dataRoot, "actionbox")
	}
	if definition.Risk == "read" || result.Operation.Status == feishu.OperationAwaitingConfirmation || clientHasFlag(flagArguments, "--async") {
		return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": result.Operation, "challenge": result.Challenge, "submitted": result.Submitted, "result": result.Result}))
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		view, statusErr := service.Status(result.Operation.ID)
		if statusErr != nil {
			return statusErr
		}
		if operationTerminal(view.Status) {
			return write(feishu.PublicResult(map[string]any{"status": "ok", "operation": view}))
		}
		if err := call.wait(250 * time.Millisecond); err != nil {
			return err
		}
	}
	view, _ := service.Status(result.Operation.ID)
	return write(feishu.PublicResult(map[string]any{"status": "pending", "operation": view, "nextAction": operationPendingNextAction(view)}))
}

func filterCompatibilityInput(definition feishu.CapabilityDefinition, input map[string]any) map[string]any {
	filtered := map[string]any{}
	for name := range definition.Flags {
		if value, exists := input[name]; exists {
			filtered[name] = value
		}
	}
	return filtered
}

func compatibilityPayload(capabilityID string, payload []byte) (map[string]any, error) {
	definition, ok := feishu.CapabilityByID(capabilityID)
	if !ok {
		return nil, errors.New("unknown_capability")
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, errors.New("invalid compatibility payload")
	}
	result := map[string]any{}
	remainder := map[string]any{}
	for name, value := range raw {
		normalized := camelToKebab(name)
		if field, exists := definition.Flags[normalized]; exists {
			result[normalized] = normalizeJSONField(field, value)
		} else {
			remainder[name] = value
		}
	}
	for _, carrier := range []string{"data", "json"} {
		if field, exists := definition.Flags[carrier]; exists && len(remainder) > 0 {
			result[carrier] = normalizeJSONField(field, remainder)
			break
		}
	}
	return result, nil
}

func compatibilityFieldValue(field feishu.CapabilityField, value string) any {
	switch field.Type {
	case "integer":
		parsed, _ := strconv.Atoi(value)
		return parsed
	case "boolean":
		parsed, _ := strconv.ParseBool(value)
		return parsed
	case "csv":
		return strings.Split(value, ",")
	case "json":
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			return decoded
		}
	}
	return value
}

func normalizeJSONField(field feishu.CapabilityField, value any) any {
	if field.Type != "json" {
		return value
	}
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(text), &decoded) == nil {
			return decoded
		}
	}
	return value
}

func (call *invocation) applyCompatibilityPrivateInputs(family string, arguments, flags []string, input map[string]any) error {
	if family == "message" && len(arguments) > 0 && arguments[0] == "thread" {
		value, err := call.clientPrivateValue(flags, "--message-id-file")
		if err != nil {
			return err
		}
		input["thread"] = strings.TrimSpace(string(value))
	}
	if family == "knowledge" && len(arguments) > 1 && arguments[0] == "comments" && arguments[1] == "add" {
		value, err := call.clientPrivateValue(flags, "--content-file")
		if err != nil {
			return err
		}
		input["content"] = string(value)
	}
	if family == "minutes" && len(arguments) > 0 && arguments[0] == "transcript" {
		input["transcript"] = true
	}
	return nil
}

func applyCompatibilityTarget(dataRoot, family string, input map[string]any, flags []string) error {
	if family != "message" {
		return nil
	}
	targetValue := clientOptionalFlag(flags, "--target")
	if targetValue == "" {
		return nil
	}
	config, err := feishu.NewClientConfigStore(dataRoot).Load()
	if err != nil {
		return err
	}
	target, err := config.ResolveMessageTarget(targetValue)
	if err != nil {
		return err
	}
	if target.Type != "chat_id" {
		return errors.New("message history reads require a group chat target")
	}
	input["chat-id"] = target.ID
	return nil
}

var compatibilityWordBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func camelToKebab(value string) string {
	value = compatibilityWordBoundary.ReplaceAllString(value, `${1}-${2}`)
	return strings.Map(func(r rune) rune {
		if r == '_' {
			return '-'
		}
		return unicode.ToLower(r)
	}, value)
}
