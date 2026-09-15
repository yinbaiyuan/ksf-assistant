package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Async prompts are public agentMessage items, not pending server RPCs.
// These adapters mirror Desktop's question-item IDs and accepted reply envelope.
type AsyncInput struct {
	RequestID string
	TurnID    string
	Questions []map[string]any
}

const asyncReplyOpen = "<send_user_message_question_reply>"
const asyncReplyClose = "</send_user_message_question_reply>"

func asyncKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "async_" + hex.EncodeToString(sum[:16])
}

func asyncReplies(items []any) map[string]string {
	result := map[string]string{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		kind, _ := item["type"].(string)
		field := "content"
		if kind == "steeringUserMessage" {
			if item["status"] != "accepted" {
				continue
			}
			field = "input"
		} else if kind != "userMessage" {
			continue
		}
		parts, _ := item[field].([]any)
		if len(parts) != 1 {
			continue
		}
		part, _ := parts[0].(map[string]any)
		if part["type"] != "text" {
			continue
		}
		text, _ := part["text"].(string)
		text = strings.TrimSpace(text)
		if !strings.HasPrefix(text, asyncReplyOpen) || !strings.HasSuffix(text, asyncReplyClose) {
			continue
		}
		payload := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, asyncReplyOpen), asyncReplyClose))
		var answers []struct {
			QuestionItemID string `json:"questionItemId"`
			Answer         string `json:"answer"`
		}
		if json.Unmarshal([]byte(payload), &answers) != nil {
			continue
		}
		for _, answer := range answers {
			if answer.QuestionItemID != "" {
				result[answer.QuestionItemID] = answer.Answer
			}
		}
	}
	return result
}

func AsyncInputs(value any, includeAnswered bool) []AsyncInput {
	result := []AsyncInput{}
	seen := map[string]bool{}
	answered := map[string]string{}
	var collectAnswers func(any)
	collectAnswers = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if items, ok := v["items"].([]any); ok {
				for id, answer := range asyncReplies(items) {
					answered[id] = answer
				}
				return
			}
			for _, child := range v {
				collectAnswers(child)
			}
		case []any:
			for _, child := range v {
				collectAnswers(child)
			}
		}
	}
	collectAnswers(value)
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			items, ok := v["items"].([]any)
			turn := desktopScalarString(v["turnId"])
			if turn == "" {
				turn = desktopScalarString(v["id"])
			}
			if ok && turn != "" {
				for _, raw := range items {
					item, _ := raw.(map[string]any)
					if item["type"] != "agentMessage" || item["delivery"] != "async" {
						continue
					}
					source := desktopScalarString(item["id"])
					if source == "" {
						continue
					}
					questions, _ := item["questions"].([]any)
					legacy := len(questions) == 0
					if legacy {
						questions = []any{map[string]any{"title": item["text"]}}
					}
					for index, rawQ := range questions {
						q, _ := rawQ.(map[string]any)
						if q["isSecret"] == true {
							continue
						}
						title, _ := q["title"].(string)
						if strings.TrimSpace(title) == "" {
							continue
						}
						id := source
						if !legacy {
							data, _ := json.Marshal([]any{"request_user_input_async", source, index})
							id = string(data)
						}
						if _, done := answered[id]; done && !includeAnswered {
							continue
						}
						key := asyncKey(turn + "\x00" + id)
						if seen[key] {
							continue
						}
						seen[key] = true
						options := []any{}
						if values, ok := q["options"].([]any); ok {
							for _, value := range values {
								if label, ok := value.(string); ok && label != "" {
									options = append(options, map[string]any{"label": label, "description": ""})
								}
							}
						}
						result = append(result, AsyncInput{RequestID: key, TurnID: turn, Questions: []map[string]any{{"id": key, "question": title, "options": options, "asyncQuestionItemId": id}}})
					}
				}
				return // Never discover prompts inside tool-result payloads.
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(value)
	return result
}

func findAsyncInput(value any, ref RequestRef) (AsyncInput, bool) {
	if !strings.HasPrefix(ref.CompatibilityString(), "async_") {
		return AsyncInput{}, false
	}
	for _, input := range AsyncInputs(value, false) {
		candidate, _ := RequestRefFromValue(input.RequestID)
		if candidate.Equal(ref) {
			return input, true
		}
	}
	return AsyncInput{}, false
}

func asyncAnswerAccepted(value any, target UserInputTarget) bool {
	input, ok := findAsyncInput(target.State, target.RequestID)
	if !ok {
		return false
	}
	id := input.Questions[0]["asyncQuestionItemId"].(string)
	found := false
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if items, ok := v["items"].([]any); ok {
				if _, ok := asyncReplies(items)[id]; ok {
					found = true
				}
				return
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(value)
	return found
}
