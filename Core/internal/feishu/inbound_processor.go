package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"ksfassistant/core/internal/feishutypes"
	"os"
	"strings"
	"sync"
	"time"
)

type InboundMessage = feishutypes.InboundMessage
type InboundCardAction = feishutypes.InboundCardAction
type InboundMessageHandler func(context.Context, InboundMessage) error
type InboundCardHandler func(context.Context, InboundCardAction) error

const inboundDeliveryWorkers = 4
const inboundDeliveryBuffer = 32

type inboundDeliveryJob struct {
	work inboundWork
	done chan struct{}
}

type terminalDeliveryError struct{ error }

type InboundProcessor struct {
	dataRoot    string
	inbox       *EventInbox
	workbox     *InboundWorkbox
	message     InboundMessageHandler
	card        InboundCardHandler
	audit       AuditLog
	ctx         context.Context
	cancel      context.CancelFunc
	workers     sync.WaitGroup
	ingestMu    sync.Mutex
	executionMu sync.Mutex
	inFlight    map[string]bool
	closed      bool
	jobs        [inboundDeliveryWorkers]chan inboundDeliveryJob
}

func NewInboundProcessor(dataRoot string, _ Settings, message InboundMessageHandler, card InboundCardHandler) (*InboundProcessor, error) {
	if _, err := NewClientConfigStore(dataRoot).Load(); err != nil {
		return nil, err
	}
	workbox := NewInboundWorkbox(dataRoot)
	if _, err := workbox.ScrubTerminal(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	processor := &InboundProcessor{dataRoot: dataRoot, inbox: NewEventInbox(dataRoot), workbox: workbox, message: message, card: card, audit: NewAuditLog(dataRoot), ctx: ctx, cancel: cancel, inFlight: map[string]bool{}}
	for partition := range processor.jobs {
		processor.jobs[partition] = make(chan inboundDeliveryJob, inboundDeliveryBuffer)
		processor.workers.Add(1)
		go processor.runDeliveryWorker(partition)
	}
	return processor, nil
}

func (processor *InboundProcessor) Close() {
	processor.executionMu.Lock()
	processor.closed = true
	processor.cancel()
	processor.executionMu.Unlock()
	processor.workers.Wait()
}

func (processor *InboundProcessor) Handle(ctx context.Context, eventKey string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := processor.ctx.Err(); err != nil {
		return err
	}
	processor.ingestMu.Lock()
	defer processor.ingestMu.Unlock()
	work, err := processor.workbox.Enqueue(eventKey, payload)
	if err != nil {
		return err
	}
	if _, _, err := processor.inbox.Put(eventKey, payload); err != nil {
		return err
	}
	if work.Existing {
		return nil
	}
	processor.schedule(work)
	return nil
}

func (processor *InboundProcessor) schedule(work inboundWork) <-chan struct{} {
	processor.executionMu.Lock()
	defer processor.executionMu.Unlock()
	if processor.closed || processor.inFlight[work.ID] {
		return nil
	}
	var raw map[string]any
	_ = json.Unmarshal(work.Payload, &raw)
	conversation := firstNestedValue(raw, []string{"chat_id", "open_chat_id"})
	if conversation == "" {
		conversation = firstNestedValue(raw, []string{"open_id", "event_id", "message_id"})
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(conversation))
	partition := int(hash.Sum32() % inboundDeliveryWorkers)
	job := inboundDeliveryJob{work: work, done: make(chan struct{})}
	processor.inFlight[work.ID] = true
	select {
	case processor.jobs[partition] <- job:
		return job.done
	default:
		delete(processor.inFlight, work.ID)
		return nil
	}
}

func (processor *InboundProcessor) runDeliveryWorker(partition int) {
	defer processor.workers.Done()
	for {
		select {
		case <-processor.ctx.Done():
			return
		case job := <-processor.jobs[partition]:
			if processor.ctx.Err() == nil {
				processor.processPersisted(job.work)
			}
			processor.executionMu.Lock()
			delete(processor.inFlight, job.work.ID)
			processor.executionMu.Unlock()
			close(job.done)
		}
	}
}

func (processor *InboundProcessor) processPersisted(work inboundWork) {
	delays := []time.Duration{0, 250 * time.Millisecond, time.Second}
	for index, delay := range delays {
		if processor.ctx.Err() != nil {
			return
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-processor.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		ctx, cancel := context.WithTimeout(processor.ctx, 15*time.Second)
		err := processor.dispatch(ctx, work.EventKey, work.Payload)
		cancel()
		if err == nil {
			if err := processor.workbox.Complete(work.ID); err == nil {
				_ = processor.audit.Record("inbound_delivery_accepted", map[string]any{"event": work.ID, "eventKey": work.EventKey, "attempt": work.AttemptCount + index + 1})
				return
			}
		}
		if processor.ctx.Err() != nil {
			return
		}
		var terminal terminalDeliveryError
		if errors.As(err, &terminal) {
			_ = processor.workbox.RecordFailure(work, "delivery_not_authorized_or_invalid", index+1, true, time.Time{})
			return
		}
	}
	_ = processor.workbox.RecordFailure(work, "core_ack_unavailable", len(delays), false, time.Now().UTC().Add(time.Minute))
	_ = processor.audit.Record("inbound_delivery_deferred", map[string]any{"event": work.ID, "eventKey": work.EventKey})
}

func (processor *InboundProcessor) Recover(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := processor.ctx.Err(); err != nil {
		return err
	}
	items, err := processor.workbox.Ready(time.Now().UTC())
	if err != nil {
		return err
	}
	waiting := []<-chan struct{}{}
	for _, work := range items {
		if done := processor.schedule(work); done != nil {
			waiting = append(waiting, done)
		}
	}
	for _, done := range waiting {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-processor.ctx.Done():
			return processor.ctx.Err()
		case <-done:
		}
	}
	return nil
}

func (processor *InboundProcessor) dispatch(ctx context.Context, eventKey string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := NewClientConfigStore(processor.dataRoot).Load()
	if err != nil {
		return err
	}
	settings, err := NewSettingsStore(processor.dataRoot).Load()
	if err != nil {
		return err
	}

	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil {
		return terminalDeliveryError{errors.New("invalid_inbound_event")}
	}
	switch eventKey {
	case "im.message.receive_v1":
		message, err := normalizeInboundMessage(raw)
		if err != nil {
			return terminalDeliveryError{err}
		}
		if !authorizeInbound(config, settings, message.ChatType, message.ChatID, message.SenderOpenID) {
			_ = processor.audit.Record("authz_denied", map[string]any{"event": AuditFingerprint(message.EventID), "actor": AuditFingerprint(message.SenderOpenID), "resource": AuditFingerprint(message.ChatID), "reason": "inbound_sender_not_authorized"})
			return terminalDeliveryError{errors.New("inbound_sender_not_authorized")}
		}
		_ = processor.audit.Record("inbound_accepted", map[string]any{"event": AuditFingerprint(message.EventID), "actor": AuditFingerprint(message.SenderOpenID), "resource": AuditFingerprint(message.MessageID), "messageType": message.MessageType})
		if processor.message == nil {
			return errors.New("Core delivery handler unavailable")
		}
		return processor.message(ctx, message)
	case "card.action.trigger":
		action, err := normalizeInboundCard(raw)
		if err != nil {
			return terminalDeliveryError{err}
		}
		if !authorizeInbound(config, settings, "p2p", "", action.OperatorOpenID) {
			_ = processor.audit.Record("card_action_denied", map[string]any{"event": AuditFingerprint(action.EventID), "actor": AuditFingerprint(action.OperatorOpenID), "reason": "card_operator_not_authorized"})
			return terminalDeliveryError{errors.New("card_operator_not_authorized")}
		}
		_ = processor.audit.Record("card_action_accepted", map[string]any{"event": AuditFingerprint(action.EventID), "actor": AuditFingerprint(action.OperatorOpenID), "resource": AuditFingerprint(action.MessageID), "action": action.Action})
		if processor.card == nil {
			return errors.New("Core delivery handler unavailable")
		}
		return processor.card(ctx, action)
	}
	return nil
}
func authorizeInbound(config ClientConfig, settings Settings, chatType, chatID, openID string) bool {
	allowed := map[string]bool{}
	for _, alias := range config.DirectAllowedAliases {
		if target, ok := config.MessageTargets[alias]; ok && target.Type == "open_id" {
			allowed[target.ID] = true
		}
	}
	if chatType == "p2p" || chatType == "direct" || chatType == "" {
		return allowed[openID]
	}
	if !settings.Group.Enabled {
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
	result := InboundCardAction{EventID: eventID(raw), OperatorOpenID: openID, ChatID: firstString(event["chat_id"], event["open_chat_id"], contextValue["open_chat_id"]), MessageID: firstString(event["message_id"], event["open_message_id"], contextValue["open_message_id"]), Token: stringValue(event["token"]), Value: value, FormValue: form, Raw: event}
	if result.OperatorOpenID == "" || result.MessageID == "" {
		return InboundCardAction{}, errors.New("invalid_card_action")
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
