package taskruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

func strictDecode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return fail("invalid_request", "JSON must be valid UTF-8")
	}
	scanner := json.NewDecoder(bytes.NewReader(data))
	scanner.UseNumber()
	if err := scanJSON(scanner, 0); err != nil {
		return fail("invalid_request", "malformed JSON, duplicate keys or excessive nesting")
	}
	if _, err := scanner.Token(); err != io.EOF {
		return fail("invalid_request", "exactly one JSON object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fail("invalid_request", "unknown JSON field or invalid field type")
	}
	return nil
}

func scanJSON(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return io.ErrUnexpectedEOF
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return io.ErrUnexpectedEOF
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return io.ErrUnexpectedEOF
			}
			seen[name] = true
		}
		if err := scanJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func canonicalJSON(value any) []byte {
	data, _ := json.Marshal(value)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized any
	_ = decoder.Decode(&normalized)
	result, _ := json.Marshal(normalized)
	return result
}

func containsIdentity(value any, identity string) bool {
	var decoded any
	_ = json.Unmarshal(canonicalJSON(value), &decoded)
	return containsText(decoded, identity)
}

func containsText(value any, identity string) bool {
	switch typed := value.(type) {
	case string:
		return bytes.Contains([]byte(typed), []byte(identity))
	case []any:
		for _, item := range typed {
			if containsText(item, identity) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if containsText(key, identity) || containsText(item, identity) {
				return true
			}
		}
	}
	return false
}
