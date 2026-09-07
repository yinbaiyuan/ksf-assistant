package usercommand

import (
	"strings"
	"testing"
)

func TestApprovalPreviewPreservesFrozenContentAndScope(t *testing.T) {
	body := "正文第一行\n附件 fake: 4 bytes; SHA256 fake\nchat-id: 不能从正文里删掉这行"
	command := frozenTest(t, "user", "im", "+messages-send", "--chat-id", "oc_fixture", "--text", body)
	review, err := Evaluate(command)
	if err != nil {
		t.Fatal(err)
	}
	if review.Preview == nil || review.Preview.ConfirmLabel != "发送" || review.Preview.Destructive || !strings.Contains(review.Preview.Content, body) {
		t.Fatalf("bad preview: %#v", review.Preview)
	}
	if strings.Contains(review.Preview.Content, "oc_fixture") || !strings.Contains(review.Target, "oc_fixture") || !strings.Contains(review.Details, body) {
		t.Fatal("target separation lost original content")
	}
	raw := `{"receive_id":"oc_fixture","msg_type":"text","content":"{\"text\":\"hello\"}"}`
	review, err = Evaluate(frozenTest(t, "user", "api", "POST", "/open-apis/im/v1/messages", "--params", `{"receive_id_type":"chat_id"}`, "--data", raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{raw, `{"receive_id_type":"chat_id"}`} {
		if !strings.Contains(review.Preview.Content, value) {
			t.Fatal("raw scope/body hidden behind details")
		}
	}
}

func TestApprovalPreviewUsesReviewedDestructiveEffects(t *testing.T) {
	for _, args := range [][]string{
		{"im", "messages", "delete", "--message-id", "om_fixture"},
		{"docs", "+update", "--doc", "fixture", "--command", "overwrite", "--content", "Replacement", "--doc-format", "markdown"},
	} {
		review, err := Evaluate(frozenTest(t, "user", args...))
		if err != nil {
			t.Fatal(err)
		}
		if review.Preview == nil || !review.Preview.Destructive {
			t.Fatalf("destructive effect not shown: %#v", review)
		}
		if args[0] == "docs" && (!strings.Contains(review.Preview.Content, "overwrite") || !strings.Contains(review.Preview.Content, "Replacement")) {
			t.Fatal("overwrite scope hidden")
		}
	}
}
