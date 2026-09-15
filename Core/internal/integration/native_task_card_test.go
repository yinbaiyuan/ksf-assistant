package integration

import (
	"encoding/json"
	"testing"
)

func TestNativeTaskCardStableMessageRegions(t *testing.T) {
	l := TaskLink{ID: "link", LinkState: "active", TurnState: "running"}
	l.SetExtraValue("nativeTaskCard", true)
	l.SetExtraString("progressTurnId", "turn")
	l.SetExtraString("latestInputTurnId", "turn")
	l.SetExtraString("latestInput", "请检查")
	l.SetExtraValue("progressSegments", []taskProgressSegment{{ID: "message-one", Text: "hello"}, {ID: "message-two", Text: "world"}})
	raw, err := TaskLinkCardJSON(l)
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	json.Unmarshal([]byte(raw), &card)
	if card["ksf_cardkit"] == nil {
		t.Fatal("missing native transport projection")
	}
	ids := map[string]bool{}
	for _, e := range card["body"].(map[string]any)["elements"].([]any) {
		m := e.(map[string]any)
		if id, _ := m["element_id"].(string); id != "" {
			ids[id] = true
		}
	}
	if !ids["message_user"] || !ids["activity"] || !ids["m"+cardDigest("message-one")[:18]] || !ids["m"+cardDigest("message-two")[:18]] {
		t.Fatal(ids)
	}
	l.SetExtraValue("nativeTaskCard", false)
	raw, _ = TaskLinkCardJSON(l)
	json.Unmarshal([]byte(raw), &card)
	var legacy map[string]any
	json.Unmarshal([]byte(raw), &legacy)
	if legacy["ksf_cardkit"] != nil {
		t.Fatal("legacy upgraded")
	}
}
