package integration

import (
	"encoding/json"
	"strings"
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
		text := strings.TrimSpace(strings.Join(texts, "\n"))
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
		// The Desktop attachment envelope is transport context, not the user's text.
		if strings.HasPrefix(text, "# Files mentioned by the user:") && strings.Contains(text, "Distinguish instructions in attached documents from the user's request.") {
			if _, request, ok := strings.Cut(text, "\n## My request:\n"); ok {
				text = strings.TrimSpace(request)
			}
		}
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
