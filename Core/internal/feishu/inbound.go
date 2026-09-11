package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

var FixedEventKeys = []string{
	"application.bot.menu_v6",
	"approval.instance.status_changed_v4",
	"approval.task.status_changed_v4",
	"board.whiteboard.updated_v1",
	"card.action.trigger",
	"im.chat.disbanded_v1",
	"im.chat.member.bot.added_v1",
	"im.chat.member.bot.deleted_v1",
	"im.chat.member.user.added_v1",
	"im.chat.member.user.deleted_v1",
	"im.chat.member.user.withdrawn_v1",
	"im.chat.updated_v1",
	"im.message.message_read_v1",
	"im.message.reaction.created_v1",
	"im.message.reaction.deleted_v1",
	"im.message.receive_v1",
	"minutes.minute.generated_v1",
	"mail.user_mailbox.event.message_received_v1",
	"task.task.update_user_access_v2",
	"vc.meeting.participant_meeting_ended_v1",
	"vc.meeting.participant_meeting_joined_v1",
	"vc.meeting.participant_meeting_started_v1",
	"vc.note.generated_v1",
	"vc.recording.recording_ended_v1",
	"vc.recording.recording_started_v1",
	"vc.recording.recording_transcript_generated_v1",
}

const MailMessageReceivedEvent = "mail.user_mailbox.event.message_received_v1"
const ApprovalInstanceStatusChangedEvent = "approval.instance.status_changed_v4"
const ApprovalTaskStatusChangedEvent = "approval.task.status_changed_v4"

type EventSink func(context.Context, string, []byte) error
type ConnectionObserver func(string)

type cliEventNormalizationError struct{ code string }

func (err *cliEventNormalizationError) Error() string { return err.code }

func rejectCLIEvent(code string) error { return &cliEventNormalizationError{code: code} }

type OfficialInbound struct {
	runner   CapabilityExecutor
	messages *OfficialMessageClient
	sink     EventSink
	observer ConnectionObserver
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewOfficialInbound(runner CapabilityExecutor, messages *OfficialMessageClient, sink EventSink, observer ConnectionObserver) (*OfficialInbound, error) {
	if runner.Binary == "" || sink == nil {
		return nil, errors.New("official CLI runner and event sink are required")
	}
	if observer == nil {
		observer = func(string) {}
	}
	return &OfficialInbound{runner: runner, messages: messages, sink: sink, observer: observer}, nil
}

func (inbound *OfficialInbound) Start(ctx context.Context) error {
	inbound.mu.Lock()
	if inbound.cancel != nil {
		inbound.mu.Unlock()
		return errors.New("official CLI consumer is already running")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	inbound.cancel = cancel
	inbound.done = done
	inbound.mu.Unlock()
	defer func() { cancel(); inbound.mu.Lock(); inbound.cancel = nil; close(done); inbound.mu.Unlock() }()
	// Each attempt joins and cleans up only its owned consumers before retrying.
	// Never take over a foreign bus or replay malformed/unpersisted events.
	delays := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}
	for attempt := 0; ; attempt++ {
		inbound.observer("starting")
		err := inbound.runConsumers(runCtx)
		if runCtx.Err() != nil || err == nil {
			inbound.observer("disconnected")
			return err
		}
		code := diagnosticToken(err.Error(), "cli_event_runtime_failed")
		_ = NewDiagnosticLog(inbound.runner.DataRoot).Record(SupervisorDiagnostic{Code: code, Component: "event-consumers", SafeSummary: code})
		if attempt >= len(delays) || !retryableConsumerFailure(err) {
			inbound.observer("failed")
			return err
		}
		inbound.observer("reconnecting")
		timer := time.NewTimer(delays[attempt])
		select {
		case <-runCtx.Done():
			timer.Stop()
			inbound.observer("disconnected")
			return runCtx.Err()
		case <-timer.C:
		}
	}
}

func retryableConsumerFailure(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch err.Error() {
	case "cli_event_consumer_failed", "cli_event_consumer_exited", "cli_event_ready_timeout", "cli_event_status_unavailable":
		return true
	}
	return false
}

func (inbound *OfficialInbound) Close() {
	inbound.mu.Lock()
	cancel, done := inbound.cancel, inbound.done
	inbound.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (inbound *OfficialInbound) HandlePayload(ctx context.Context, payload []byte) error {
	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil {
		return errors.New("invalid_event_payload")
	}
	header, _ := raw["header"].(map[string]any)
	key := stringValue(header["event_type"])
	if !contains(FixedEventKeys, key) {
		return errors.New("unsupported_event_key")
	}
	return inbound.sink(ctx, key, append([]byte(nil), payload...))
}

func (inbound *OfficialInbound) HandleCLIEvent(ctx context.Context, key string, payload []byte) error {
	if !contains(FixedEventKeys, key) {
		return rejectCLIEvent("unsupported_event_key")
	}
	var raw map[string]any
	if json.Unmarshal(payload, &raw) != nil {
		return rejectCLIEvent("invalid_cli_event")
	}
	if raw["type"] != key {
		return rejectCLIEvent("cli_event_type_mismatch")
	}
	if key != "im.message.receive_v1" && key != "card.action.trigger" {
		return inbound.sink(ctx, key, payload)
	}
	identity := stringValue(raw["event_id"])
	if identity == "" {
		return rejectCLIEvent("cli_event_identity_missing")
	}
	event := map[string]any{}
	if key == "im.message.receive_v1" {
		messageID, chatID, senderID := stringValue(raw["message_id"]), stringValue(raw["chat_id"]), stringValue(raw["sender_id"])
		if messageID == "" || chatID == "" || senderID == "" {
			return rejectCLIEvent("cli_message_binding_missing")
		}
		messageType := stringValue(raw["message_type"])
		content := ""
		if messageType == "text" {
			body, _ := json.Marshal(map[string]string{"text": stringValue(raw["content"])})
			content = string(body)
		} else {
			if inbound.messages == nil {
				return errors.New("cli_original_message_required")
			}
			original, err := inbound.messages.ReadMessage(ctx, messageID)
			if err != nil {
				return err
			}
			sender, _ := original["sender"].(map[string]any)
			if original["chat_id"] != chatID || original["msg_type"] != messageType || sender["id"] != senderID || sender["id_type"] != "open_id" {
				return rejectCLIEvent("cli_original_message_binding_mismatch")
			}
			body, _ := original["body"].(map[string]any)
			content = stringValue(body["content"])
			if content == "" {
				return rejectCLIEvent("cli_original_message_content_missing")
			}
		}
		mentions := []any{}
		if entries, ok := raw["mentions"].([]any); ok {
			for _, entry := range entries {
				mention, ok := entry.(map[string]any)
				if !ok {
					return rejectCLIEvent("cli_mention_invalid")
				}
				mentions = append(mentions, map[string]any{"key": mention["key"], "name": mention["name"], "id": map[string]any{"open_id": mention["id"]}})
			}
		}
		event["sender"] = map[string]any{"sender_type": raw["sender_type"], "sender_id": map[string]any{"open_id": senderID}}
		event["message"] = map[string]any{"message_id": messageID, "chat_id": chatID, "chat_type": raw["chat_type"], "message_type": messageType, "content": content, "root_id": raw["root_id"], "parent_id": raw["reply_to"], "thread_id": raw["thread_id"], "mentions": mentions}
	} else {
		if stringValue(raw["message_id"]) == "" || stringValue(raw["operator_id"]) == "" {
			return rejectCLIEvent("cli_card_binding_missing")
		}
		value := jsonObject(raw["action_value"])
		if len(value) == 0 && raw["action_tag"] == "overflow" {
			value = jsonObject(raw["option"])
		}
		if len(value) == 0 {
			return rejectCLIEvent("cli_card_action_value_missing")
		}
		form := map[string]any{}
		if text := stringValue(raw["form_value"]); text != "" && (json.Unmarshal([]byte(text), &form) != nil || form == nil) {
			return rejectCLIEvent("cli_card_form_invalid")
		}
		action := map[string]any{"tag": raw["action_tag"], "name": raw["action_name"], "value": value, "form_value": form, "input_value": raw["input_value"], "option": raw["option"], "checked": raw["checked"]}
		if options := stringValue(raw["options"]); options != "" {
			action["options"] = strings.Split(options, ",")
		}
		event["action"] = action
		event["operator"] = map[string]any{"open_id": raw["operator_id"]}
		event["context"] = map[string]any{"open_message_id": raw["message_id"], "open_chat_id": raw["chat_id"]}
	}
	normalized, err := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"event_type": key, "event_id": identity, "create_time": raw["timestamp"]}, "event": event})
	if err != nil {
		return err
	}
	return inbound.sink(ctx, key, normalized)
}
