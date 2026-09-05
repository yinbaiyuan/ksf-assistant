package privateipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeStrict decodes one fixed private-RPC DTO. Private protocol versions
// are negotiated explicitly, so silently accepting fields from another
// version would hide a contract mismatch instead of providing compatibility.
func DecodeStrict(raw json.RawMessage, target any, required bool) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		if required {
			return errors.New("private IPC params are required")
		}
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("private IPC params contain multiple JSON values")
		}
		return err
	}
	return nil
}

// RequireNoParams accepts an omitted/null params member or an empty object,
// and rejects any accidental payload on a parameterless method.
func RequireNoParams(raw json.RawMessage) error {
	return DecodeStrict(raw, &struct{}{}, false)
}
