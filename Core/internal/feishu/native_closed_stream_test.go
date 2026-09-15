package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type closedStreamFixture struct {
	probeCLIFixture
	patchFailure bool
}

type sequenceRecoveryFixture struct {
	probeCLIFixture
	rejected     bool
	uncertain    bool
	alwaysReject bool
}

func (f *sequenceRecoveryFixture) CallMessage(ctx context.Context, r MessageCLIRequest) (map[string]any, error) {
	if r.Method == "content" && (!f.rejected || f.alwaysReject) {
		f.requests = append(f.requests, r)
		if !f.uncertain {
			f.uncertain = true
			return nil, errors.New("confirmation lost")
		}
		f.rejected = true
		return nil, &CLIExecutionError{Started: true, Structured: map[string]any{"type": "api", "code": float64(300317)}}
	}
	return f.probeCLIFixture.CallMessage(ctx, r)
}
func TestNativeRestartRecoversRejectedTextSequence(t *testing.T) {
	f := &sequenceRecoveryFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	ctx := context.Background()
	if _, err := c.Send(ctx, MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(nativeFixture, "hello", "hello latest", 1)
	if err := c.PatchCard(ctx, "om_probe", updated); err == nil {
		t.Fatal("lost confirmation hidden")
	}
	original := f.requests[len(f.requests)-1]
	restarted, _ := NewOfficialMessageClient("app", f)
	restarted.nativeRoot = c.nativeRoot
	if err := restarted.PatchCard(ctx, "om_probe", updated); err != nil {
		t.Fatal(err)
	}
	writes := f.requests[2:]
	if len(writes) != 3 || writes[1].Body["uuid"] != original.Body["uuid"] || writes[2].Body["sequence"] != 2 || writes[2].Body["uuid"] == original.Body["uuid"] || writes[2].Method != "content" || writes[2].Body["content"] != "hello latest" {
		t.Fatal(writes)
	}
}

func TestNativeSequenceRecoveryIsBoundedAcrossRestart(t *testing.T) {
	f := &sequenceRecoveryFixture{alwaysReject: true, uncertain: true}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	ctx := context.Background()
	if _, err := c.Send(ctx, MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(nativeFixture, "hello", "latest", 1)
	if err := c.PatchCard(ctx, "om_probe", updated); err == nil {
		t.Fatal("rejection hidden")
	}
	if len(f.requests) != 4 {
		t.Fatal("unbounded retries", len(f.requests))
	}
	pending, _ := json.Marshal(f.requests[3])
	restarted, _ := NewOfficialMessageClient("app", f)
	restarted.nativeRoot = c.nativeRoot
	if err := restarted.PatchCard(ctx, "om_probe", updated); err == nil {
		t.Fatal("rejection hidden")
	}
	last, _ := json.Marshal(f.requests[len(f.requests)-1])
	if len(f.requests) != 5 || string(last) != string(pending) {
		t.Fatal("restart rebased twice")
	}
}

func (f *closedStreamFixture) CallMessage(ctx context.Context, r MessageCLIRequest) (map[string]any, error) {
	if r.Method == "patch" && f.patchFailure {
		f.requests = append(f.requests, r)
		return nil, errors.New("timeout")
	}
	if r.Resource == "cardkit" && r.Method == "content" {
		f.requests = append(f.requests, r)
		return nil, &CLIExecutionError{Started: true, Structured: map[string]any{"type": "api", "code": float64(300309)}}
	}
	if r.Resource == "cardkit" {
		if _, err := cardKitCommand(r); err != nil {
			return nil, err
		}
	}
	return f.probeCLIFixture.CallMessage(ctx, r)
}

func TestNativeFallbackUncertainPatchKeepsExactIdentity(t *testing.T) {
	f := &closedStreamFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	ctx := context.Background()
	if _, err := c.Send(ctx, MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(nativeFixture, "hello", "latest", 1)
	f.patchFailure = true
	if err := c.PatchCard(ctx, "om_probe", updated); err == nil {
		t.Fatal("timeout hidden")
	}
	pending, _ := json.Marshal(f.requests[len(f.requests)-1])
	f.patchFailure = false
	restarted, _ := NewOfficialMessageClient("app", f)
	restarted.nativeRoot = c.nativeRoot
	n := len(f.requests)
	if err := restarted.PatchCard(ctx, "om_probe", updated); err != nil {
		t.Fatal(err)
	}
	retried, _ := json.Marshal(f.requests[n])
	if len(f.requests) != n+1 || string(pending) != string(retried) {
		t.Fatal("uncertain fallback patch identity changed")
	}
}

func TestNativeClosedStreamFallsBackWithoutReplacingInput(t *testing.T) {
	f := &closedStreamFixture{}
	c, _ := NewOfficialMessageClient("app", f)
	c.nativeRoot = t.TempDir()
	ctx := context.Background()
	if _, err := c.Send(ctx, MessageTarget{Type: "open_id", ID: "ou_test"}, "card", nativeFixture, "unique"); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(nativeFixture, "hello", "hello world", 1)
	if err := c.PatchCard(ctx, "om_probe", updated); err != nil {
		t.Fatal("closed streaming blocks updates", err)
	}
	writes := f.requests[2:]
	if len(writes) != 2 || writes[0].Method != "content" || writes[1].Method != "patch" {
		t.Fatal("must replace only rejected text operation", writes)
	}
	if writes[0].Body["uuid"] == writes[1].Body["uuid"] {
		t.Fatal("new request reused rejected identity")
	}
	var body map[string]any
	if json.Unmarshal([]byte(writes[1].Body["partial_element"].(string)), &body) != nil || body["content"] != "hello world" {
		t.Fatal(body)
	}
	restarted, _ := NewOfficialMessageClient("app", f)
	restarted.nativeRoot = c.nativeRoot
	n := len(f.requests)
	if err := restarted.PatchCard(ctx, "om_probe", strings.Replace(updated, "reading", "testing", 1)); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != n+1 || f.requests[n].Method != "patch" || f.requests[n].Params["element_id"] != "activity" {
		t.Fatal("fallback not durable; input rebuilt", f.requests[n:])
	}
	next := strings.Replace(updated, "running:turn1", "running:turn2", 1)
	if err := restarted.PatchCard(ctx, "om_probe", next); err != nil {
		t.Fatal(err)
	}
	last := f.requests[len(f.requests)-1]
	if last.Method != "replace" {
		t.Fatal("phase controls not updated")
	}
	var card map[string]any
	if json.Unmarshal([]byte(last.Body["card"].(map[string]any)["data"].(string)), &card) != nil || card["config"].(map[string]any)["streaming_mode"] != false {
		t.Fatal("phase transition re-enabled closed stream")
	}
}
