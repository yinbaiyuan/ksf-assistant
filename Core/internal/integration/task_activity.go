package integration

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
)

// Store only a public action and bounded per-turn path digests, never command
// text, tool arguments/output, diffs or full file paths.
type taskActivity struct {
	TurnID  string                  `json:"turnId"`
	Action  string                  `json:"action,omitempty"`
	Title   string                  `json:"title,omitempty"`
	Recent  string                  `json:"recent,omitempty"`
	Files   []string                `json:"files,omitempty"`
	Limited bool                    `json:"limited,omitempty"`
	Edits   map[string]activityEdit `json:"edits,omitempty"`
}

type activityEdit struct {
	Event   string `json:"event"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Known   bool   `json:"known"`
}

const activityFileLimit = 1024

const activityProjectionVersion = "4"

func projectTaskActivity(turn map[string]any) taskActivity {
	result := taskActivity{TurnID: cleanString(turn["id"]), Edits: map[string]activityEdit{}}
	items, _ := turn["items"].([]any)
	files := map[string]bool{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		kind := cleanString(item["type"])
		status := strings.ToLower(statusType(item["status"]))
		// Only the public summary heading is eligible. Never inspect reasoning
		// content, encrypted payloads, or agentMessage analysis for a status.
		if kind == "reasoning" {
			var summaries []string
			switch values := item["summary"].(type) {
			case []string:
				summaries = values
			case []any:
				for _, value := range values {
					if s, ok := value.(string); ok {
						summaries = append(summaries, s)
					}
				}
			}
			for i := len(summaries) - 1; i >= 0; i-- {
				if title := publicProgressTitle(summaries[i]); title != "" {
					result.Title = title
					break
				}
			}
		}
		if kind == "agentMessage" && (cleanString(item["phase"]) == "final_answer" || cleanString(item["phase"]) == "commentary" || cleanString(item["phase"]) == "") && hasVisualizationReference(cleanString(item["text"])) {
			result.Recent = "已创建可视化"
		}
		if kind == "contextCompaction" {
			// Desktop's synthetic item has a completed boolean, not status.
			// Automatic compaction is labeled conversation optimization in Work
			// mode. Keep the status-based adapter for older/protocol snapshots.
			if completed, known := item["completed"].(bool); known {
				runningLabel, doneLabel := "正在优化对话", "已优化对话"
				if cleanString(item["source"]) == "manual" {
					runningLabel, doneLabel = "正在压缩上下文", "上下文已压缩"
				}
				if completed {
					result.Action = ""
					result.Title, result.Recent = doneLabel, doneLabel
				} else {
					result.Action = runningLabel
				}
			} else if isRunningStatus(status) {
				result.Action = "正在压缩上下文"
			} else if status == "completed" {
				result.Recent = "上下文已压缩"
			} else if status == "" {
				result.Recent = "上下文压缩"
			}
		}
		if status == "completed" {
			switch kind {
			case "fileChange":
				result.Recent = "已编辑文件"
			case "commandExecution":
				result.Recent = commandActivity(item, false)
			case "mcpToolCall", "dynamicToolCall":
				result.Recent = "工具调用已结束"
			}
		}
		if kind == "fileChange" && status == "completed" {
			changes, _ := item["changes"].([]any)
			for _, rawChange := range changes {
				change, _ := rawChange.(map[string]any)
				value := strings.TrimSpace(cleanString(change["path"]))
				if value == "" {
					continue
				}
				value = path.Clean(strings.ReplaceAll(value, "\\", "/"))
				if len(value) > 1 && value[1] == ':' {
					value = strings.ToLower(value)
				}
				key := cardDigest(value)
				if !files[key] && len(files) >= activityFileLimit {
					result.Limited = true
					continue
				}
				files[key] = true
				diff, ok := change["diff"].(string)
				added, removed, known := activityDiffLines(diff)
				event := cleanString(item["id"])
				edit := activityEdit{Event: cardDigest(event + ":" + diff), Added: added, Removed: removed, Known: ok && known && event != ""}
				if previous, exists := result.Edits[key]; exists && (previous.Event != edit.Event || !previous.Known) {
					edit.Known = false
				}
				result.Edits[key] = edit
			}
		}
		if !isRunningStatus(status) {
			continue
		}
		action := ""
		switch kind {
		case "commandExecution":
			action = commandActivity(item, true)
		case "fileChange":
			action = "正在编辑文件"
		case "mcpToolCall", "dynamicToolCall":
			action = "正在调用工具"
		case "webSearch":
			action = "正在搜索网页"
		}
		if action != "" {
			result.Action = action
		}
	}
	for key := range files {
		result.Files = append(result.Files, key)
	}
	sort.Strings(result.Files)
	return result
}

// Accept a public summary title, not a multiline explanatory body.
// Incomplete Markdown titles remain hidden while streaming.
func publicProgressTitle(summary string) string {
	s := strings.TrimSpace(summary)
	title := s
	if strings.HasPrefix(s, "**") {
		end := strings.Index(s[2:], "**")
		if end < 0 {
			return ""
		}
		title = strings.TrimSpace(s[2 : 2+end])
	} else if strings.HasPrefix(s, "#") {
		line := strings.SplitN(s, "\n", 2)[0]
		n := len(line) - len(strings.TrimLeft(line, "#"))
		if n > 6 || n == len(line) || line[n] != ' ' {
			return ""
		}
		title = strings.TrimSpace(line[n:])
	} else if strings.HasPrefix(s, "*") || strings.HasPrefix(s, "_") || strings.HasPrefix(s, "`") {
		return ""
	}
	if strings.ContainsAny(title, "\r\n") || len([]rune(title)) > 240 {
		return ""
	}
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, title)
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`", "!", "\\!").Replace(title)
}

func commandActivity(item map[string]any, running bool) string {
	fallback, prefix := "命令已执行", "已"
	if running {
		fallback, prefix = "正在运行命令", "正在"
	}
	actions, _ := item["commandActions"].([]any)
	if len(actions) != 1 {
		return fallback
	}
	parsed, _ := actions[0].(map[string]any)
	switch cleanString(parsed["type"]) {
	case "read":
		name := publicActivityFileName(cleanString(parsed["path"]))
		if name == "" {
			return "📖 " + prefix + "读取文件"
		}
		return "📖 " + prefix + "读取「" + name + "」"
	case "search":
		return prefix + "搜索"
	case "listFiles":
		return prefix + "查看文件列表"
	}
	return fallback
}

func publicActivityFileName(value string) string {
	if value == "" || strings.Contains(value, "://") {
		return ""
	}
	name := path.Base(strings.ReplaceAll(value, "\\", "/"))
	lower := strings.ToLower(name)
	if strings.HasPrefix(name, ".") || name == "/" {
		return ""
	}
	for _, sensitive := range []string{"secret", "credential", "token", "password", "id_rsa", "id_ed25519", ".pem", ".key"} {
		if strings.Contains(lower, sensitive) {
			return ""
		}
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	runes := []rune(name)
	if len(runes) > 64 {
		name = string(runes[:61]) + "…"
	}
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "[", "\\[",
		"]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`", "!", "\\!").Replace(name)
}

func mergeTaskActivity(link TaskLink, next taskActivity) taskActivity {
	var previous taskActivity
	link.ExtraValue("taskActivity", &previous)
	if previous.TurnID != next.TurnID {
		return next
	}
	// A partial snapshot may omit the latest title. Retain only that public
	// title within the same turn, never an old running action or another turn.
	if next.Title == "" {
		next.Title = previous.Title
	}
	keys := map[string]bool{}
	for _, key := range next.Files {
		keys[key] = true
	}
	next.Limited = next.Limited || previous.Limited
	for _, key := range previous.Files {
		if !keys[key] && len(keys) >= activityFileLimit {
			next.Limited = true
			continue
		}
		keys[key] = true
		if next.Edits == nil {
			next.Edits = map[string]activityEdit{}
		}
		if old, found := previous.Edits[key]; found {
			if incoming, exists := next.Edits[key]; exists {
				if old.Event != incoming.Event || !old.Known {
					incoming.Known = false
					next.Edits[key] = incoming
				}
			} else {
				next.Edits[key] = old
			}
		}
	}
	next.Files = nil
	for key := range keys {
		next.Files = append(next.Files, key)
	}
	sort.Strings(next.Files)
	return next
}

func taskActivityText(link TaskLink) string {
	var activity taskActivity
	if link.LinkState != "active" || !link.ExtraValue("taskActivity", &activity) || activity.TurnID == "" || activity.TurnID != link.ExtraString("latestInputTurnId") {
		return ""
	}
	count := ""
	if len(activity.Files) > 0 {
		prefix := "已修改 "
		if activity.Limited {
			prefix = "已修改至少 "
		}
		count = fmt.Sprintf("%s%d 个文件", prefix, len(activity.Files))
		added, removed, known := 0, 0, !activity.Limited
		for _, key := range activity.Files {
			edit, ok := activity.Edits[key]
			known = known && ok && edit.Known
			added += edit.Added
			removed += edit.Removed
		}
		if known {
			count += fmt.Sprintf(" · +%d −%d", added, removed)
		}
	}
	state := map[string]string{"completed": "已完成", "failed": "执行失败", "interrupted": "已停止", "waiting_input": "等待你的回复", "desktop_action_required": "等待桌面操作", "plan_ready": "等待开始执行", "queued": "等待执行"}[link.TurnState]
	if state != "" {
		if count != "" {
			return state + " · " + count
		}
		return state
	}
	if link.TurnState != "running" {
		return ""
	}
	action := activity.Action
	if action == "" {
		action = activity.Title
	}
	if action == "" {
		action = activity.Recent
	}
	if action == "" {
		action = "正在处理"
	}
	if count != "" {
		return action + "\n" + count
	}
	return action
}

// Recognize only an explicit assistant output reference, not prose claiming a
// visualization was created. Its private path is never projected into the card.
func hasVisualizationReference(text string) bool {
	const marker = "visualize"
	for {
		_, rest, ok := strings.Cut(text, marker)
		if !ok {
			return false
		}
		payload, tail, closed := strings.Cut(rest, "")
		if !closed {
			return false
		}
		var ref struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(payload), &ref) == nil && strings.HasSuffix(strings.ToLower(ref.Path), ".html") {
			return true
		}
		text = tail
	}
}

// A single file's unified diff is reliable only for a single observed edit.
// Repeated edits are deliberately not summed as if they were a net turn diff.
func activityDiffLines(diff string) (added, removed int, known bool) {
	inHunk := false
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "@@ ") {
			inHunk = true
			known = true
			continue
		}
		if !inHunk {
			continue
		}
		if strings.HasPrefix(line, "+") {
			added++
		} else if strings.HasPrefix(line, "-") {
			removed++
		} else if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\\") {
			return 0, 0, false
		}
	}
	return
}
