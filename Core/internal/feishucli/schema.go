package feishucli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type flagSpec struct {
	kind   string
	values []string
}
type commandSpec struct {
	flags    map[string]flagSpec
	count    int
	required []string
}
type compatibilitySpec struct {
	id      string
	aliases map[string]string
}

var outboundIDPattern = regexp.MustCompile(`^OUT-[A-Za-z0-9_-]{3,120}$`)

var compatibilityCommands = map[string]compatibilitySpec{
	"message/list":            {id: "im.shortcut.chat.messages.list", aliases: map[string]string{"limit": "page-size"}},
	"message/search":          {id: "im.shortcut.messages.search", aliases: map[string]string{"limit": "page-size"}},
	"message/thread":          {id: "im.shortcut.threads.messages.list", aliases: map[string]string{"limit": "page-size"}},
	"knowledge/search":        {id: "docs.shortcut.search", aliases: map[string]string{"types": "filter", "limit": "page-size"}},
	"knowledge/read":          {id: "docs.shortcut.fetch", aliases: map[string]string{"target": "doc", "format": "doc-format"}},
	"knowledge/comments/list": {id: "drive.shortcut.list.comments", aliases: map[string]string{"target": "url", "limit": "page-size"}},
	"knowledge/comments/add":  {id: "drive.shortcut.add.comment", aliases: map[string]string{"target": "doc"}},
	"calendar/agenda":         {id: "calendar.shortcut.agenda", aliases: map[string]string{}},
	"calendar/search":         {id: "calendar.shortcut.search.event", aliases: map[string]string{"limit": "page-size"}},
	"calendar/get":            {id: "calendar.shortcut.get", aliases: map[string]string{}},
	"calendar/freebusy":       {id: "calendar.shortcut.freebusy", aliases: map[string]string{}},
	"calendar/create":         {id: "calendar.shortcut.create", aliases: map[string]string{}},
	"calendar/update":         {id: "calendar.shortcut.update", aliases: map[string]string{}},
	"calendar/rsvp":           {id: "calendar.shortcut.rsvp", aliases: map[string]string{}},
	"task/mine":               {id: "task.shortcut.get.my.tasks", aliases: map[string]string{"limit": "page-limit"}},
	"task/related":            {id: "task.shortcut.get.related.tasks", aliases: map[string]string{"limit": "page-limit"}},
	"task/search":             {id: "task.shortcut.search", aliases: map[string]string{"limit": "page-limit"}},
	"task/get":                {id: "task.tasks.get", aliases: map[string]string{}},
	"task/tasklists":          {id: "task.tasklists.list", aliases: map[string]string{"limit": "page-size"}},
	"task/tasklist-search":    {id: "task.shortcut.tasklist.search", aliases: map[string]string{"limit": "page-limit"}},
	"task/create":             {id: "task.shortcut.create", aliases: map[string]string{}},
	"task/update":             {id: "task.shortcut.update", aliases: map[string]string{}},
	"task/complete":           {id: "task.shortcut.complete", aliases: map[string]string{}},
	"task/reopen":             {id: "task.shortcut.reopen", aliases: map[string]string{}},
	"task/assign":             {id: "task.shortcut.assign", aliases: map[string]string{}},
	"task/reminder":           {id: "task.shortcut.reminder", aliases: map[string]string{}},
	"sheets/get":              {id: "sheets.shortcut.cells.get", aliases: map[string]string{}},
	"sheets/inspect":          {id: "sheets.shortcut.workbook.info", aliases: map[string]string{"target": "url"}},
	"sheets/cells":            {id: "sheets.shortcut.cells.get", aliases: map[string]string{"target": "url"}},
	"sheets/table":            {id: "sheets.shortcut.table.get", aliases: map[string]string{"target": "url"}},
	"sheets/search":           {id: "sheets.shortcut.cells.search", aliases: map[string]string{}},
	"sheets/put":              {id: "sheets.shortcut.cells.set", aliases: map[string]string{}},
	"sheets/create":           {id: "sheets.shortcut.workbook.create", aliases: map[string]string{}},
	"sheets/export":           {id: "sheets.shortcut.workbook.export", aliases: map[string]string{}},
	"sheets/import":           {id: "sheets.shortcut.workbook.import", aliases: map[string]string{}},
	"sheets/revision":         {id: "sheets.shortcut.revision.get", aliases: map[string]string{"target": "url"}},
	"sheets/create-sheet":     {id: "sheets.shortcut.sheet.create", aliases: map[string]string{"target": "url"}},
	"sheets/set-cells":        {id: "sheets.shortcut.cells.set", aliases: map[string]string{"target": "url"}},
	"sheets/append-table":     {id: "sheets.shortcut.table.put", aliases: map[string]string{"target": "url"}},
	"base/inspect":            {id: "base.shortcut.url.resolve", aliases: map[string]string{}},
	"base/schema":             {id: "base.shortcut.field.list", aliases: map[string]string{}},
	"base/records":            {id: "base.shortcut.record.list", aliases: map[string]string{"limit": "limit"}},
	"base/search":             {id: "base.shortcut.record.search", aliases: map[string]string{}},
	"base/get":                {id: "base.shortcut.record.get", aliases: map[string]string{}},
	"base/create-records":     {id: "base.shortcut.record.batch.create", aliases: map[string]string{}},
	"base/update-records":     {id: "base.shortcut.record.batch.update", aliases: map[string]string{}},
	"meeting/search":          {id: "vc.shortcut.search", aliases: map[string]string{"limit": "page-size"}},
	"meeting/active":          {id: "vc.shortcut.meeting.list.active", aliases: map[string]string{}},
	"meeting/get":             {id: "vc.meeting.get", aliases: map[string]string{}},
	"meeting/detail":          {id: "vc.shortcut.detail", aliases: map[string]string{}},
	"meeting/events":          {id: "vc.shortcut.meeting.events", aliases: map[string]string{"limit": "page-size"}},
	"meeting/recording":       {id: "vc.shortcut.recording", aliases: map[string]string{}},
	"note/detail":             {id: "note.shortcut.detail", aliases: map[string]string{}},
	"note/transcript":         {id: "note.shortcut.transcript", aliases: map[string]string{}},
	"minutes/search":          {id: "minutes.shortcut.search", aliases: map[string]string{"limit": "page-size"}},
	"minutes/get":             {id: "minutes.minutes.get", aliases: map[string]string{}},
	"minutes/detail":          {id: "minutes.shortcut.detail", aliases: map[string]string{}},
	"minutes/transcript":      {id: "minutes.shortcut.detail", aliases: map[string]string{}},
	"minutes/upload":          {id: "minutes.shortcut.upload", aliases: map[string]string{}},
	"minutes/update-title":    {id: "minutes.shortcut.update", aliases: map[string]string{}},
	"minutes/replace-summary": {id: "minutes.shortcut.summary", aliases: map[string]string{}},
	"minutes/todos":           {id: "minutes.shortcut.todo", aliases: map[string]string{}},
	"minutes/replace-words":   {id: "minutes.shortcut.word.replace", aliases: map[string]string{}},
	"minutes/replace-speaker": {id: "minutes.shortcut.speaker.replace", aliases: map[string]string{}},
}

func needsAction(command string) bool {
	switch command {
	case "profile", "auth", "targets", "capability", "operation", "policy", "events", "message", "knowledge", "calendar", "task", "sheets", "base", "meeting", "note", "minutes", "workflow", "task-link":
		return true
	}
	return false
}
func nestedAction(command, action string) bool {
	return command == "knowledge" && action == "comments" || command == "targets" && (action == "directory" || action == "group-directory")
}
func spec(count int, options, switches, payloads, required string) commandSpec {
	value := commandSpec{flags: map[string]flagSpec{}, count: count, required: strings.Fields(required)}
	for _, name := range strings.Fields(options) {
		value.flags[name] = flagSpec{kind: "string"}
	}
	for _, name := range strings.Fields(switches) {
		value.flags[name] = flagSpec{kind: "boolean"}
	}
	for _, name := range strings.Fields(payloads) {
		value.flags[name] = flagSpec{kind: "payload"}
	}
	for _, name := range []string{"limit", "max-chars", "expected-revision"} {
		if _, ok := value.flags[name]; ok {
			value.flags[name] = flagSpec{kind: "integer"}
		}
	}
	return value
}
func commandSchema(request Request) (commandSpec, error) {
	schema, err := baseCommandSchema(request)
	if err != nil {
		return schema, err
	}
	names := []string{}
	for name, field := range schema.flags {
		if field.kind == "payload" {
			names = append(names, name)
		}
	}
	for _, name := range names {
		schema.flags["input-extension-"+name] = flagSpec{kind: "extension"}
	}
	return schema, nil
}

func baseCommandSchema(request Request) (commandSpec, error) {
	key := request.Command
	if request.Action != "" {
		key += "/" + request.Action
	}
	switch key {
	case "help", "version", "status", "snapshot", "doctor", "permissions", "capabilities", "events/catalog", "events/status", "profile/show", "profile/catalog", "targets/init", "targets/list", "policy/read", "task-link/protocol", "task-link/list":
		return spec(0, "", "", "", ""), nil
	case "auth/configure-existing":
		return spec(0, "profile", "", "payload-file", "payload-file"), nil
	case "auth/start-config":
		return spec(0, "profile", "create-new", "", ""), nil
	case "auth/start-user":
		return spec(0, "scope", "", "", ""), nil
	case "auth/finish-user":
		return spec(0, "device-code", "", "", ""), nil
	case "auth/ensure-current-user":
		return spec(0, "", "", "", ""), nil
	case "events/review", "task-link/sync-review", "task-link/diagnostics":
		return spec(0, "", "", "", ""), nil
	case "events/retry", "task-link/sync-retry":
		return spec(0, "id", "", "", "id"), nil
	case "events/recent":
		return spec(0, "limit", "", "", ""), nil
	case "events/get":
		return spec(0, "fingerprint", "", "", "fingerprint"), nil
	case "result":
		return spec(2, "", "", "", ""), nil
	case "recent":
		return spec(1, "limit", "", "", ""), nil
	case "targets/remove":
		return spec(2, "", "", "", ""), nil
	case "targets/set":
		return spec(2, "type kind", "", "value-file", "value-file"), nil
	case "targets/directory/status", "targets/group-directory/status":
		return spec(0, "", "", "", ""), nil
	case "targets/directory/search", "targets/group-directory/search":
		return spec(0, "query limit", "", "", "query"), nil
	case "targets/directory/bind", "targets/group-directory/bind":
		return spec(0, "name candidate", "", "", "name candidate"), nil
	case "targets/directory/unbind", "targets/group-directory/unbind":
		return spec(0, "name", "", "", "name"), nil
	case "send":
		return spec(0, "target target-name target-group-name format id source", "dry-run async", "media-file content-file text-file", ""), nil
	case "capability/catalog":
		return spec(0, "domain", "", "", ""), nil
	case "capability/get":
		return spec(1, "", "", "", ""), nil
	case "capability/read", "capability/write":
		return withCapabilityFiles(request, spec(1, "", "dry-run async", "payload-file", "payload-file"))
	case "operation/prepare":
		return withCapabilityFiles(request, spec(1, "source", "", "payload-file", "payload-file"))
	case "operation/confirm":
		return spec(0, "id", "", "challenge-file", "id challenge-file"), nil
	case "operation/cancel", "operation/status":
		return spec(0, "id", "", "", "id"), nil
	case "policy/update":
		return spec(0, "expected-revision", "", "payload-file", "expected-revision payload-file"), nil
	case "workflow/standup-report":
		return spec(0, "start end", "", "", "start end"), nil
	case "workflow/meeting-summary":
		return spec(0, "meeting-ids minute-token minutes-url max-chars", "include-transcript", "", ""), nil
	case "task-link/create":
		return spec(0, "", "", "payload-file", "payload-file"), nil
	case "task-link/status", "task-link/interrupt", "task-link/release", "task-link/sync":
		return spec(0, "task-key", "", "", "task-key"), nil
	}
	compatibility, ok := compatibilityCommands[key]
	if !ok {
		return commandSpec{}, errors.New("unsupported bridge client command or action")
	}
	fields, err := capabilityFields(compatibility.id)
	if err != nil {
		return commandSpec{}, err
	}
	value := spec(0, "", "async", "payload-file", "")
	for name, field := range fields {
		kind := field.Type
		if kind == "path" {
			if !field.Output {
				kind = "payload"
			}
		}
		value.flags[name] = flagSpec{kind: kind, values: field.Values}
	}
	for alias, name := range compatibility.aliases {
		value.flags[alias] = value.flags[name]
	}
	if request.Command == "message" {
		value.flags["target"] = flagSpec{kind: "string"}
	}
	if key == "message/thread" {
		value.flags["message-id-file"] = flagSpec{kind: "payload"}
		value.required = append(value.required, "message-id-file")
	}
	if key == "knowledge/comments/add" {
		value.flags["content-file"] = flagSpec{kind: "payload"}
		value.required = append(value.required, "content-file")
	}
	return value, nil
}
func Validate(request Request) error { return validate(request, true) }
func validate(request Request, checkPayload bool) error {
	schema, err := commandSchema(request)
	if err != nil {
		return err
	}
	if len(request.Positionals) != schema.count {
		return errors.New("invalid positional argument count")
	}
	if len(request.Options)+len(request.Switches)+len(request.Payloads) > 128 {
		return errors.New("too many CLI flags")
	}
	for _, value := range request.Positionals {
		if !validArgument(value) {
			return errors.New("invalid positional argument")
		}
	}
	seen := map[string]bool{}
	total := 0
	for name, value := range request.Options {
		field, ok := schema.flags[name]
		if !ok || field.kind == "boolean" || field.kind == "payload" {
			return fmt.Errorf("unsupported option --%s", name)
		}
		if !validArgument(value) {
			return fmt.Errorf("invalid option --%s", name)
		}
		if err := validateFlag(name, value, field); err != nil {
			return err
		}
		seen[name] = true
		total += len(name) + len(value)
	}
	for _, name := range request.Switches {
		field, ok := schema.flags[name]
		if !ok || field.kind != "boolean" || seen[name] {
			return fmt.Errorf("unsupported or duplicate switch --%s", name)
		}
		seen[name] = true
	}
	payloadBytes := 0
	for name, payload := range request.Payloads {
		field, ok := schema.flags[name]
		if !ok || field.kind != "payload" || seen[name] {
			return fmt.Errorf("unsupported payload --%s", name)
		}
		seen[name] = true
		limit := inputLimit(request, name)
		if len(payload) > limit {
			return uploadLimitError(name, limit)
		}
		if limit == MaxPayloadBytes {
			payloadBytes += len(payload)
		}
	}
	if payloadBytes > MaxPayloadBytes || total > 512*1024 {
		return errors.New("CLI request too large")
	}
	for _, name := range schema.required {
		if !seen[name] {
			return fmt.Errorf("missing --%s", name)
		}
	}
	for name := range request.Options {
		if strings.HasPrefix(name, "input-extension-") && !seen[strings.TrimPrefix(name, "input-extension-")] {
			return errors.New("input extension has no corresponding file payload")
		}
	}
	if request.Command == "targets" && (request.Action == "set" || request.Action == "remove") {
		category := request.Positionals[0]
		if category != "message" && category != "document" {
			return errors.New("target category must be message or document")
		}
		if request.Action == "set" {
			required := "type"
			if category == "document" {
				required = "kind"
			}
			if request.Options[required] == "" {
				return fmt.Errorf("missing --%s", required)
			}
		}
	}
	if request.Command == "capability" && request.Action != "catalog" || request.Command == "operation" && request.Action == "prepare" {
		if _, err := capabilityFields(request.Positionals[0]); err != nil {
			return err
		}
	}
	if request.Command == "result" || request.Command == "recent" {
		allowed := []string{"outbox", "actionbox"}
		if request.Command == "recent" {
			allowed = append(allowed, "messages", "audit")
		}
		if !oneOf(request.Positionals[0], allowed) {
			return errors.New("unsupported record kind")
		}
	}
	if request.Command == "send" {
		if id := request.Options["id"]; id != "" && !outboundIDPattern.MatchString(id) {
			return errors.New("invalid outbound request id")
		}
		if countPresent(seen, "target", "target-name", "target-group-name") != 1 {
			return errors.New("provide exactly one of --target, --target-name, or --target-group-name")
		}
		format := request.Options["format"]
		if format != "" && !oneOf(format, []string{"text", "markdown", "card", "image", "file"}) {
			return errors.New("unsupported message format")
		}
		if format == "image" || format == "file" {
			if !seen["media-file"] || seen["content-file"] || seen["text-file"] {
				return errors.New("media send requires only --media-file content")
			}
		} else if countPresent(seen, "content-file", "text-file") != 1 || seen["media-file"] {
			return errors.New("send requires exactly one of --content-file or --text-file")
		}
	}
	if request.Command == "doc" && request.Action == "update" {
		mode := request.Options["mode"]
		if mode != "" && !oneOf(mode, []string{"append", "overwrite", "str_replace"}) {
			return errors.New("invalid document update mode")
		}
		if mode == "str_replace" && !seen["pattern-file"] {
			return errors.New("missing --pattern-file")
		}
	}
	if request.Command == "workflow" && request.Action == "meeting-summary" && countPresent(seen, "meeting-ids", "minute-token", "minutes-url") != 1 {
		return errors.New("provide exactly one meeting summary target")
	}
	if checkPayload {
		if payload, ok := request.Payloads["payload-file"]; ok {
			var object map[string]json.RawMessage
			if json.Unmarshal(payload, &object) != nil || object == nil {
				return errors.New("invalid JSON object payload")
			}
			if err := rejectPayloadPaths(request, object); err != nil {
				return err
			}
		}
		size, err := requestWireBound(request)
		if err != nil {
			return errors.New("invalid CLI request")
		}
		if size > MaxUploadRequestBytes {
			return errors.New("CLI request exceeds bounded upload limit")
		}
	}
	return nil
}
func countPresent(values map[string]bool, names ...string) int {
	count := 0
	for _, name := range names {
		if values[name] {
			count++
		}
	}
	return count
}
func oneOf(value string, choices []string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
func validateFlag(name, value string, field flagSpec) error {
	switch field.kind {
	case "extension":
		if !inputExtensionPattern.MatchString(value) {
			return fmt.Errorf("invalid input extension --%s", name)
		}
	case "integer":
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil || (name == "expected-revision" && number < 0) || (name == "limit" || name == "max-chars") && number < 1 {
			return fmt.Errorf("invalid integer --%s", name)
		}
	case "json":
		if !json.Valid([]byte(value)) {
			return fmt.Errorf("invalid JSON --%s", name)
		}
	case "enum":
		if !oneOf(value, field.values) {
			return fmt.Errorf("invalid enum --%s", name)
		}
	case "path":
		if filepath.IsAbs(value) || strings.Contains(value, "\\") || strings.Contains(value, ":") || value == ".." || strings.HasPrefix(filepath.Clean(value), ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe output path --%s", name)
		}
	}
	return nil
}
