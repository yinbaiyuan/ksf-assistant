package desktop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var desktopIntegerRequestID = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)

// RequestRef preserves the JSON type and bytes of a Desktop server request ID.
// Desktop accepts both string and integer IDs; converting an integer through
// float64 or string changes the lookup key and can produce a silent no-op.
type RequestRef struct {
	Raw  json.RawMessage
	Kind string
}

func ParseRequestRef(raw json.RawMessage) (RequestRef, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return RequestRef{}, errors.New("invalid Codex Desktop user-input request id")
	}
	if raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
			return RequestRef{}, errors.New("invalid Codex Desktop user-input request id")
		}
		canonical, _ := json.Marshal(value)
		return RequestRef{Raw: canonical, Kind: "string"}, nil
	}
	value := string(raw)
	if !desktopIntegerRequestID.MatchString(value) {
		return RequestRef{}, errors.New("invalid Codex Desktop user-input request id")
	}
	return RequestRef{Raw: append(json.RawMessage(nil), raw...), Kind: "integer"}, nil
}

func RequestRefFromValue(value any) (RequestRef, error) {
	switch typed := value.(type) {
	case string:
		raw, _ := json.Marshal(typed)
		return ParseRequestRef(raw)
	case json.Number:
		return ParseRequestRef(json.RawMessage(typed.String()))
	case json.RawMessage:
		return ParseRequestRef(typed)
	case int:
		raw, _ := json.Marshal(typed)
		return ParseRequestRef(raw)
	case int64:
		raw, _ := json.Marshal(typed)
		return ParseRequestRef(raw)
	case uint64:
		raw, _ := json.Marshal(typed)
		return ParseRequestRef(raw)
	default:
		return RequestRef{}, errors.New("invalid Codex Desktop user-input request id")
	}
}

func (ref RequestRef) Value() (any, error) {
	parsed, err := ParseRequestRef(ref.Raw)
	if err != nil {
		return nil, err
	}
	if parsed.Kind == "string" {
		var value string
		_ = json.Unmarshal(parsed.Raw, &value)
		return value, nil
	}
	return json.Number(string(parsed.Raw)), nil
}

func (ref RequestRef) CompatibilityString() string {
	value, err := ref.Value()
	if err != nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return value.(json.Number).String()
}

func (ref RequestRef) Equal(other RequestRef) bool {
	left, leftErr := ParseRequestRef(ref.Raw)
	right, rightErr := ParseRequestRef(other.Raw)
	return leftErr == nil && rightErr == nil && left.Kind == right.Kind && bytes.Equal(left.Raw, right.Raw)
}

func (ref RequestRef) Fingerprint() string {
	parsed, err := ParseRequestRef(ref.Raw)
	if err != nil {
		return "invalid"
	}
	digest := sha256.Sum256(append([]byte(parsed.Kind+"\x00"), parsed.Raw...))
	return hex.EncodeToString(digest[:8])
}
