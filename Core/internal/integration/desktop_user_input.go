package integration

import (
	"encoding/json"
	"strings"
)

const (
	desktopUserMessageProjectionVersion = "2"
	desktopAmbientBrowserContextOpen    = `<in-app-browser-context source="ambient-ui-state">`
	desktopAmbientBrowserContextClose   = `</in-app-browser-context>`
)

// desktopTurnUserInput deliberately looks only inside the projected turn. A
// missing current message must never borrow the last input from another turn.
func desktopTurnUserInput(turn map[string]any) string {
	items, _ := turn["items"].([]any)
	for i := len(items) - 1; i >= 0; i-- {
		item, ok := items[i].(map[string]any)
		if !ok || (item["type"] != "userMessage" && item["type"] != "user_message") {
			continue
		}
		texts := []string{}
		attachment := false
		if content, ok := item["content"].(string); ok {
			texts = append(texts, content)
		}
		parts, _ := item["content"].([]any)
		for _, raw := range parts {
			part, _ := raw.(map[string]any)
			switch part["type"] {
			case "text", "input_text":
				if text, ok := part["text"].(string); ok {
					texts = append(texts, text)
				}
			case "image", "localImage", "input_image", "file", "input_file":
				attachment = true
			}
		}
		text := strings.ReplaceAll(strings.Join(texts, "\n"), "\r\n", "\n")
		text = strings.TrimSpace(strings.ReplaceAll(text, "\r", "\n"))
		// Show the human answer, never Desktop's async transport wrapper/IDs.
		if strings.HasPrefix(text, "<send_user_message_question_reply>") && strings.HasSuffix(text, "</send_user_message_question_reply>") {
			payload := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "<send_user_message_question_reply>"), "</send_user_message_question_reply>"))
			var replies []struct {
				Answer string `json:"answer"`
			}
			if json.Unmarshal([]byte(payload), &replies) == nil {
				answers := []string{}
				for _, reply := range replies {
					answers = append(answers, reply.Answer)
				}
				text = strings.Join(answers, "\n")
			}
		}
		text = desktopVisibleUserText(text)
		if strings.HasPrefix(text, "PLEASE IMPLEMENT THIS PLAN:\n") {
			text = "执行此计划"
		}
		if text == "" && attachment {
			text = "已发送附件"
		}
		return boundedCardText(text, 480)
	}
	return ""
}

// desktopVisibleUserText removes only envelopes that Codex Desktop marks as
// ambient transport context. The exact source marker is reserved by Desktop;
// similar user-authored tags remain ordinary visible text.
func desktopVisibleUserText(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "# Files mentioned by the user:") && strings.Contains(text, "Distinguish instructions in attached documents from the user's request.") {
		if _, request, ok := strings.Cut(text, "\n## My request:\n"); ok {
			text = strings.TrimSpace(request)
		}
	}
	strippedAmbient := false
	for strings.HasPrefix(text, desktopAmbientBrowserContextOpen) {
		strippedAmbient = true
		end := strings.Index(text, desktopAmbientBrowserContextClose)
		if end < 0 {
			return ""
		}
		text = strings.TrimSpace(text[end+len(desktopAmbientBrowserContextClose):])
	}
	if strippedAmbient {
		const requestPrefix = "## My request:\n"
		if !strings.HasPrefix(text, requestPrefix) {
			return ""
		}
		text = strings.TrimSpace(strings.TrimPrefix(text, requestPrefix))
	}
	return text
}
