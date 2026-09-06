package userapproval

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"ksfassistant/core/internal/privateipc"
)

func DecodeParams(raw json.RawMessage, target any, keys ...string) error {
	if len(raw) > 4096 {
		return errors.New("approval_invalid_request")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("approval_invalid_request")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("approval_invalid_request")
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return errors.New("approval_invalid_request")
		}
		allowed := false
		for _, expected := range keys {
			if key == expected {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("approval_invalid_request")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("approval_invalid_request")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("approval_invalid_request")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("approval_invalid_request")
	}
	if len(seen) != len(keys) {
		return errors.New("approval_invalid_request")
	}
	return privateipc.DecodeStrict(raw, target, true)
}
