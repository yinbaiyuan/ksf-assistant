package desktop

import (
	"encoding/json"
	"testing"
)

func TestRequestRefPreservesStringAndIntegerIdentity(t *testing.T) {
	integer, err := ParseRequestRef(json.RawMessage(`900719925474099312345`))
	if err != nil {
		t.Fatal(err)
	}
	text, err := ParseRequestRef(json.RawMessage(`"900719925474099312345"`))
	if err != nil {
		t.Fatal(err)
	}
	if integer.Kind != "integer" || string(integer.Raw) != "900719925474099312345" || integer.Equal(text) || integer.Fingerprint() == text.Fingerprint() {
		t.Fatalf("typed identity collapsed: integer=%#v text=%#v", integer, text)
	}
	value, err := integer.Value()
	if err != nil || value.(json.Number).String() != "900719925474099312345" {
		t.Fatalf("integer bytes changed: %#v err=%v", value, err)
	}
}

func TestRequestRefAcceptsOnlyStringsAndIntegers(t *testing.T) {
	for _, raw := range []string{`1.5`, `true`, `null`, `{}`, `[]`, `""`} {
		if _, err := ParseRequestRef(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid request id %s", raw)
		}
	}
	if ref, err := ParseRequestRef(json.RawMessage(`-7`)); err != nil || ref.Kind != "integer" {
		t.Fatalf("negative integer rejected: %#v err=%v", ref, err)
	}
}
