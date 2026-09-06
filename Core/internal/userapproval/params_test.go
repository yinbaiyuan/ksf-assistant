package userapproval

import "testing"

func TestDecisionParamsRejectAmbiguity(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"id":"a","approve":null}`, `{"id":"a","approve":false,"approve":true}`, `{"ID":"a","approve":true}`, `{"id":"a","approve":true,"approved":true}`, `{"id":"a","approve":true} {}`} {
		var value struct {
			ID      string `json:"id"`
			Approve bool   `json:"approve"`
		}
		if DecodeParams([]byte(raw), &value, "id", "approve") == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	var value struct {
		ID      string `json:"id"`
		Approve bool   `json:"approve"`
	}
	if err := DecodeParams([]byte(`{"id":"a","approve":false}`), &value, "id", "approve"); err != nil {
		t.Fatal(err)
	}
}
