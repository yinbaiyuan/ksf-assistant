package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"ksfassistant/core/internal/userapproval"
	"regexp"
	"strconv"
	"time"
)

// CardKit is an internal, bot-only transport. It is not an arbitrary API proxy.
// The service using it must own the entity/message binding and sequence journal.
type cardKitAuthorizationKey struct{}

var cardKitID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func cardKitCommand(request MessageCLIRequest) ([]string, error) {
	invalid := errors.New("invalid_cardkit_request")
	if request.File != "" {
		return nil, invalid
	}
	method, path := "", "/open-apis/cardkit/v1/cards"
	allowed := map[string]bool{}
	if request.Method == "create" {
		if len(request.Params) != 0 || request.Body["type"] != "card_json" {
			return nil, invalid
		}
		data, ok := request.Body["data"].(string)
		var card map[string]any
		if !ok || len(data) > 30000 || json.Unmarshal([]byte(data), &card) != nil || card["schema"] != "2.0" {
			return nil, invalid
		}
		method = "POST"
		allowed["type"], allowed["data"] = true, true
	} else {
		id := request.Params["card_id"]
		if !cardKitID.MatchString(id) {
			return nil, invalid
		}
		path += "/" + id
		uuid, ok := request.Body["uuid"].(string)
		if !ok || !cardKitID.MatchString(uuid) {
			return nil, invalid
		}
		encoded, err := json.Marshal(request.Body["sequence"])
		sequence, parseErr := strconv.ParseInt(string(encoded), 10, 32)
		if err != nil || parseErr != nil || sequence < 1 {
			return nil, invalid
		}
		allowed["uuid"], allowed["sequence"] = true, true
		switch request.Method {
		case "replace":
			if len(request.Params) != 1 {
				return nil, invalid
			}
			card, ok := request.Body["card"].(map[string]any)
			if !ok || len(card) != 2 || card["type"] != "card_json" {
				return nil, invalid
			}
			data, ok := card["data"].(string)
			var value map[string]any
			if !ok || len(data) > 30000 || json.Unmarshal([]byte(data), &value) != nil || value["schema"] != "2.0" {
				return nil, invalid
			}
			allowed["card"] = true
			method = "PUT"
		case "insert":
			if len(request.Params) != 1 || request.Body["type"] != "insert_before" {
				return nil, invalid
			}
			target, ok := request.Body["target_element_id"].(string)
			data, valid := request.Body["elements"].(string)
			var elements []map[string]any
			if !ok || !cardKitID.MatchString(target) || !valid || len(data) > 30000 || json.Unmarshal([]byte(data), &elements) != nil || len(elements) != 1 || elements[0]["tag"] != "markdown" {
				return nil, invalid
			}
			id, ok := elements[0]["element_id"].(string)
			if !ok || !cardKitID.MatchString(id) {
				return nil, invalid
			}
			allowed["type"], allowed["target_element_id"], allowed["elements"] = true, true, true
			method, path = "POST", path+"/elements"
		case "remove":
			if len(request.Params) != 2 || !cardKitID.MatchString(request.Params["element_id"]) {
				return nil, invalid
			}
			method, path = "DELETE", path+"/elements/"+request.Params["element_id"]
		case "content", "patch":
			if len(request.Params) != 2 || !cardKitID.MatchString(request.Params["element_id"]) {
				return nil, invalid
			}
			path += "/elements/" + request.Params["element_id"]
			field := "content"
			method, path = "PUT", path+"/content"
			if request.Method == "patch" {
				method, path, field = "PATCH", "/open-apis/cardkit/v1/cards/"+id+"/elements/"+request.Params["element_id"], "partial_element"
			}
			text, ok := request.Body[field].(string)
			if !ok || len(text) > 30000 {
				return nil, invalid
			}
			if field == "partial_element" {
				var partial map[string]string
				if json.Unmarshal([]byte(text), &partial) != nil || len(partial) != 1 {
					return nil, invalid
				}
				if _, ok := partial["content"]; !ok {
					return nil, invalid
				}
			}
			allowed[field] = true
		case "settings":
			if len(request.Params) != 1 {
				return nil, invalid
			}
			text, ok := request.Body["settings"].(string)
			var settings struct {
				Config map[string]bool `json:"config"`
			}
			var outer map[string]json.RawMessage
			if !ok || json.Unmarshal([]byte(text), &outer) != nil || len(outer) != 1 || json.Unmarshal([]byte(text), &settings) != nil || len(settings.Config) != 1 {
				return nil, invalid
			}
			if _, ok := settings.Config["streaming_mode"]; !ok {
				return nil, invalid
			}
			allowed["settings"] = true
			method, path = "PATCH", path+"/settings"
		default:
			return nil, invalid
		}
	}
	if len(request.Body) != len(allowed) {
		return nil, invalid
	}
	for field := range request.Body {
		if !allowed[field] {
			return nil, invalid
		}
	}
	return []string{"api", method, path, "--as", "bot"}, nil
}

func internalCardKitContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, cardKitAuthorizationKey{}, true)
}

// Wait only for a local authorization lease, before any remote request. No
// business operation is retried, and cancellation/timeout remains fail-closed.
func cardKitExecutionLease(ctx context.Context, root string) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		release, err := userapproval.TrySharedExecutionLease(root)
		if err == nil || err.Error() != "approval_authorization_busy" {
			return release, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
