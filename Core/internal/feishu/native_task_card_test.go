package feishu

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const nativeFixture = `{"schema":"2.0","config":{"streaming_mode":true},"ksf_cardkit":{"phase":"running:turn1","streaming":true},"body":{"elements":[{"tag":"markdown","element_id":"m1","content":"hello"},{"tag":"markdown","element_id":"activity","content":"reading"},{"tag":"form","name":"input","elements":[]}]}}`

func TestNativeLateUserInputPrecedesReplyWithoutReplacingForm(t *testing.T) {
	f := &probeCLIFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	ctx := context.Background()
	if _, err := c.Send(ctx, MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(nativeFixture, `"elements":[`, `"elements":[{"tag":"markdown","element_id":"message_user","content":"**你**\n问题"},`, 1)
	if err := c.PatchCard(ctx, "om_probe", updated); err != nil {
		t.Fatal(err)
	}
	requests := f.requests[2:]
	if len(requests) != 1 || requests[0].Method != "insert" || requests[0].Body["target_element_id"] != "m1" {
		t.Fatal(requests)
	}
	updated = strings.Replace(updated, "问题", "修正后的问题", 1)
	if err := c.PatchCard(ctx, "om_probe", updated); err != nil {
		t.Fatal(err)
	}
	last := f.requests[len(f.requests)-1]
	if last.Method != "content" || last.Params["element_id"] != "message_user" {
		t.Fatal(last)
	}
}

func TestNativeTaskCardCreatesEntityAndDoesNotRecreate(t *testing.T) {
	f := &probeCLIFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	id, err := c.Send(context.Background(), MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 || f.requests[0].Resource != "cardkit" || id != "om_probe" {
		t.Fatal("entity path not used", f.requests)
	}
	n := len(f.requests)
	if _, err = c.Send(context.Background(), MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil || len(f.requests) != n {
		t.Fatal("duplicate send", err)
	}
}

func TestNativeTaskCardTextUpdateAndExactRecovery(t *testing.T) {
	f := &probeCLIFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	ctx := context.Background()
	if _, err := c.Send(ctx, MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil {
		t.Fatal(err)
	}
	f.fail = true
	updated := strings.Replace(nativeFixture, "hello", "hello world", 1)
	if err := c.PatchCard(ctx, "om_probe", updated); err == nil {
		t.Fatal("expected uncertain update")
	}
	first := f.requests[len(f.requests)-1]
	if first.Method != "content" || first.Params["element_id"] != "m1" {
		t.Fatal(first)
	}
	f.fail = false
	restarted, _ := NewOfficialMessageClient("app", f)
	restarted.nativeRoot = c.nativeRoot
	if err := restarted.PatchCard(ctx, "om_probe", updated); err != nil {
		t.Fatal(err)
	}
	last := f.requests[len(f.requests)-1]
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(last)
	if string(a) != string(b) {
		t.Fatalf("retry identity changed: %s / %s", a, b)
	}
	n := len(f.requests)
	if err := restarted.PatchCard(ctx, "om_probe", updated); err != nil || len(f.requests) != n {
		t.Fatal("acknowledged update replayed", err)
	}
}

func TestNativeTaskCardFinalTextBeforeClosing(t *testing.T) {
	f := &probeCLIFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	ctx := context.Background()
	if _, err := c.Send(ctx, MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil {
		t.Fatal(err)
	}
	final := strings.ReplaceAll(strings.Replace(strings.Replace(nativeFixture, "hello", "final", 1), "running:turn1", "completed:turn1", 1), "true", "false")
	if err := c.PatchCard(ctx, "om_probe", final); err != nil {
		t.Fatal(err)
	}
	requests := f.requests[2:]
	if len(requests) != 3 || requests[0].Method != "content" || requests[1].Method != "settings" || requests[2].Method != "replace" {
		t.Fatal(requests)
	}
	for i, r := range requests {
		if r.Body["sequence"] != i+1 {
			t.Fatal("sequence", r)
		}
	}
}

func TestNativeTaskCardRejectsMalformedIDs(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(nativeFixture, `"element_id":"m1"`, `"element_id":42`, 1),
		strings.Replace(nativeFixture, `"element_id":"m1"`, `"element_id":"activity"`, 1),
		strings.Replace(nativeFixture, `"content":"hello"`, `"content":42`, 1),
	} {
		if _, native, err := parseNativeProjection(raw); !native || err == nil {
			t.Fatal("accepted invalid projection", raw)
		}
	}
}

func TestNativeTaskCardMissingBindingNeverFallsBack(t *testing.T) {
	f := &probeCLIFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	if err := c.PatchCard(context.Background(), "om_missing", nativeFixture); err == nil || len(f.requests) != 0 {
		t.Fatal("missing native binding fell back to whole message update", err)
	}
	if err := c.PatchCard(context.Background(), "om_old", `{"schema":"2.0","body":{"elements":[]}}`); err != nil || len(f.requests) != 1 || f.requests[0].Resource != "messages" {
		t.Fatal("legacy card broken", err)
	}
}
