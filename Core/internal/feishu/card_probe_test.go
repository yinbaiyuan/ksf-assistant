package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestCardProbeDoesNotCreateWithoutExplicitStart(t *testing.T) {
	root, transport := newTransportFixture(t, &transportClientFixture{})
	fixture := &messageCLIFixture{result: map[string]any{"card_id": "123"}}
	client, _ := NewOfficialMessageClient("fixture", fixture)
	probe := NewCardProbe(root, transport, client)
	if _, err := probe.Command(context.Background(), "step", "test-1", "", ""); err == nil {
		t.Fatal("missing probe advanced")
	}
	if fixture.request.Resource != "" {
		t.Fatal("implicit card creation")
	}
}

type probeCLIFixture struct {
	requests []MessageCLIRequest
	fail     bool
	err      error
}

func TestFormalProbeUsesGovernedNativeLifecycle(t *testing.T) {
	p, f := newProbeFixture(t)
	p.client.nativeRoot = p.root
	ctx := context.Background()
	if _, err := p.Command(ctx, "start", "formal-test", "fixture", "formal"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := p.Command(ctx, "step", "formal-test", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Command(ctx, "finish", "formal-test", "", ""); err != nil {
		t.Fatal(err)
	}
	methods := map[string]int{}
	for _, r := range f.requests {
		methods[r.Method]++
	}
	if methods["insert"] != 1 || methods["remove"] != 1 || methods["settings"] != 1 || methods["replace"] != 1 {
		t.Fatal(methods)
	}
}

func (f *probeCLIFixture) CallMessage(_ context.Context, r MessageCLIRequest) (map[string]any, error) {
	f.requests = append(f.requests, r)
	if f.err != nil {
		return nil, f.err
	}
	if f.fail {
		return nil, errors.New("timeout")
	}
	if r.Resource == "cardkit" && r.Method == "create" {
		return map[string]any{"card_id": "123"}, nil
	}
	if r.Resource == "messages" && r.Method == "create" {
		return map[string]any{"message_id": "om_probe"}, nil
	}
	return map[string]any{}, nil
}

func TestCardProbePreSendRejectionPreservesLastProjection(t *testing.T) {
	p, f := newProbeFixture(t)
	ctx := context.Background()
	if _, err := p.Command(ctx, "start", "busy-test", "fixture", "native"); err != nil {
		t.Fatal(err)
	}
	f.err = &UserApprovalError{Code: "approval_authorization_busy"}
	if _, err := p.Command(ctx, "step", "busy-test", "", ""); err == nil {
		t.Fatal("expected local rejection")
	}
	var state cardProbeState
	_, err := readPrivateJSON(filepath.Join(p.directory("busy-test"), "state.json"), &state)
	if err != nil || state.Phase != "ready" || state.Samples != 0 || state.Sequence != 0 || state.LastError != "approval_authorization_busy" {
		t.Fatalf("wrong rejection state: %+v %v", state, err)
	}
}

func newProbeFixture(t *testing.T) (*CardProbe, *probeCLIFixture) {
	t.Helper()
	f := &probeCLIFixture{}
	client, _ := NewOfficialMessageClient("fixture", f)
	root, transport := newTransportFixture(t, client)
	store := NewCapabilityPolicyStore(root)
	policy, _ := store.Load()
	policy.CapabilityOverrides["im.sdk.message.send"] = CapabilityAllowed
	if _, err := store.Save(policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	return NewCardProbe(root, transport, client), f
}

func TestCardProbeLifecycleAndStopDoNotUpdateInput(t *testing.T) {
	p, f := newProbeFixture(t)
	ctx := context.Background()
	if _, err := p.Command(ctx, "start", "test-1", "fixture", "component"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Command(ctx, "start", "test-1", "fixture", "component"); err == nil {
		t.Fatal("duplicate card created")
	}
	if _, err := p.Command(ctx, "step", "test-1", "", ""); err != nil {
		t.Fatal(err)
	}
	r := f.requests[len(f.requests)-1]
	if r.Method != "patch" || r.Params["element_id"] != "probe_body" || r.Body["sequence"] != 1 {
		t.Fatalf("%+v", r)
	}
	var partial map[string]any
	if json.Unmarshal([]byte(r.Body["partial_element"].(string)), &partial) != nil || partial["content"] == nil || len(partial) != 1 {
		t.Fatal("input region was included")
	}
	card := InboundCardAction{MessageID: "om_probe", OperatorOpenID: "ou_fixture", Value: map[string]any{"probeId": "test-1", "action": "stop"}}
	if handled, err := p.HandleCard(card); !handled || err != nil {
		t.Fatal(handled, err)
	}
	if _, err := p.Command(ctx, "step", "test-1", "", ""); err != nil {
		t.Fatal(err)
	}
	r = f.requests[len(f.requests)-1]
	if r.Method != "settings" || r.Body["sequence"] != 2 {
		t.Fatalf("stop sent more body: %+v", r)
	}
	if _, err := p.Command(ctx, "step", "test-1", "", ""); err == nil {
		t.Fatal("finished probe resumed")
	}
}

func TestCardProbeUnknownUpdateCannotReplayAndForeignCallbackRejected(t *testing.T) {
	p, f := newProbeFixture(t)
	ctx := context.Background()
	if _, err := p.Command(ctx, "start", "test-1", "fixture", "native"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.HandleCard(InboundCardAction{MessageID: "om_other", OperatorOpenID: "ou_fixture", Value: map[string]any{"probeId": "test-1", "action": "stop"}}); err == nil {
		t.Fatal("foreign message accepted")
	}
	f.fail = true
	if _, err := p.Command(ctx, "step", "test-1", "", ""); err == nil {
		t.Fatal("expected timeout")
	}
	n := len(f.requests)
	if _, err := p.Command(ctx, "step", "test-1", "", ""); err == nil || len(f.requests) != n {
		t.Fatal("uncertain operation replayed")
	}
	var state cardProbeState
	_, err := readPrivateJSON(filepath.Join(p.directory("test-1"), "state.json"), &state)
	if err != nil || state.Sequence != 1 || state.Phase != "update_outcome_unknown" {
		t.Fatal(state, err)
	}
}

func TestCardProbeUsesStableRegionsAndIsolatedCallbacks(t *testing.T) {
	card := cardProbeJSON("test-1", true)
	if card["config"].(map[string]any)["streaming_mode"] != true {
		t.Fatal("native mode missing")
	}
	elements := card["body"].(map[string]any)["elements"].([]any)
	if elements[0].(map[string]any)["element_id"] != "probe_body" || elements[1].(map[string]any)["element_id"] != "probe_progress" {
		t.Fatal("unstable regions")
	}
	form := elements[2].(map[string]any)
	if form["tag"] != "form" {
		t.Fatal("input is not below progress")
	}
	columns := form["elements"].([]any)[0].(map[string]any)["columns"].([]any)
	submit := columns[2].(map[string]any)["elements"].([]any)[0].(map[string]any)
	behaviors, _ := submit["behaviors"].([]any)
	if len(behaviors) != 1 {
		t.Fatal("submit must bind an explicit callback like existing task cards")
	}
	value := behaviors[0].(map[string]any)["value"].(map[string]any)
	if value["probeId"] != "test-1" || value["action"] != "submit" {
		t.Fatal("submit callback is not isolated")
	}
}

func TestCardProbeUnknownBodyCanBeClosedWithoutReplay(t *testing.T) {
	p, f := newProbeFixture(t)
	ctx := context.Background()
	if _, err := p.Command(ctx, "start", "close-test", "fixture", "native"); err != nil {
		t.Fatal(err)
	}
	f.fail = true
	_, _ = p.Command(ctx, "step", "close-test", "", "")
	f.fail = false
	if _, err := p.Command(ctx, "finish", "close-test", "", ""); err != nil {
		t.Fatal(err)
	}
	r := f.requests[len(f.requests)-1]
	if r.Method != "settings" || r.Body["sequence"] != 2 {
		t.Fatal("replayed body instead of closing")
	}
}
