package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type InboundMessage struct {
	EventID, MessageID, RootID, ParentID, ChatID, ChatType, MessageType, Text, SenderOpenID string
	Raw                                                                                     map[string]any
}
type InboundCardAction struct {
	EventID, OperatorOpenID, ChatID, MessageID, Action, TaskKey, LinkID, QuestionRevision, Token string
	Value, FormValue                                                                             map[string]any
	Raw                                                                                          map[string]any
}
type InboundMessageHandler func(context.Context, InboundMessage) error
type InboundCardHandler func(context.Context, InboundCardAction) error
type InboundProcessor struct {
	inbox       *EventInbox
	workbox     *InboundWorkbox
	config      ClientConfig
	settings    Settings
	message     InboundMessageHandler
	card        InboundCardHandler
	audit       AuditLog
	executionMu sync.Mutex
	inFlight    map[string]bool
}

const maxInboundMessageAttempts = 6

func NewInboundProcessor(dataRoot string, settings Settings, message InboundMessageHandler, card InboundCardHandler) (*InboundProcessor, error) {
	config, err := NewClientConfigStore(dataRoot).Load()
	if err != nil {
		return nil, err
	}
	workbox := NewInboundWorkbox(dataRoot)
	if _, err := workbox.ScrubTerminal(); err != nil {
		return nil, err
	}
	return &InboundProcessor{inbox: NewEventInbox(dataRoot), workbox: workbox, config: config, settings: settings, message: message, card: card, audit: NewAuditLog(dataRoot), inFlight: map[string]bool{}}, nil
}
func (processor *InboundProcessor) Handle(ctx context.Context, eventKey string, payload []byte) error {
	work, err := processor.workbox.Enqueue(eventKey, payload)
	if err != nil {
		return err
	}
	if !processor.acquire(work.ID) {
		return nil
	}
	_, accepted, err := processor.inbox.Put(eventKey, payload)
	if err != nil {
		processor.release(work.ID)
		_ = processor.workbox.Complete(work.ID)
		return err
	}
	if !accepted {
		processor.release(work.ID)
		if work.Existing {
			return nil
		}
		return processor.workbox.Complete(work.ID)
	}
	go processor.processPersisted(work)
	return nil
}

func (processor *InboundProcessor) processPersisted(work inboundWork) {
	defer processor.release(work.ID)
	if work.EventKey == "card.action.trigger" {
		attempt := work.AttemptCount + 1
		_ = processor.audit.Record("inbound_execution_started", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": attempt})
		err := processor.dispatch(context.Background(), work.EventKey, work.Payload)
		if err == nil {
			_ = processor.audit.Record("inbound_execution_succeeded", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": attempt})
			_ = processor.workbox.Complete(work.ID)
			return
		}
		errorClass := inboundErrorClass(err)
		_ = processor.audit.Record("inbound_execution_failed", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": attempt, "errorClass": errorClass, "terminal": true})
		_ = processor.workbox.RecordFailure(work, errorClass, 1, true, time.Time{})
		return
	}

	delays := []time.Duration{0, 250 * time.Millisecond, time.Second}
	attempts := 0
	for index, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		attempts++
		_ = processor.audit.Record("inbound_execution_started", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": work.AttemptCount + index + 1})
		err := processor.dispatch(context.Background(), work.EventKey, work.Payload)
		if err == nil {
			_ = processor.audit.Record("inbound_execution_succeeded", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": work.AttemptCount + index + 1})
			_ = processor.workbox.Complete(work.ID)
			return
		}
		if terminalInboundError(err) {
			_ = processor.audit.Record("inbound_execution_failed", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": work.AttemptCount + index + 1, "errorClass": inboundErrorClass(err), "terminal": true})
			_ = processor.workbox.Complete(work.ID)
			return
		}
		_ = processor.audit.Record("inbound_execution_retrying", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": work.AttemptCount + index + 1, "errorClass": inboundErrorClass(err)})
	}
	terminal := work.AttemptCount+attempts >= maxInboundMessageAttempts
	_ = processor.workbox.RecordFailure(work, "temporary_failure", attempts, terminal, time.Now().UTC().Add(time.Minute))
	if terminal {
		_ = processor.audit.Record("inbound_execution_failed", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": work.AttemptCount + len(delays), "errorClass": "temporary_failure", "terminal": true})
	}
}

func terminalInboundError(err error) bool {
	if err == nil {
		return false
	}
	value := err.Error()
	return strings.HasPrefix(value, "invalid_") || strings.HasPrefix(value, "unsupported_") || strings.HasSuffix(value, "_not_authorized")
}

func inboundErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	value := err.Error()
	for _, class := range []string{
		"desktop_user_input_owner_unavailable",
		"desktop_user_input_snapshot_timeout",
		"desktop_user_input_request_stale",
		"desktop_user_input_submit_rejected",
		"desktop_user_input_not_consumed",
	} {
		if strings.HasPrefix(value, class) {
			return class
		}
	}
	for _, prefix := range []string{"invalid_", "unsupported_"} {
		if strings.HasPrefix(value, prefix) {
			return strings.Fields(value)[0]
		}
	}
	if strings.HasSuffix(value, "_not_authorized") {
		return "not_authorized"
	}
	if strings.Contains(strings.ToLower(value), "timeout") {
		return "timeout"
	}
	if strings.Contains(strings.ToLower(value), "owner") {
		return "owner_unavailable"
	}
	return "temporary_failure"
}

func (processor *InboundProcessor) Recover(ctx context.Context) error {
	items, err := processor.workbox.Ready(time.Now().UTC())
	if err != nil {
		return err
	}
	for _, work := range items {
		if !processor.acquire(work.ID) {
			continue
		}
		recoverErr := func() error {
			defer processor.release(work.ID)
			if work.EventKey == "card.action.trigger" {
				const errorClass = "stale_card_action_after_restart"
				_ = processor.audit.Record("inbound_execution_failed", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": work.AttemptCount + 1, "errorClass": errorClass, "terminal": true, "recovered": true})
				return processor.workbox.RecordFailure(work, errorClass, 1, true, time.Time{})
			}
			if _, _, err := processor.inbox.Put(work.EventKey, work.Payload); err != nil {
				return err
			}
			attempt := work.AttemptCount + 1
			_ = processor.audit.Record("inbound_execution_started", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": attempt, "recovered": true})
			err := processor.dispatch(ctx, work.EventKey, work.Payload)
			if err == nil {
				_ = processor.audit.Record("inbound_execution_succeeded", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": attempt, "recovered": true})
				return processor.workbox.Complete(work.ID)
			}
			if terminalInboundError(err) {
				_ = processor.audit.Record("inbound_execution_failed", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": attempt, "errorClass": inboundErrorClass(err), "terminal": true, "recovered": true})
				return processor.workbox.Complete(work.ID)
			}
			terminal := attempt >= maxInboundMessageAttempts
			if err := processor.workbox.RecordFailure(work, inboundErrorClass(err), 1, terminal, time.Now().UTC().Add(time.Minute)); err != nil {
				return err
			}
			direction := "inbound_execution_retrying"
			if terminal {
				direction = "inbound_execution_failed"
			}
			_ = processor.audit.Record(direction, map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": attempt, "errorClass": inboundErrorClass(err), "terminal": terminal, "recovered": true})
			return nil
		}()
		if recoverErr != nil {
			return recoverErr
		}
	}
	return nil
}

func (processor *InboundProcessor) acquire(id string) bool {
	processor.executionMu.Lock()
	defer processor.executionMu.Unlock()
	if processor.inFlight[id] {
		return false
	}
	processor.inFlight[id] = true
	return true
}

func (processor *InboundProcessor) release(id string) {
	processor.executionMu.Lock()
	delete(processor.inFlight, id)
	processor.executionMu.Unlock()
}

func (processor *InboundProcessor) dispatch(ctx context.Context, eventKey string, payload []byte) error {
	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil {
		return errors.New("invalid_inbound_event")
	}
	switch eventKey {
	case "im.message.receive_v1":
		message, err := normalizeInboundMessage(raw)
		if err != nil {
			return err
		}
		if !processor.authorize(message.ChatType, message.ChatID, message.SenderOpenID) {
			_ = processor.audit.Record("authz_denied", map[string]any{"event": AuditFingerprint(message.EventID), "actor": AuditFingerprint(message.SenderOpenID), "resource": AuditFingerprint(message.ChatID), "reason": "inbound_sender_not_authorized"})
			return errors.New("inbound_sender_not_authorized")
		}
		_ = processor.audit.Record("inbound_accepted", map[string]any{"event": AuditFingerprint(message.EventID), "actor": AuditFingerprint(message.SenderOpenID), "resource": AuditFingerprint(message.MessageID), "messageType": message.MessageType})
		if processor.message != nil {
			return processor.message(ctx, message)
		}
	case "card.action.trigger":
		action, err := normalizeInboundCard(raw)
		if err != nil {
			return err
		}
		if !processor.authorize("p2p", "", action.OperatorOpenID) {
			_ = processor.audit.Record("card_action_denied", map[string]any{"event": AuditFingerprint(action.EventID), "actor": AuditFingerprint(action.OperatorOpenID), "reason": "card_operator_not_authorized"})
			return errors.New("card_operator_not_authorized")
		}
		_ = processor.audit.Record("card_action_accepted", map[string]any{"event": AuditFingerprint(action.EventID), "actor": AuditFingerprint(action.OperatorOpenID), "resource": AuditFingerprint(action.MessageID), "action": action.Action})
		if processor.card != nil {
			return processor.card(ctx, action)
		}
	}
	return nil
}
func (processor *InboundProcessor) authorize(chatType, chatID, openID string) bool {
	allowed := map[string]bool{}
	for _, alias := range processor.config.DirectAllowedAliases {
		if target, ok := processor.config.MessageTargets[alias]; ok && target.Type == "open_id" {
			allowed[target.ID] = true
		}
	}
	if chatType == "p2p" || chatType == "direct" || chatType == "" {
		return allowed[openID]
	}
	if !processor.settings.Group.Enabled {
		return false
	}
	return csvEnvironmentAllows("FEISHU_GROUP_ALLOWED_CHAT_IDS", chatID) && csvEnvironmentAllows("FEISHU_GROUP_ALLOWED_OPEN_IDS", openID)
}
func csvEnvironmentAllows(name, value string) bool {
	items := strings.Split(os.Getenv(name), ",")
	if len(items) == 1 && strings.TrimSpace(items[0]) == "" {
		return true
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "*" || item == value {
			return true
		}
	}
	return false
}
func normalizeInboundMessage(raw map[string]any) (InboundMessage, error) {
	event := eventObject(raw)
	message, ok := event["message"].(map[string]any)
	if !ok {
		return InboundMessage{}, errors.New("inbound_message_missing")
	}
	sender, _ := event["sender"].(map[string]any)
	senderID, _ := sender["sender_id"].(map[string]any)
	content := fmt.Sprint(message["content"])
	text := ""
	var parsed map[string]any
	if json.Unmarshal([]byte(content), &parsed) == nil {
		text = fmt.Sprint(parsed["text"])
	}
	if text == "<nil>" {
		text = ""
	}
	result := InboundMessage{EventID: eventID(raw), MessageID: stringValue(message["message_id"]), RootID: stringValue(message["root_id"]), ParentID: stringValue(message["parent_id"]), ChatID: stringValue(message["chat_id"]), ChatType: stringValue(message["chat_type"]), MessageType: stringValue(message["message_type"]), Text: text, SenderOpenID: stringValue(senderID["open_id"]), Raw: event}
	if result.MessageID == "" || result.ChatID == "" || result.SenderOpenID == "" {
		return InboundMessage{}, errors.New("invalid_inbound_message")
	}
	return result, nil
}
func normalizeInboundCard(raw map[string]any) (InboundCardAction, error) {
	event := eventObject(raw)
	contextValue, _ := event["context"].(map[string]any)
	operator, _ := event["operator"].(map[string]any)
	operatorID, _ := operator["operator_id"].(map[string]any)
	action, _ := event["action"].(map[string]any)
	value := jsonObject(action["value"])
	if len(value) == 0 {
		value = jsonObject(event["action_value"])
	}
	if len(value) == 0 && stringValue(action["tag"]) == "overflow" {
		value = jsonObject(action["option"])
	}
	form := jsonObject(action["form_value"])
	if len(form) == 0 {
		form = jsonObject(event["form_value"])
	}
	openID := stringValue(operatorID["open_id"])
	if openID == "" {
		openID = stringValue(operator["open_id"])
	}
	if openID == "" {
		openID = stringValue(event["operator_id"])
	}
	result := InboundCardAction{EventID: eventID(raw), OperatorOpenID: openID, ChatID: firstString(event["chat_id"], event["open_chat_id"], contextValue["open_chat_id"]), MessageID: firstString(event["message_id"], event["open_message_id"], contextValue["open_message_id"]), Action: stringValue(value["action"]), TaskKey: stringValue(value["taskKey"]), LinkID: stringValue(value["linkId"]), QuestionRevision: stringValue(value["questionRevision"]), Token: stringValue(event["token"]), Value: value, FormValue: form, Raw: event}
	if result.OperatorOpenID == "" || result.Action == "" || stringValue(value["namespace"]) != "feishu_bridge" || numberValue(value["version"]) != 1 {
		return InboundCardAction{}, errors.New("invalid_card_action")
	}
	if strings.HasPrefix(result.Action, "task_link_") && !regexp.MustCompile(`^[a-f0-9]{20}$`).MatchString(result.TaskKey) {
		return InboundCardAction{}, errors.New("invalid_card_task_key")
	}
	if strings.HasPrefix(result.Action, "task_link_") && !regexp.MustCompile(`^LINK-[A-F0-9]{16}$`).MatchString(result.LinkID) {
		return InboundCardAction{}, errors.New("invalid_card_link")
	}
	allowed := map[string]bool{"task_link_interrupt": true, "task_link_release": true, "task_link_followup": true, "task_link_answer": true, "task_link_implement_plan": true}
	if !allowed[result.Action] {
		return InboundCardAction{}, errors.New("unsupported_card_action")
	}
	if result.Action == "task_link_followup" {
		followup := strings.TrimSpace(strings.ReplaceAll(stringValue(form["followup"]), "\x00", ""))
		if followup == "" || len([]rune(followup)) > 1000 {
			return InboundCardAction{}, errors.New("invalid_card_followup")
		}
		result.FormValue["followup"] = followup
		mode := stringValue(form["turnMode"])
		if mode != "" && mode != "default" && mode != "plan" {
			return InboundCardAction{}, errors.New("invalid_card_turn_mode")
		}
	}
	if result.Action == "task_link_implement_plan" && !regexp.MustCompile(`^[a-f0-9]{20}$`).MatchString(stringValue(value["planRevision"])) {
		return InboundCardAction{}, errors.New("invalid_plan_revision")
	}
	if result.Action == "task_link_answer" {
		if !regexp.MustCompile(`^[a-f0-9]{20}$`).MatchString(result.QuestionRevision) {
			return InboundCardAction{}, errors.New("invalid_card_question_revision")
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`).MatchString(stringValue(value["questionId"])) || strings.TrimSpace(stringValue(value["answer"])) == "" || len([]rune(stringValue(value["answer"]))) > 160 {
			return InboundCardAction{}, errors.New("invalid_card_answer")
		}
	}
	return result, nil
}

func jsonObject(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	var object map[string]any
	if json.Unmarshal([]byte(stringValue(value)), &object) == nil {
		return object
	}
	return map[string]any{}
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := stringValue(value); text != "" {
			return text
		}
	}
	return ""
}

func numberValue(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	}
	return 0
}
func eventObject(raw map[string]any) map[string]any {
	if event, ok := raw["event"].(map[string]any); ok {
		return event
	}
	if data, ok := raw["data"].(map[string]any); ok {
		if event, ok := data["event"].(map[string]any); ok {
			return event
		}
		return data
	}
	return raw
}
func eventID(raw map[string]any) string {
	if header, ok := raw["header"].(map[string]any); ok {
		return stringValue(header["event_id"])
	}
	if event := eventObject(raw); event != nil {
		if value := firstString(event["event_id"], event["eventId"]); value != "" {
			return value
		}
	}
	return stringValue(raw["event_id"])
}
func stringValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}
