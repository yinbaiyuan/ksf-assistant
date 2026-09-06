package integration

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrInvalidTaskCard = errors.New("invalid_task_card")
var taskCardRevision = regexp.MustCompile(`^[a-f0-9]{20}$`)
var taskCardLinkID = regexp.MustCompile(`^LINK-[A-F0-9]{16}$`)
var taskCardQuestionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func cardString(value any) string { text, _ := value.(string); return text }

func decodeTaskCard(action InboundCardAction) (InboundCardAction, bool, error) {
	namespace := cardString(action.Value["namespace"])
	if namespace != "" && namespace != "feishu_bridge" {
		return action, false, nil
	}
	if namespace == "" && !strings.HasPrefix(action.Action, "task_link_") {
		return action, false, nil
	}
	if namespace != "" && fmt.Sprint(action.Value["version"]) != "1" {
		return action, true, ErrInvalidTaskCard
	}
	for key, field := range map[string]*string{"action": &action.Action, "taskKey": &action.TaskKey, "linkId": &action.LinkID, "questionRevision": &action.QuestionRevision} {
		if value, present := action.Value[key]; present {
			text, ok := value.(string)
			if !ok || *field != "" && *field != text {
				return action, true, ErrInvalidTaskCard
			}
			*field = text
		}
	}
	if !taskCardRevision.MatchString(action.TaskKey) || !taskCardLinkID.MatchString(action.LinkID) {
		return action, true, ErrInvalidTaskCard
	}
	form := make(map[string]any, len(action.FormValue))
	for key, value := range action.FormValue {
		form[key] = value
	}
	if value, ok := form["followup"]; ok {
		form["followup"] = strings.TrimSpace(strings.ReplaceAll(cardString(value), "\x00", ""))
	}
	action.FormValue = form
	return action, true, nil
}

func validateTaskCard(action InboundCardAction) error {
	switch action.Action {
	case "task_link_interrupt", "task_link_release":
	case "task_link_followup":
		text, mode := cardString(action.FormValue["followup"]), cardString(action.FormValue["turnMode"])
		if text == "" || len([]rune(text)) > 1000 || mode != "" && mode != "default" && mode != "plan" {
			return ErrInvalidTaskCard
		}
	case "task_link_implement_plan":
		if !taskCardRevision.MatchString(cardString(action.Value["planRevision"])) {
			return ErrInvalidTaskCard
		}
	case "task_link_answer":
		answer := cardString(action.Value["answer"])
		if !taskCardRevision.MatchString(action.QuestionRevision) || !taskCardQuestionID.MatchString(cardString(action.Value["questionId"])) || strings.TrimSpace(answer) == "" || len([]rune(answer)) > 160 {
			return ErrInvalidTaskCard
		}
	default:
		return ErrInvalidTaskCard
	}
	return nil
}

func canonicalTaskCardEnvelope(envelope map[string]any) error {
	value, _ := envelope["Value"].(map[string]any)
	if cardString(value["namespace"]) != "feishu_bridge" || fmt.Sprint(value["version"]) != "1" {
		return nil
	}
	for key, field := range map[string]string{"action": "Action", "taskKey": "TaskKey", "linkId": "LinkID", "questionRevision": "QuestionRevision"} {
		if text, ok := value[key].(string); ok {
			if prior := cardString(envelope[field]); prior != "" && prior != text {
				return ErrInvalidTaskCard
			}
			envelope[field] = text
		}
	}
	if cardString(value["action"]) != "task_link_followup" {
		return nil
	}
	form, _ := envelope["FormValue"].(map[string]any)
	if form == nil {
		return nil
	}
	text := strings.TrimSpace(strings.ReplaceAll(cardString(form["followup"]), "\x00", ""))
	form["followup"] = text
	raw, _ := envelope["Raw"].(map[string]any)
	action, _ := raw["action"].(map[string]any)
	if original, ok := action["form_value"].(map[string]any); ok && len(original) > 0 {
		original["followup"] = text
	} else if original, ok := raw["form_value"].(map[string]any); ok {
		original["followup"] = text
	}
	return nil
}
