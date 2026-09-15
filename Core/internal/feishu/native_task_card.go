package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"time"
)

// Native cards keep the existing governed message transport. This journal owns
// only the CardKit entity, its monotonic sequence and an exact pending operation.
// It never owns credentials, recipient authorization or Codex task control.
type nativeProjection struct {
	Phase     string         `json:"phase"`
	Streaming bool           `json:"streaming"`
	Card      map[string]any `json:"card"`
}
type nativeCardState struct {
	StandardTextUpdates  bool              `json:"standardTextUpdates,omitempty"`
	AppID                string            `json:"appId"`
	Target               MessageTarget     `json:"target"`
	Key                  string            `json:"key"`
	Initial              string            `json:"initial"`
	CardID               string            `json:"cardId"`
	MessageID            string            `json:"messageId"`
	Creating             bool              `json:"creating"`
	Sequence             int               `json:"sequence"`
	Projection           nativeProjection  `json:"projection"`
	Texts                map[string]string `json:"texts"`
	Pending              *nativeCardWrite  `json:"pending,omitempty"`
	Submitted            time.Time         `json:"submitted,omitempty"`
	Acknowledged         time.Time         `json:"acknowledged,omitempty"`
	ActivityAcknowledged time.Time         `json:"activityAcknowledged,omitempty"`
}
type nativeCardWrite struct {
	SequenceRebased bool              `json:"sequenceRebased,omitempty"`
	Request         MessageCLIRequest `json:"request"`
	ID              string            `json:"id,omitempty"`
	Text            string            `json:"text,omitempty"`
	Remove          bool              `json:"remove,omitempty"`
	Replacement     *nativeProjection `json:"replacement,omitempty"`
	Streaming       *bool             `json:"streaming,omitempty"`
}

func parseNativeProjection(raw string) (nativeProjection, bool, error) {
	var card map[string]any
	if json.Unmarshal([]byte(raw), &card) != nil {
		return nativeProjection{}, false, nil
	}
	marker, present := card["ksf_cardkit"]
	if !present {
		return nativeProjection{}, false, nil
	}
	data, _ := json.Marshal(marker)
	var p nativeProjection
	if json.Unmarshal(data, &p) != nil || p.Phase == "" || len(p.Phase) > 512 || card["schema"] != "2.0" {
		return p, true, errors.New("invalid_native_task_card")
	}
	delete(card, "ksf_cardkit")
	p.Card = card
	body, ok := card["body"].(map[string]any)
	if !ok {
		return p, true, errors.New("invalid_native_task_card")
	}
	elements, ok := body["elements"].([]any)
	if !ok || len(elements) == 0 || len(raw) > 30000 {
		return p, true, errors.New("invalid_native_task_card")
	}
	ids := map[string]bool{}
	for _, rawElement := range elements {
		e, ok := rawElement.(map[string]any)
		if !ok {
			return p, true, errors.New("invalid_native_task_card")
		}
		if value, exists := e["element_id"]; exists {
			id, valid := value.(string)
			if !valid || !cardKitID.MatchString(id) || ids[id] {
				return p, true, errors.New("invalid_native_task_card")
			}
			ids[id] = true
			if e["tag"] == "markdown" {
				if _, valid := e["content"].(string); !valid {
					return p, true, errors.New("invalid_native_task_card")
				}
			}
		}
	}
	if !ids["activity"] {
		return p, true, errors.New("invalid_native_task_card")
	}
	config, ok := card["config"].(map[string]any)
	if !ok || config["streaming_mode"] != p.Streaming {
		return p, true, errors.New("invalid_native_task_card")
	}
	return p, true, nil
}
func nativeElements(p nativeProjection) []map[string]any {
	body, _ := p.Card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	result := []map[string]any{}
	for _, raw := range elements {
		e, _ := raw.(map[string]any)
		if e["tag"] == "markdown" && e["element_id"] != nil {
			result = append(result, e)
		}
	}
	return result
}
func nativeTexts(p nativeProjection) map[string]string {
	texts := map[string]string{}
	for _, e := range nativeElements(p) {
		id, _ := e["element_id"].(string)
		text, _ := e["content"].(string)
		texts[id] = text
	}
	return texts
}
func (c *OfficialMessageClient) nativeKeyPath(key string) string {
	return filepath.Join(c.nativeRoot, "native-cards-v1", "keys", secretHash(key)+".json")
}
func (c *OfficialMessageClient) nativeMessagePath(id string) string {
	return filepath.Join(c.nativeRoot, "native-cards-v1", "messages", secretHash(id)+".json")
}

func (c *OfficialMessageClient) sendNative(ctx context.Context, target MessageTarget, p nativeProjection, key string) (string, error) {
	if c.nativeRoot == "" || key == "" {
		return "", errors.New("native_card_store_unavailable")
	}
	path := c.nativeKeyPath(key)
	initial, _ := json.Marshal(p)
	var messageID string
	err := withProcessFileLock(path+".lock", func() error {
		var state nativeCardState
		missing, err := readPrivateJSON(path, &state)
		if err != nil {
			return err
		}
		if missing {
			state = nativeCardState{AppID: c.appID, Target: target, Key: key, Initial: secretHash(string(initial)), Projection: p, Creating: true, Texts: nativeTexts(p)}
			if err = writePrivateJSON(path, state); err != nil {
				return err
			}
			data, _ := json.Marshal(p.Card)
			result, err := c.client.CallMessage(ctx, MessageCLIRequest{Resource: "cardkit", Method: "create", Body: map[string]any{"type": "card_json", "data": string(data)}})
			if err != nil {
				return err
			}
			state.CardID, _ = result["card_id"].(string)
			if !cardKitID.MatchString(state.CardID) {
				return errors.New("cardkit_missing_card_id")
			}
			state.Creating = false
			if err = writePrivateJSON(path, state); err != nil {
				return err
			}
		}
		if state.AppID != c.appID || state.Target != target || state.Initial != secretHash(string(initial)) {
			return ErrOperationRequestMismatch
		}
		if state.Creating || state.CardID == "" {
			return errors.New("native_card_create_outcome_unknown")
		}
		if state.MessageID == "" {
			reference, _ := json.Marshal(map[string]any{"type": "card", "data": map[string]string{"card_id": state.CardID}})
			result, err := c.client.CallMessage(ctx, MessageCLIRequest{Resource: "messages", Method: "create", Params: map[string]string{"receive_id_type": target.Type}, Body: map[string]any{"receive_id": target.ID, "msg_type": "interactive", "content": string(reference), "uuid": key}})
			state.MessageID, err = messageResultID(result, err)
			if err != nil {
				return err
			}
			if err = writePrivateJSON(path, state); err != nil {
				return err
			}
		}
		messageID = state.MessageID
		return writePrivateJSON(c.nativeMessagePath(messageID), map[string]string{"key": key})
	})
	return messageID, err
}

func (c *OfficialMessageClient) patchNative(ctx context.Context, id, raw string) (bool, error) {
	if c.nativeRoot == "" {
		return false, nil
	}
	var binding map[string]string
	missing, err := readPrivateJSON(c.nativeMessagePath(id), &binding)
	if err != nil {
		return true, err
	}
	if missing {
		if _, native, parseErr := parseNativeProjection(raw); native {
			if parseErr != nil {
				return true, parseErr
			}
			return true, errors.New("native_card_binding_missing")
		}
		return false, nil
	}
	p, present, err := parseNativeProjection(raw)
	if err != nil {
		return true, err
	}
	if !present {
		return true, errors.New("native_card_projection_required")
	}
	path := c.nativeKeyPath(binding["key"])
	err = withProcessFileLock(path+".lock", func() error {
		var state nativeCardState
		missing, err := readPrivateJSON(path, &state)
		if err != nil {
			return err
		}
		if missing || state.AppID != c.appID || state.MessageID != id {
			return ErrOperationRequestMismatch
		}
		// Recover only the exact persisted UUID/sequence. Never issue a new identity
		// for an uncertain write, and never recreate a message here.
		if state.Pending != nil {
			if err = c.applyNativeWrite(ctx, path, &state); err != nil {
				return err
			}
		}
		if reflect.DeepEqual(state.Projection, p) {
			return nil
		}
		if state.Projection.Streaming {
			if err = c.syncNativeTexts(ctx, path, &state, p); err != nil {
				return err
			}
			if !p.Streaming {
				disabled := false
				if err = c.nativeWrite(ctx, path, &state, nativeCardWrite{Request: MessageCLIRequest{Method: "settings", Body: map[string]any{"settings": `{"config":{"streaming_mode":false}}`}}, Streaming: &disabled}); err != nil {
					return err
				}
			}
		}
		if state.Projection.Phase != p.Phase || state.Projection.Streaming != p.Streaming || !p.Streaming {
			data, _ := json.Marshal(p.Card)
			if err = c.nativeWrite(ctx, path, &state, nativeCardWrite{Request: MessageCLIRequest{Method: "replace", Body: map[string]any{"card": map[string]any{"type": "card_json", "data": string(data)}}}, Replacement: &p}); err != nil {
				return err
			}
		} else if err = c.syncNativeTexts(ctx, path, &state, p); err != nil {
			return err
		}
		state.Projection = p
		return writePrivateJSON(path, state)
	})
	return true, err
}

func (c *OfficialMessageClient) syncNativeTexts(ctx context.Context, path string, state *nativeCardState, p nativeProjection) error {
	desired := nativeTexts(p)
	elements := nativeElements(p)
	for index, e := range elements {
		id := e["element_id"].(string)
		text := desired[id]
		old, exists := state.Texts[id]
		if exists && old == text {
			continue
		}
		if id == "activity" && p.Streaming && state.Projection.Phase == p.Phase {
			if delay := time.Until(state.ActivityAcknowledged.Add(time.Second)); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		w := nativeCardWrite{ID: id, Text: text, Request: MessageCLIRequest{Method: "content", Params: map[string]string{"element_id": id}, Body: map[string]any{"content": text}}}
		if !exists {
			// Insert before the next acknowledged content region, not always at
			// the footer: late user input belongs before the existing reply.
			target := "activity"
			for _, next := range elements[index+1:] {
				nextID := next["element_id"].(string)
				if _, present := state.Texts[nextID]; present {
					target = nextID
					break
				}
			}
			data, _ := json.Marshal([]any{e})
			w.Request = MessageCLIRequest{Method: "insert", Body: map[string]any{"type": "insert_before", "target_element_id": target, "elements": string(data)}}
		}
		if err := c.nativeWrite(ctx, path, state, w); err != nil {
			return err
		}
	}
	removed := []string{}
	for id := range state.Texts {
		if _, ok := desired[id]; !ok {
			removed = append(removed, id)
		}
	}
	sort.Strings(removed)
	for _, id := range removed {
		if err := c.nativeWrite(ctx, path, state, nativeCardWrite{ID: id, Remove: true, Request: MessageCLIRequest{Method: "remove", Params: map[string]string{"element_id": id}, Body: map[string]any{}}}); err != nil {
			return err
		}
	}
	return nil
}
func (c *OfficialMessageClient) nativeWrite(ctx context.Context, path string, state *nativeCardState, w nativeCardWrite) error {
	// A new operation must not mutate the previous request's identity maps.
	body := make(map[string]any, len(w.Request.Body)+2)
	for key, value := range w.Request.Body {
		body[key] = value
	}
	w.Request.Body = body
	params := make(map[string]string, len(w.Request.Params)+1)
	for key, value := range w.Request.Params {
		params[key] = value
	}
	w.Request.Params = params
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.StandardTextUpdates {
		if w.Request.Method == "content" {
			data, _ := json.Marshal(map[string]any{"content": w.Text})
			w.Request.Method = "patch"
			w.Request.Body = map[string]any{"partial_element": string(data)}
		}
		if w.Replacement != nil {
			// The projection is desired task state; the durable transport mode
			// overrides animation only, including on subsequent turn transitions.
			data, _ := json.Marshal(w.Replacement.Card)
			var card map[string]any
			_ = json.Unmarshal(data, &card)
			card["config"].(map[string]any)["streaming_mode"] = false
			data, _ = json.Marshal(card)
			w.Request.Body = map[string]any{"card": map[string]any{"type": "card_json", "data": string(data)}}
		}
	}
	state.Sequence++
	w.Request.Resource = "cardkit"
	if w.Request.Params == nil {
		w.Request.Params = map[string]string{}
	}
	w.Request.Params["card_id"] = state.CardID
	w.Request.Body["sequence"] = state.Sequence
	w.Request.Body["uuid"] = fmt.Sprintf("%s-%d", secretHash(state.Key)[:32], state.Sequence)
	state.Pending = &w
	state.Submitted = time.Now().UTC()
	if err := writePrivateJSON(path, state); err != nil {
		return err
	}
	return c.applyNativeWrite(ctx, path, state)
}
func (c *OfficialMessageClient) applyNativeWrite(ctx context.Context, path string, state *nativeCardState) error {
	w := state.Pending
	if _, err := c.client.CallMessage(ctx, w.Request); err != nil {
		var rejection *UserApprovalError
		var failure *CLIExecutionError
		if w.Request.Resource == "cardkit" && (w.Request.Method == "content" || w.Request.Method == "patch") && !w.SequenceRebased && errors.As(err, &failure) && failure.Started && failure.Structured["type"] == "api" {
			code, _ := json.Marshal(failure.Structured["code"])
			if string(code) == "300317" {
				// A rejected replay does not prove the original request succeeded.
				// Reassert only idempotent full-text setters with a fresh durable
				// sequence. Never replay structural mutations under a new identity.
				// One rebase per operation bounds recovery across process restarts.
				w.SequenceRebased = true
				return c.nativeWrite(ctx, path, state, *w)
			}
		}
		if w.Request.Resource == "cardkit" && w.Request.Method == "content" && errors.As(err, &failure) && failure.Started && failure.Structured["type"] == "api" {
			code, _ := json.Marshal(failure.Structured["code"])
			if string(code) == "300309" {
				// Explicit remote rejection: no text was applied. Persist a new
				// operation identity for ordinary component PATCH, never a whole
				// card replacement. Unknown/network failures keep exact replay.
				state.StandardTextUpdates = true
				return c.nativeWrite(ctx, path, state, *w)
			}
		}
		if errors.As(err, &rejection) || errors.As(err, &failure) && !failure.Started {
			state.Pending = nil
			_ = writePrivateJSON(path, state)
		}
		return err
	}
	if state.Texts == nil {
		state.Texts = map[string]string{}
	}
	if w.Remove {
		delete(state.Texts, w.ID)
	} else if w.ID != "" {
		state.Texts[w.ID] = w.Text
	}
	if w.Replacement != nil {
		state.Projection = *w.Replacement
		state.Texts = nativeTexts(*w.Replacement)
	}
	if w.Streaming != nil {
		state.Projection.Streaming = *w.Streaming
	}
	state.Pending = nil
	state.Acknowledged = time.Now().UTC()
	if w.ID == "activity" {
		state.ActivityAcknowledged = state.Acknowledged
	}
	return writePrivateJSON(path, state)
}
