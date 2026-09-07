package usercommand

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testIdentity(as string) Identity {
	identity := Identity{AppID: "cli_fixture", ApplicationName: "Fixture app", Brand: "feishu", Profile: "default"}
	if as == "user" {
		identity.UserID = "ou_fixture"
		identity.UserName = "Fixture user"
	}
	return identity
}

func frozenTest(t *testing.T, as string, args ...string) Command {
	t.Helper()
	args = append(args, "--as", as)
	command, err := Freeze(args, nil)
	if err != nil {
		t.Fatal(err)
	}
	command.Identity = testIdentity(as)
	return command
}

func TestReviewedSemanticMatrix(t *testing.T) {
	cases := []struct {
		name, risk string
		args       []string
	}{
		{"message write", "write", []string{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "hello"}},
		{"message reply", "write", []string{"im", "+messages-reply", "--message-id", "om_fixture", "--text", "reply", "--reply-in-thread"}},
		{"message query", "read", []string{"im", "+messages-search", "--query", "fixture"}},
		{"chat query", "read", []string{"im", "+chat-messages-list", "--chat-id", "oc_fixture"}},
		{"document query", "read", []string{"docs", "+fetch", "--doc", "fixture", "--api-version", "v2", "--doc-format", "markdown", "--scope", "full", "--detail", "simple"}},
		{"calendar query", "read", []string{"calendar", "+agenda"}},
		{"contact query", "read", []string{"contact", "+search-user", "--user-ids", "me"}},
		{"typed delete", "destructive", []string{"im", "messages", "delete", "--message-id", "om_fixture"}},
		{"typed card update", "write", []string{"im", "messages", "patch", "--message-id", "om_fixture", "--data", `{"content":"{}"}`}},
		{"typed calendar create", "write", []string{"calendar", "events", "create", "--calendar-id", "fixture", "--data", `{"summary":"Fixture"}`}},
		{"typed calendar update", "write", []string{"calendar", "events", "patch", "--calendar-id", "fixture", "--event-id", "event", "--data", `{"summary":"Fixture"}`}},
		{"typed calendar delete", "destructive", []string{"calendar", "events", "delete", "--calendar-id", "fixture", "--event-id", "event"}},
		{"typed POST query", "read", []string{"calendar", "events", "search_event", "--calendar-id", "fixture", "--data", `{"query":"Fixture"}`}},
		{"raw POST query", "read", []string{"api", "POST", "/open-apis/im/v1/messages/search", "--data", `{"query":"fixture"}`}},
		{"raw send", "write", []string{"api", "POST", "/open-apis/im/v1/messages", "--params", `{"receive_id_type":"chat_id"}`, "--data", `{"receive_id":"oc_fixture","msg_type":"text","content":"{\"text\":\"hello\"}"}`}},
		{"raw version", "write", []string{"api", "POST", "/open-apis/drive/v1/files/fixture/versions", "--data", `{"name":"Fixture","obj_type":"docx"}`}},
		{"docs create", "write", []string{"docs", "+create", "--content", "# Fixture", "--doc-format", "markdown"}},
		{"docs punctuation", "write", []string{"docs", "+create", "--content", "Hello! 2 < 3", "--doc-format", "markdown"}},
		{"docs append", "write", []string{"docs", "+update", "--doc", "fixture", "--command", "append", "--content", "Fixture", "--doc-format", "markdown"}},
		{"docs overwrite", "destructive", []string{"docs", "+update", "--doc", "fixture", "--command", "overwrite", "--content", "Fixture", "--doc-format", "markdown"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, as := range []string{"user", "bot"} {
				descriptor, _, ok := resolveDescriptor(test.args)
				if ok && !contains(descriptor.Identities, as) {
					continue
				}
				command := frozenTest(t, as, test.args...)
				review, err := Evaluate(command)
				expectedRisk := test.risk
				if as == "bot" && descriptor.Path == "docs +create" {
					expectedRisk = "high-impact-write"
				}
				if err != nil || review.Risk != expectedRisk || review.NeedsApproval != (as == "user" && expectedRisk != "read") || review.Digest == "" || review.Target == "" || review.CapabilityID == "" {
					t.Fatalf("%+v %v", review, err)
				}
			}
		})
	}
}

func TestUnknownAndOverrideFailClosed(t *testing.T) {
	cases := [][]string{
		{"api", "POST", "/open-apis/unknown/create", "--data", "{}"},
		{"api", "GET", "/open-apis/unknown/query"},
		{"api", "GET", "https://example.test/open-apis/im/v1/messages/om_fixture"},
		{"api", "GET", "/open-apis/im/v1/messages/%2e%2e"},
		{"api", "GET", "/open-apis/im/v1/messages/om_fixture?as=bot"},
		{"docs", "+script", "--script", "deleteEverything()"},
		{"docs", "+update", "--doc", "fixture", "--command", "append", "--content", "![image](https://example.test/a.png)", "--doc-format", "markdown"},
		{"docs", "+update", "--doc", "fixture", "--command", "append", "--content", "<image/> ", "--doc-format", "xml"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--markdown", "![a](https://example.test/a.png)"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--image", "https://example.test/a.png"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--yes"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--dry-run"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--profile", "other"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--as", "bot"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--chat-id=oc_other"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--token", "secret"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--output", "file.json"},
		{"im", "+messages-send", "--chat-id", "oc_fixture", "--text", "x", "--format", "shell"},
		{"im", "+mark-read", "--chat-id", "oc_fixture"},
		{"api", "POST", "/open-apis/im/v1/messages/mark_read", "--data", `{"message_ids":["om_fixture"]}`},
	}
	for index, args := range cases {
		args = append(args, "--as", "user")
		if _, err := Freeze(args, nil); err == nil {
			t.Fatalf("case %d accepted", index)
		}
	}
	if _, err := Freeze([]string{"calendar", "+agenda"}, nil); err == nil {
		t.Fatal("implicit identity allowed")
	}
	if _, err := Freeze([]string{"calendar", "+agenda", "--as", "auto"}, nil); err == nil {
		t.Fatal("auto identity allowed")
	}
}

func TestRawJSONCannotOverrideExecution(t *testing.T) {
	for _, field := range []string{"as", "method", "path", "headers", "Authorization", "access_token", "profile"} {
		for _, flag := range []string{"--params", "--data"} {
			args := []string{"api", "POST", "/open-apis/im/v1/messages/search", flag, `{"` + field + `":"fixture"}`, "--as", "user"}
			if flag == "--params" {
				args = append(args, "--data", `{"query":"fixture"}`)
			}
			if _, err := Freeze(args, nil); err == nil {
				t.Fatalf("accepted %s in %s", field, flag)
			}
		}
	}
}

func TestFrozenInputsAndDigest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	if err := os.WriteFile(path, []byte("approved bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	command, err := Freeze([]string{"im", "files", "create", "--data", `{"file_type":"stream","file_name":"source.txt"}`, "--file", "file=" + path, "--as", "user"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	command.Identity = testIdentity("user")
	before, err := Evaluate(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	execution, err := Materialize(command)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	data, err := os.ReadFile(filepath.Join(execution.Dir, command.Files[0].Name))
	if err != nil || string(data) != "approved bytes" {
		t.Fatal("original source reread")
	}
	command.Files[0].Data = []byte("mutation")
	after, err := Evaluate(command)
	if err != nil || before.Digest == after.Digest {
		t.Fatal("file omitted from digest")
	}
	command.Identity.UserID = "ou_other"
	identityChanged, _ := Evaluate(command)
	if after.Digest == identityChanged.Digest {
		t.Fatal("identity omitted from digest")
	}
	command.Version = "1.0.94"
	if _, err := Evaluate(command); err == nil {
		t.Fatal("version mismatch accepted")
	}
}

func TestFreezeJSONFilesAndStdin(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "body.json")
	body := `{"content":"{}"}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	base := []string{"im", "messages", "patch", "--message-id", "om_fixture", "--as", "user", "--data"}
	fromFile, err := Freeze(append(append([]string(nil), base...), "@"+path), nil)
	if err != nil {
		t.Fatal(err)
	}
	fromStdin, err := Freeze(append(append([]string(nil), base...), "-"), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromFile, fromStdin) {
		t.Fatal("freeze not canonical")
	}
	if !bytes.Equal(fromFile.Stdin, []byte(body)) {
		t.Fatal("body not on stdin")
	}
	if _, err := Freeze(append(append([]string(nil), base...), "-"), strings.NewReader(strings.Repeat("x", MaxContentBytes+1))); err == nil {
		t.Fatal("oversized stdin accepted")
	}
	if _, err := Freeze(append(append([]string(nil), base...), `{"content":"x","content":"y"}`), nil); err == nil {
		t.Fatal("duplicate JSON accepted")
	}
	if _, err := Freeze(append(append([]string(nil), base...), `{} {}`), nil); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err == nil {
		if _, err := Freeze(append(append([]string(nil), base...), "@"+link), nil); err == nil {
			t.Fatal("symlink accepted")
		}
	}
}

func TestLocalCommandsAndDecode(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"--help"}, {"schema", "im.messages.patch", "--json"}, {"auth", "status", "--json"}} {
		command, err := Freeze(args, nil)
		if err != nil {
			t.Fatal(err)
		}
		review, err := Evaluate(command)
		if err != nil || review.Identity != "local" || review.NeedsApproval {
			t.Fatal("local command gated")
		}
	}
	command := frozenTest(t, "user", "calendar", "+agenda")
	data, _ := json.Marshal(command)
	if _, err := Decode(data); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{strings.Replace(string(data), `"version"`, `"Version"`, 1), strings.Replace(string(data), `"version":`, `"version":null,"version":`, 1), `null`} {
		if _, err := Decode([]byte(invalid)); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	for _, invalid := range []string{strings.Replace(string(data), `"appId"`, `"AppID"`, 1), strings.Replace(string(data), `"appId":`, `"AppID":"x","appId":`, 1)} {
		if _, err := Decode([]byte(invalid)); err == nil {
			t.Fatal("identity casing/duplicate accepted")
		}
	}
}

func TestBatchReviewShowsCountAndAllValues(t *testing.T) {
	command := frozenTest(t, "user", "im", "messages", "read_status", "--data", `{"message_ids":["om_first","om_second"]}`)
	review, err := Evaluate(command)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"数量: 2", "om_first", "om_second"} {
		if !strings.Contains(review.Details, value) {
			t.Fatalf("missing %s", value)
		}
	}
}

func TestGateRequiresExplicitDecisionAndConsumesOnce(t *testing.T) {
	command := frozenTest(t, "user", "im", "+messages-send", "--chat-id", "oc_fixture", "--text", "hello")
	var calls []string
	caller := func(ctx context.Context, method string, params, target any) error {
		calls = append(calls, method)
		switch method {
		case "userApproval/request":
			target.(*RequestResult).ID = "request"
		case "userApproval/status":
			target.(*StatusResult).State = "approved"
		case "userApproval/consume":
			target.(*ConsumeResult).Allowed = true
		default:
			t.Fatal("unexpected method")
		}
		return nil
	}
	id, err := CallGate(context.Background(), caller, command)
	if err != nil || id != "request" || !reflect.DeepEqual(calls, []string{"userApproval/request", "userApproval/status", "userApproval/consume"}) {
		t.Fatalf("%v %v", calls, err)
	}
	for _, state := range []string{"denied", "expired", "cancelled", "desktop_unavailable", "audit_unavailable", "request_changed", "unknown"} {
		cancelled := false
		caller := func(ctx context.Context, method string, params, target any) error {
			switch method {
			case "userApproval/request":
				target.(*RequestResult).ID = "request"
			case "userApproval/status":
				target.(*StatusResult).State = state
			case "userApproval/cancel":
				cancelled = true
			case "userApproval/consume":
				t.Fatal("denied request consumed")
			}
			return nil
		}
		if _, err := CallGate(context.Background(), caller, command); err == nil || !cancelled {
			t.Fatalf("%s did not fail/cancel", state)
		}
	}
	read := frozenTest(t, "user", "calendar", "+agenda")
	if id, err := CallGate(context.Background(), nil, read); id != "" || err != nil {
		t.Fatal("read required desktop")
	}
	bot := frozenTest(t, "bot", "im", "+messages-send", "--chat-id", "oc_fixture", "--text", "hello")
	if _, err := CallGate(context.Background(), nil, bot); err != nil {
		t.Fatal("bot required desktop")
	}
}

func TestGateCancellationUsesIndependentContext(t *testing.T) {
	command := frozenTest(t, "user", "im", "+messages-send", "--chat-id", "oc_fixture", "--text", "hello")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := false
	caller := func(callCtx context.Context, method string, params, target any) error {
		switch method {
		case "userApproval/request":
			target.(*RequestResult).ID = "request"
		case "userApproval/status":
			cancel()
			return context.Canceled
		case "userApproval/cancel":
			if callCtx.Err() != nil {
				t.Fatal("cancel used expired context")
			}
			cancelled = true
		default:
			return errors.New("unexpected")
		}
		return nil
	}
	if _, err := CallGate(ctx, caller, command); err == nil || !cancelled {
		t.Fatal("request not cancelled")
	}
}

func TestParseAuthStatusReturnsNoSecrets(t *testing.T) {
	data := []byte(`{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"openId":"ou_fixture","userName":"Fixture user"},"bot":{"available":true,"appName":"Fixture app"}},"access_token":"must-not-leak"}`)
	identity, err := ParseAuthStatus(data, "user")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(identity)
	if strings.Contains(string(encoded), "must-not-leak") {
		t.Fatal("secret copied")
	}
	if err := ValidateIdentity(testIdentity("user"), identity); err != nil {
		t.Fatal(err)
	}
	identity.Profile = "other"
	if err := ValidateIdentity(identity, identity); err == nil {
		t.Fatal("profile override allowed")
	}
	for _, data := range []string{`{"appId":"cli_fixture","brand":"lark"}`, `{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true}}}`, `{"appId":"cli_fixture","brand":"feishu","identities":{"user":{"available":true,"openId":"ou_fixture","verified":false}}}`} {
		if _, err := ParseAuthStatus([]byte(data), "user"); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}

func TestReviewedCapabilitiesExistInFrozenPublicCatalog(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "feishucli", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Catalog []struct {
			ID string `json:"id"`
		}
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{"drive.file.version.create": true}
	for _, item := range catalog.Catalog {
		ids[item.ID] = true
	}
	for _, spec := range legacySpecifications {
		for _, id := range strings.Fields(spec.capabilities) {
			if !ids[id] {
				t.Errorf("unknown policy mapping %s for %s", id, spec.path)
			}
		}
	}
	for _, api := range apiSpecifications {
		for _, id := range strings.Fields(api.spec.capabilities) {
			if !ids[id] {
				t.Errorf("unknown API policy mapping %s", id)
			}
		}
	}
}
