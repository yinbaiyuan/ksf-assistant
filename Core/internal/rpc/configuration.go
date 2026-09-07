package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"ksfassistant/core/internal/privateipc"
)

func decodeConfigurationParams(raw json.RawMessage, target any, allowed ...string) error {
	if len(raw) > 16*1024 {
		return errors.New("configuration_request_too_large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("configuration_invalid_request")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("configuration_invalid_request")
		}
		key, ok := token.(string)
		valid := false
		for _, name := range allowed {
			if key == name {
				valid = true
			}
		}
		if !ok || !valid || seen[key] {
			return errors.New("configuration_invalid_request")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("configuration_invalid_request")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("configuration_invalid_request")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("configuration_invalid_request")
	}
	if err := privateipc.DecodeStrict(raw, target, true); err != nil {
		return errors.New("configuration_invalid_request")
	}
	return nil
}
