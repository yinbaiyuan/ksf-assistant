package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ksfassistant/core/internal/feishuprotocol"
)

// CardProbe is opt-in diagnostics, never a task link. Nothing runs at startup.
// Each explicit step sends one latest accumulated sample, not a playback queue.
type CardProbe struct {
	root      string
	transport *ServiceTransport
	client    *OfficialMessageClient
}
type cardProbeState struct {
	ID           string             `json:"id"`
	Mode         string             `json:"mode"`
	Target       MessageTarget      `json:"target"`
	CardID       string             `json:"cardId"`
	MessageID    string             `json:"messageId"`
	Phase        string             `json:"phase"`
	Sequence     int                `json:"sequence"`
	Samples      int                `json:"samples"`
	Arrival      time.Time          `json:"arrival"`
	Submitted    time.Time          `json:"submitted"`
	Acknowledged time.Time          `json:"acknowledged"`
	LastError    string             `json:"lastError,omitempty"`
	Failure      *CLIExecutionError `json:"failure,omitempty"`
}

func NewCardProbe(root string, transport *ServiceTransport, client *OfficialMessageClient) *CardProbe {
	return &CardProbe{root, transport, client}
}

func (p *CardProbe) directory(id string) string { return filepath.Join(p.root, "card-probes-v1", id) }
func (p *CardProbe) Command(ctx context.Context, action, id, alias, mode string) (any, error) {
	if !cardKitID.MatchString(id) {
		return nil, errors.New("invalid_probe_id")
	}
	dir := p.directory(id)
	if action == "status" {
		var state cardProbeState
		if missing, err := readPrivateJSON(filepath.Join(dir, "state.json"), &state); err != nil || missing {
			if err == nil {
				err = errors.New("probe_not_found")
			}
			return nil, err
		}
		var control map[string]any
		_, _ = readPrivateJSON(filepath.Join(dir, "control.json"), &control)
		return map[string]any{"id": state.ID, "mode": state.Mode, "phase": state.Phase, "sequence": state.Sequence, "samples": state.Samples, "arrival": state.Arrival, "submitted": state.Submitted, "acknowledged": state.Acknowledged, "lastError": state.LastError, "failure": state.Failure, "control": control}, nil
	}
	if p.transport == nil || p.client == nil {
		return nil, errors.New("feishu_transport_unavailable")
	}
	if action != "start" && action != "step" && action != "progress" && action != "finish" {
		return nil, errors.New("invalid_probe_action")
	}
	if err := ensurePrivateDirectory(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	var result any
	err := withProcessFileLock(dir+".lock", func() error {
		path := filepath.Join(dir, "state.json")
		var state cardProbeState
		missing, readErr := readPrivateJSON(path, &state)
		if action == "start" {
			if !missing && readErr == nil {
				return errors.New("probe_already_exists_no_automatic_resend")
			}
			if readErr != nil {
				return readErr
			}
			if mode != "native" && mode != "component" && mode != "formal" && mode != "formal-idle" {
				return errors.New("invalid_probe_mode")
			}
			config, err := NewClientConfigStore(p.root).Load()
			if err != nil {
				return err
			}
			target, ok := config.MessageTargets[alias]
			if !ok || target.Type != "open_id" {
				return errors.New("probe_requires_authorized_direct_target")
			}
			if err := p.transport.gate(target); err != nil {
				return err
			}
			state = cardProbeState{ID: id, Mode: mode, Target: target, Phase: "create_outcome_unknown", Arrival: time.Now().UTC()}
			// Persist uncertainty BEFORE creating. CardKit create has no UUID field.
			if err := writePrivateJSON(path, state); err != nil {
				return err
			}
			if mode == "formal" || mode == "formal-idle" {
				data, _ := json.Marshal(formalProbeJSON(id, 0, mode == "formal-idle"))
				sent, err := p.transport.Message(ctx, false, feishuprotocol.MessageRequest{TargetType: target.Type, TargetID: target.ID, Format: "card", Content: string(data), IdempotencyKey: "card-probe-" + id})
				if err != nil {
					return err
				}
				state.MessageID, state.Phase = sent.MessageID, "ready"
				if err := writePrivateJSON(path, state); err != nil {
					return err
				}
				result = map[string]any{"id": id, "phase": "ready", "mode": mode}
				return nil
			}
			data, _ := json.Marshal(cardProbeJSON(id, mode == "native"))
			created, err := p.client.client.CallMessage(ctx, MessageCLIRequest{Resource: "cardkit", Method: "create", Body: map[string]any{"type": "card_json", "data": string(data)}})
			if err != nil {
				return err
			}
			state.CardID, _ = created["card_id"].(string)
			if !cardKitID.MatchString(state.CardID) {
				return errors.New("cardkit_missing_card_id")
			}
			state.Phase = "send_outcome_unknown"
			if err := writePrivateJSON(path, state); err != nil {
				return err
			}
			reference, _ := json.Marshal(map[string]any{"type": "card", "data": map[string]string{"card_id": state.CardID}})
			sent, err := p.transport.Message(ctx, false, feishuprotocol.MessageRequest{TargetType: target.Type, TargetID: target.ID, Format: "card", Content: string(reference), IdempotencyKey: "card-probe-" + id})
			if err != nil {
				return err
			}
			state.MessageID, state.Phase = sent.MessageID, "ready"
			if err := writePrivateJSON(path, state); err != nil {
				return err
			}
			result = map[string]any{"id": id, "phase": "ready", "mode": mode}
			return nil
		}
		if readErr != nil {
			return readErr
		}
		if missing {
			return errors.New("probe_not_found")
		}
		// A newer settings-only close supersedes an uncertain body update. It
		// never resends content or creates another card.
		if state.Phase != "ready" && !(action == "finish" && state.Phase == "update_outcome_unknown" && state.CardID != "" && state.MessageID != "") {
			return errors.New("probe_not_ready_no_automatic_replay")
		}
		if err := p.transport.gate(state.Target); err != nil {
			return err
		}
		binding, err := p.transport.readBinding(state.MessageID)
		if err != nil || !binding.Writable || binding.Target != state.Target {
			return errors.New("probe_binding_mismatch")
		}
		if _, err := os.Stat(filepath.Join(dir, "stop.json")); err == nil {
			action = "finish"
		}
		if state.Samples >= 120 {
			action = "finish"
		}
		previous := state
		state.Arrival = time.Now().UTC()
		state.Sequence++
		request := MessageCLIRequest{Resource: "cardkit", Params: map[string]string{"card_id": state.CardID}, Body: map[string]any{"uuid": fmt.Sprintf("%s-%d", id, state.Sequence), "sequence": state.Sequence}}
		if action == "finish" {
			request.Method = "settings"
			request.Body["settings"] = `{"config":{"streaming_mode":false}}`
		} else {
			state.Samples++
			request.Params["element_id"] = "probe_body"
			content := "**单卡交互验收**\n请在正文更新时输入中文、移动光标并提交；停止只影响此测试。\n\n" + strings.Repeat("持续输出测试。", state.Samples)
			if action == "progress" {
				state.Samples--
				request.Params["element_id"] = "probe_progress"
				content = fmt.Sprintf("<font color='grey'>组件更新验收 · 第 %d 次更新</font>", state.Sequence)
			}
			request.Method = "content"
			request.Body["content"] = content
			if state.Mode == "component" {
				request.Method = "patch"
				delete(request.Body, "content")
				partial, _ := json.Marshal(map[string]string{"content": content})
				request.Body["partial_element"] = string(partial)
			}
		}
		state.Phase, state.Submitted = "update_outcome_unknown", time.Now().UTC()
		if err := writePrivateJSON(path, state); err != nil {
			return err
		}
		if state.Mode == "formal" || state.Mode == "formal-idle" {
			data, _ := json.Marshal(formalProbeJSON(id, state.Samples, action == "finish"))
			err = p.transport.Patch(ctx, feishuprotocol.CardRequest{MessageID: state.MessageID, Content: string(data)})
		} else {
			_, err = p.client.client.CallMessage(ctx, request)
		}
		if err != nil {
			state.LastError = "cardkit_update_unconfirmed"
			var failure *CLIExecutionError
			if errors.As(err, &failure) {
				state.Failure = failure // structured fields already sanitized; Cause is never serialized.
			}
			// Only positively identified pre-send failures may restore the prior
			// projection. Generic errors remain unknown; never infer no side effect.
			var rejected *UserApprovalError
			if errors.As(err, &rejected) || failure != nil && !failure.Started {
				state = previous
				state.LastError = CapabilityOperationErrorCode(err)
				state.Failure = failure
			}
			_ = writePrivateJSON(path, state)
			return err
		}
		state.Phase, state.Acknowledged = "ready", time.Now().UTC()
		state.LastError, state.Failure = "", nil
		if action == "finish" {
			state.Phase = "finished"
		}
		if err := writePrivateJSON(path, state); err != nil {
			return err
		}
		result = map[string]any{"id": id, "phase": state.Phase, "sequence": state.Sequence, "queueMs": state.Submitted.Sub(state.Arrival).Milliseconds(), "apiMs": state.Acknowledged.Sub(state.Submitted).Milliseconds()}
		return nil
	})
	return result, err
}

// Exercise the same entity journal and governed transport as new task links.
// Only synthetic, fixed content is accepted; real task identities stay untouched.
func formalProbeJSON(id string, samples int, finished bool) map[string]any {
	card := cardProbeJSON(id, !finished)
	phase := "running"
	if finished {
		phase = "completed"
	}
	card["ksf_cardkit"] = map[string]any{"phase": phase, "streaming": !finished}
	body := card["body"].(map[string]any)
	elements := body["elements"].([]any)
	elements[1].(map[string]any)["element_id"] = "activity"
	elements[0].(map[string]any)["content"] = "**正式链路隔离验收**\n" + strings.Repeat("正文持续增长。", samples+1)
	if samples >= 2 {
		second := map[string]any{"tag": "markdown", "element_id": "second_message", "content": "新消息组件。" + strings.Repeat("新增内容。", samples-1)}
		elements = append([]any{elements[0], second}, elements[1:]...)
	}
	if samples >= 6 {
		elements = elements[1:]
	}
	body["elements"] = elements
	return card
}

// Callback receipt does not acquire the network writer lock. Stop is durable
// immediately; the next explicit step closes streaming instead of sending text.
func (p *CardProbe) HandleCard(card InboundCardAction) (bool, error) {
	id, _ := card.Value["probeId"].(string)
	action, _ := card.Value["action"].(string)
	if id == "" {
		name := stringValue(jsonObject(card.Raw["action"])["name"])
		if strings.HasPrefix(name, "probe_submit_") {
			id, action = strings.TrimPrefix(name, "probe_submit_"), "submit"
		}
	}
	if id == "" {
		return false, nil
	}
	if !cardKitID.MatchString(id) {
		return true, errors.New("invalid_probe_id")
	}
	var state cardProbeState
	if missing, err := readPrivateJSON(filepath.Join(p.directory(id), "state.json"), &state); err != nil || missing {
		if err == nil {
			err = errors.New("probe_not_found")
		}
		return true, err
	}
	if state.MessageID != card.MessageID || state.Target.ID != card.OperatorOpenID || state.Target.Type != "open_id" {
		return true, errors.New("probe_callback_binding_mismatch")
	}
	if err := p.transport.gate(state.Target); err != nil {
		return true, err
	}
	if action != "stop" && action != "submit" {
		return true, errors.New("invalid_probe_callback")
	}
	text, _ := card.FormValue["probe_input"].(string)
	if utf8.RuneCountInString(text) > 1000 {
		return true, errors.New("probe_input_too_long")
	}
	receipt := map[string]any{"action": action, "received": time.Now().UTC(), "input": text}
	if action == "stop" {
		if err := writePrivateJSON(filepath.Join(p.directory(id), "stop.json"), receipt); err != nil {
			return true, err
		}
	}
	return true, writePrivateJSON(filepath.Join(p.directory(id), "control.json"), receipt)
}

func cardProbeJSON(id string, native bool) map[string]any {
	plain := func(text string) map[string]any { return map[string]any{"tag": "plain_text", "content": text} }
	return map[string]any{"schema": "2.0", "config": map[string]any{"update_multi": true, "width_mode": "fill", "streaming_mode": native}, "header": map[string]any{"title": plain("KSFAssistant · 隔离验收"), "subtitle": plain("仅测试卡片交互，不连接真实任务"), "template": "blue"}, "body": map[string]any{"direction": "vertical", "vertical_spacing": "12px", "elements": []any{
		map[string]any{"tag": "markdown", "element_id": "probe_body", "content": "**单卡交互验收**\n等待开始更新。"},
		map[string]any{"tag": "markdown", "element_id": "probe_progress", "text_size": "notation", "content": "<font color='grey'>请测试中文输入、光标移动、提交和停止</font>"},
		map[string]any{"tag": "form", "name": "probe_form", "element_id": "probe_form", "elements": []any{
			map[string]any{"tag": "column_set", "flex_mode": "none", "horizontal_spacing": "8px", "columns": []any{
				map[string]any{"tag": "column", "width": "auto", "vertical_align": "bottom", "elements": []any{map[string]any{"tag": "button", "name": "probe_stop", "element_id": "probe_stop", "text": plain("停止"), "type": "danger", "behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{"probeId": id, "action": "stop"}}}}}},
				map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{map[string]any{"tag": "input", "element_id": "probe_input", "name": "probe_input", "required": true, "input_type": "text", "width": "fill", "max_length": 1000, "placeholder": plain("补充或修正（仅测试文字）")}}},
				map[string]any{"tag": "column", "width": "auto", "vertical_align": "bottom", "elements": []any{map[string]any{"tag": "button", "name": "probe_submit_" + id, "text": plain("发送"), "type": "primary_filled", "form_action_type": "submit", "behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{"probeId": id, "action": "submit"}}}}}},
			}},
		}},
	}}}
}
