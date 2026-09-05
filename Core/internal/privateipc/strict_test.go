package privateipc

import (
	"encoding/json"
	"testing"
)

func TestDecodeStrictRejectsUnknownAndTrailingFields(t *testing.T) {
	type request struct {
		Name  string      `json:"name"`
		Value json.Number `json:"value"`
	}
	var value request
	if err := DecodeStrict(json.RawMessage(`{"name":"test","value":900719925474099312345}`), &value, true); err != nil {
		t.Fatal(err)
	}
	if value.Value.String() != "900719925474099312345" {
		t.Fatalf("number was not preserved: %s", value.Value)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"name":"test","value":1,"extra":true}`),
		json.RawMessage(`{"name":"test","value":1} {}`),
	} {
		if err := DecodeStrict(raw, &value, true); err == nil {
			t.Fatalf("expected strict rejection for %s", raw)
		}
	}
}

func TestRequireNoParamsAcceptsOnlyEmptyParams(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`)} {
		if err := RequireNoParams(raw); err != nil {
			t.Fatalf("empty params rejected: %s: %v", raw, err)
		}
	}
	if err := RequireNoParams(json.RawMessage(`{"unexpected":true}`)); err == nil {
		t.Fatal("parameterless method accepted an unknown field")
	}
}
