package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func PendingQuestionRevision(turnID, requestID string, questions []map[string]any) string {
	raw, _ := json.Marshal(strings.TrimSpace(requestID))
	return PendingQuestionRevisionRaw(turnID, raw, questions)
}

func PendingQuestionRevisionRaw(turnID string, requestID json.RawMessage, questions []map[string]any) string {
	return PendingQuestionRevisionScoped("", turnID, "", requestID, questions)
}

func PendingQuestionRevisionScoped(taskKey, turnID, ownerClientID string, requestID json.RawMessage, questions []map[string]any) string {
	taskKey = strings.TrimSpace(taskKey)
	turnID = strings.TrimSpace(turnID)
	ownerClientID = strings.TrimSpace(ownerClientID)
	requestID = json.RawMessage(strings.TrimSpace(string(requestID)))
	if len(requestID) == 0 || string(requestID) == "null" || len(questions) == 0 {
		return ""
	}
	payload, err := json.Marshal(struct {
		TaskKey       string           `json:"taskKey,omitempty"`
		TurnID        string           `json:"turnId"`
		OwnerClientID string           `json:"ownerClientId,omitempty"`
		RequestID     json.RawMessage  `json:"requestId"`
		Questions     []map[string]any `json:"questions"`
	}{TaskKey: taskKey, TurnID: turnID, OwnerClientID: ownerClientID, RequestID: requestID, Questions: questions})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:10])
}

func TaskLinkCardJSON(link TaskLink) (string, error) {
	link.LinkState = effectiveTaskLinkState(link, time.Now())
	statusLabel, statusColor := taskLinkStatusTag(link)
	mode := link.ExtraString("activeTurnMode")
	if link.TurnState == "plan_ready" {
		mode = "plan"
	} else if mode == "" {
		mode = link.NextTurnMode
	}
	if mode != "plan" {
		mode = "default"
	}
	title := strings.TrimSpace(link.ProjectName)
	if title == "" {
		title = "Codex 任务"
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"width_mode": "fill", "update_multi": true},
		"header": map[string]any{
			"template": taskLinkCardTemplate(link), "title": plainText(title),
			"text_tag_list": []any{
				map[string]any{"tag": "text_tag", "text": plainText(statusLabel), "color": statusColor},
				map[string]any{"tag": "text_tag", "text": plainText(map[string]string{"default": "默认", "plan": "计划"}[mode]), "color": "green"},
			},
		},
		"body": map[string]any{
			"direction": "vertical", "vertical_spacing": "0px", "padding": "0px 0px 16px 0px",
			"elements": cardBodyElements(taskLinkCardElements(link)),
		},
	}
	data, err := json.Marshal(card)
	return string(data), err
}

func taskLinkCardElements(link TaskLink) []any {
	elements := []any{}
	identity := strings.TrimSpace(link.Title)
	if identity == strings.TrimSpace(link.ProjectName) {
		identity = ""
	}
	metadata := taskLinkMetadata(link)
	controls := []any{}
	if link.LinkState == "active" {
		controls = append(controls, taskCardButton("release_task_link", "断开", "task_link_release", "danger", false, link, nil))
	}
	var primary any
	if identity != "" {
		primary = markdown("**" + boundedCardText(identity, 120) + "**")
	} else if metadata != "" {
		primary = markdown("<font color='grey'>" + metadata + "</font>")
	}
	if primary != nil || len(controls) > 0 {
		vertical := "center"
		if identity != "" {
			vertical = "top"
		}
		elements = append(elements, cardControlRow(primary, controls, vertical))
	}
	if identity != "" && metadata != "" {
		elements = append(elements, withMargin(markdown("<font color='grey'>"+metadata+"</font>"), "4px 20px 0px 20px"))
	}
	if len(elements) > 0 {
		elements = append(elements, map[string]any{"tag": "hr"})
	}
	if latest := strings.TrimSpace(link.ExtraString("latestInput")); latest != "" {
		elements = append(elements, markdown("**你**\n"+boundedCardText(latest, 480)), map[string]any{"tag": "hr"})
	}
	if link.TurnState == "plan_ready" {
		plan := strings.TrimSpace(link.Detail)
		if plan == "" {
			plan = taskLinkCardDetail(link)
		}
		elements = append(elements, markdown("**计划**\n"+plan))
		if link.PendingPlanRevision != "" {
			elements = append(elements, centeredCardControl(taskCardButton("implement_task_link_plan", "开始执行", "task_link_implement_plan", "primary_filled", false, link, map[string]any{"planRevision": link.PendingPlanRevision})))
		}
	} else if questions := taskLinkQuestionElements(link); len(questions) > 0 {
		elements = append(elements, questions...)
	} else {
		detail := strings.TrimSpace(link.Detail)
		if detail == "" {
			detail = taskLinkCardDetail(link)
		}
		if link.ExtraString("latestInput") != "" {
			detail = "**Codex**\n" + detail
		}
		elements = append(elements, markdown(detail))
	}
	if form := taskLinkQuickReplyForm(link); form != nil {
		elements = append(elements, map[string]any{"tag": "hr"}, form)
	} else if link.LinkState == "active" && (link.TurnState == "queued" || link.TurnState == "desktop_action_required") {
		elements = append(elements, map[string]any{"tag": "hr"}, taskCardButton("interrupt_task_link", "停止", "task_link_interrupt", "danger", false, link, nil))
	}
	return elements
}

func taskLinkQuickReplyForm(link TaskLink) any {
	if link.LinkState != "active" || link.TurnState == "queued" || link.TurnState == "desktop_action_required" {
		return nil
	}
	terminal := link.TurnState == "idle" || link.TurnState == "completed" || link.TurnState == "failed" || link.TurnState == "interrupted"
	planReady := link.TurnState == "plan_ready"
	formElements := []any{}
	if terminal {
		mode := link.NextTurnMode
		if mode != "plan" {
			mode = "default"
		}
		selector := map[string]any{
			"tag": "select_static", "name": "turnMode", "required": true, "type": "text", "width": "default",
			"placeholder": plainText("选择模式"), "initial_option": mode,
			"options": []any{
				map[string]any{"text": plainText("默认模式"), "value": "default"},
				map[string]any{"text": plainText("Plan 模式"), "value": "plan"},
			},
		}
		modeRow := map[string]any{"tag": "column_set", "flex_mode": "none", "horizontal_spacing": "8px", "columns": []any{
			map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{markdown("**开始新一轮**")}},
			map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{selector}},
		}}
		formElements = append(formElements, modeRow)
	}
	label, placeholder, submitLabel, submitType := "", "输入下一步问题或要求", "发送", "primary_filled"
	if link.TurnState == "running" {
		placeholder = "补充或修正"
	} else if link.TurnState == "waiting_input" {
		label, placeholder = "回答 Codex", "输入对当前问题的回答"
	} else if planReady {
		label, placeholder, submitLabel, submitType = "修改计划", "输入需要调整的内容", "提交修改", "default"
	}
	input := map[string]any{"tag": "input", "name": "followup", "required": true, "placeholder": plainText(placeholder), "width": "fill", "max_length": 1000, "input_type": "text"}
	if label != "" {
		input["label"], input["label_position"] = plainText(label), "top"
	}
	columns := []any{}
	if link.TurnState == "running" || link.TurnState == "waiting_input" {
		columns = append(columns, map[string]any{"tag": "column", "width": "auto", "vertical_align": "bottom", "elements": []any{taskCardButton("interrupt_task_link", "停止", "task_link_interrupt", "danger", false, link, nil)}})
	}
	intent := map[string]any{}
	if terminal {
		intent["intent"] = "new_turn"
	}
	columns = append(columns,
		map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{input}},
		map[string]any{"tag": "column", "width": "auto", "vertical_align": "bottom", "elements": []any{taskCardButton("submit_task_link_followup", submitLabel, "task_link_followup", submitType, true, link, intent)}},
	)
	formElements = append(formElements, map[string]any{"tag": "column_set", "flex_mode": "none", "horizontal_spacing": "8px", "columns": columns})
	return map[string]any{"tag": "form", "name": "codex_task_link_followup_form", "direction": "vertical", "vertical_spacing": "8px", "elements": formElements}
}

func taskLinkQuestionElements(link TaskLink) []any {
	if link.TurnState != "waiting_input" {
		return nil
	}
	var questions []map[string]any
	if !link.ExtraValue("pendingQuestions", &questions) || len(questions) != 1 {
		return nil
	}
	question := questions[0]
	if secret, _ := question["isSecret"].(bool); secret {
		return nil
	}
	questionRevision := strings.TrimSpace(link.ExtraString("pendingQuestionRevision"))
	if len(questionRevision) != 20 {
		return nil
	}
	questionID, prompt := fmt.Sprint(question["id"]), strings.TrimSpace(fmt.Sprint(question["question"]))
	elements := []any{markdown("**Codex · 需要选择**\n" + prompt)}
	options, _ := question["options"].([]any)
	for index, raw := range options {
		if index >= 3 {
			break
		}
		option, _ := raw.(map[string]any)
		label := strings.TrimSpace(fmt.Sprint(option["label"]))
		if label == "" {
			continue
		}
		description := strings.TrimSpace(fmt.Sprint(option["description"]))
		content := "**" + boundedCardText(label, 60) + "**"
		if description != "" {
			content += "\n<font color='grey'>" + boundedCardText(description, 500) + "</font>"
		}
		elements = append(elements, cardControlRow(markdown(content), []any{taskCardButton("answer_option_"+fmt.Sprint(index+1), "选择", "task_link_answer", "default", false, link, map[string]any{"questionId": questionID, "questionRevision": questionRevision, "answer": label})}, "center"))
	}
	return elements
}

func taskCardButton(name, label, actionName, style string, submit bool, link TaskLink, extra map[string]any) map[string]any {
	value := map[string]any{"namespace": "feishu_bridge", "version": 1, "action": actionName, "taskKey": link.TaskKey, "linkId": link.ID}
	for key, item := range extra {
		value[key] = item
	}
	button := map[string]any{"tag": "button", "name": name, "text": plainText(label), "type": style, "size": "medium", "width": "default", "behaviors": []any{map[string]any{"type": "callback", "value": value}}}
	if submit {
		button["form_action_type"] = "submit"
	}
	return button
}

func cardControlRow(content any, controls []any, vertical string) map[string]any {
	columns := []any{}
	if content != nil {
		columns = append(columns, map[string]any{"tag": "column", "width": "weighted", "weight": 1, "vertical_align": vertical, "elements": []any{content}})
	}
	if len(controls) > 0 {
		columns = append(columns, map[string]any{"tag": "column", "width": "auto", "direction": "horizontal", "horizontal_spacing": "8px", "vertical_align": vertical, "elements": controls})
	}
	return map[string]any{"tag": "column_set", "flex_mode": "none", "horizontal_spacing": "8px", "columns": columns}
}

func centeredCardControl(control any) map[string]any {
	return map[string]any{"tag": "column_set", "flex_mode": "none", "horizontal_align": "center", "columns": []any{map[string]any{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []any{control}}}}
}

func taskLinkMetadata(link TaskLink) string {
	parts := []string{}
	if link.TurnState == "running" && link.TurnOwner == "bridge" {
		parts = append(parts, "飞书控制")
	}
	phase := strings.TrimSpace(link.Phase)
	if link.TurnState == "running" && phase != "" && phase != "运行中" && phase != "当前进展" {
		parts = append(parts, boundedCardText(phase, 40))
	}
	return strings.Join(parts, " · ")
}

func taskLinkStatusTag(link TaskLink) (string, string) {
	if link.LinkState != "active" {
		label := map[string]string{"released": "连接已解除", "expired": "连接已过期"}[link.LinkState]
		if label == "" {
			label = "连接已解除"
		}
		return label, "grey"
	}
	labels := map[string]string{"idle": "已连接", "running": "运行中", "waiting_input": "等待回答", "desktop_action_required": "等待桌面操作", "queued": "已排队", "plan_ready": "等待开始执行", "completed": "已完成", "failed": "本轮失败", "interrupted": "本轮已停止"}
	colors := map[string]string{"idle": "turquoise", "running": "blue", "waiting_input": "orange", "desktop_action_required": "orange", "queued": "orange", "plan_ready": "orange", "completed": "green", "failed": "red", "interrupted": "grey"}
	label, color := labels[link.TurnState], colors[link.TurnState]
	if label == "" {
		label, color = "已连接", "turquoise"
	}
	return label, color
}

func taskLinkCardDetail(link TaskLink) string {
	values := map[string]string{"running": "Codex 正在处理。", "completed": "任务已完成。", "failed": "本轮执行失败。", "interrupted": "当前轮已停止，任务连接继续有效。", "plan_ready": "计划已生成，可以实施或继续修改。"}
	if value := values[link.TurnState]; value != "" {
		return value
	}
	if link.LinkState != "active" {
		return "任务连接已解除。"
	}
	return "任务已连接。回复本消息可继续任务。"
}

func taskLinkCardTemplate(link TaskLink) string {
	if link.LinkState != "active" {
		return "grey"
	}
	values := map[string]string{"running": "blue", "waiting_input": "orange", "desktop_action_required": "orange", "queued": "orange", "plan_ready": "orange", "completed": "green", "failed": "red", "interrupted": "grey"}
	if value := values[link.TurnState]; value != "" {
		return value
	}
	return "turquoise"
}

func plainText(value string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": value}
}
func markdown(value string) map[string]any {
	return map[string]any{"tag": "markdown", "content": value}
}
func withMargin(value map[string]any, margin string) map[string]any {
	value["margin"] = margin
	return value
}
func cardBodyElements(elements []any) []any {
	for _, raw := range elements {
		if value, ok := raw.(map[string]any); ok {
			if _, exists := value["margin"]; !exists {
				value["margin"] = "12px 20px 0px 20px"
			}
		}
	}
	return elements
}
func boundedCardText(value string, maximum int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maximum {
		return string(runes)
	}
	return string(runes[:maximum-1]) + "…"
}
