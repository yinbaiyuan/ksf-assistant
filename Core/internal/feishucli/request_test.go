package feishucli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
)

func TestEventProfileWritesAreRetiredAndCatalogIsReadOnly(t *testing.T) {
	for _, role := range []string{"primary", "manual-only", "managed"} {
		if _, err := Parse([]string{"profile", "set", role}, strings.NewReader("")); err == nil {
			t.Fatalf("legacy profile write parsed: %s", role)
		}
		if err := Validate(Request{Command: "profile", Action: "set", Positionals: []string{role}}); err == nil {
			t.Fatalf("wire profile write accepted: %s", role)
		}
	}
	result, local, err := Static(Request{Command: "profile", Action: "catalog"})
	if err != nil || !local {
		t.Fatalf("compatibility catalog: %v %v", local, err)
	}
	value := result.(map[string]any)
	state := value["eventConsumer"].(map[string]any)
	if len(value["profiles"].([]any)) != 0 || state["profile"] != "managed" || state["desiredConnection"] != true || state["configurable"] != false {
		t.Fatalf("catalog offers event roles: %+v", value)
	}
	if _, exists := value["sharedAppRule"]; exists {
		t.Fatal("retired primary arbitration rule still exposed")
	}
}

func TestParseTransfersExplicitInputInsteadOfPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(path, []byte(`{"calendar-id":"primary","event-id":"event"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	request, err := Parse([]string{"capability", "read", "calendar.shortcut.get", "--payload-file", path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(path)) || request.Options["payload-file"] != "" || len(request.Payloads["payload-file"]) == 0 {
		t.Fatalf("input was not transferred safely: %s", encoded)
	}
	decoded, err := DecodeRequest(encoded)
	if err != nil || !bytes.Equal(decoded.Payloads["payload-file"], request.Payloads["payload-file"]) {
		t.Fatalf("round trip failed: %v", err)
	}
}

func TestParseStdinAndNestedActions(t *testing.T) {
	request, err := Parse([]string{"knowledge", "comments", "add", "--target", "doc_target", "--content-file", "-", "--async"}, strings.NewReader("comment"))
	if err != nil {
		t.Fatal(err)
	}
	if request.Command != "knowledge" || request.Action != "comments/add" || string(request.Payloads["content-file"]) != "comment" {
		t.Fatalf("request=%#v", request)
	}
	request, err = Parse([]string{"targets", "directory", "search", "--query=Alice", "--limit", "3"}, nil)
	if err != nil || request.Action != "directory/search" || request.Options["query"] != "Alice" {
		t.Fatalf("request=%#v err=%v", request, err)
	}
}

func TestStrictCommandAndFlagValidation(t *testing.T) {
	cases := [][]string{
		{"api", "GET", "/anything"}, {"meeting", "end"}, {"status", "--config", "secret"},
		{"status", "extra"}, {"snapshot", "--payload-file", "secret"},
		{"events", "recent", "--limit"}, {"events", "recent", "--limit", "many"},
		{"events", "recent", "--limit", "2", "--limit", "3"},
		{"events", "recent", "--limit", "0"}, {"capability", "catalog", "--arbitrary"},
		{"capability", "get", "apps.app.create"}, {"targets", "list", "--config", "other"},
		{"task-link", "status"}, {"task-link", "execute"}, {"profile", "set", "invalid"},
		{"send", "--target", "a", "--target-name", "b", "--text-file", "-"},
		{"doc", "update", "--target", "doc", "--content-file", "-", "--pattern-file", "-", "--mode", "str_replace"},
		{"knowledge", "read", "--target", "doc", "--async=not-bool"},
		{"calendar", "get", "--event-id", "--async"},
		{"send", "--id", "../../outside", "--target", "alias", "--media-file", "-", "--format", "file"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := Parse(args, strings.NewReader(`{}`)); err == nil {
				t.Fatal("invalid CLI input accepted")
			}
		})
	}
}

func TestStrictWireRejectsUnknownDuplicateAndPathFields(t *testing.T) {
	cases := []string{
		`{"command":"status","argv":["send"]}`,
		`{"command":"status","command":"doctor"}`,
		`{"Command":"status","command":"doctor"}`,
		`{"command":"events","action":"recent","options":{"limit":"1","limit":"2"}}`,
		`{"command":"status"} {"command":"doctor"}`,
		`{"command":"status","payloads":{"payload-file":"e30="}}`,
		`{"command":"policy","action":"update","options":{"expected-revision":"1","payload-file":"/private/secret"}}`,
		`{"command":"send","options":{"target":"alias"},"switches":["async","async"],"payloads":{"text-file":"dGV4dA=="}}`,
		`null`,
		`{"command":"capability","action":"write","positionals":["im.sdk.message.send"],"payloads":{"payload-file":"eyJmaWxlLXBhdGgiOiJzZWNyZXQuanNvbiJ9"}}`,
	}
	for _, value := range cases {
		if _, err := DecodeRequest([]byte(value)); err == nil {
			t.Fatalf("invalid wire accepted: %s", value)
		}
	}
}

func TestCapabilityPayloadInputFilesAreReadOnlyByClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document.txt")
	if err := os.WriteFile(path, []byte("uploaded content"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"file-path": path})
	request, err := Parse([]string{"capability", "write", "im.sdk.message.send", "--payload-file", "-"}, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if string(request.Payloads["file-path"]) != "uploaded content" || bytes.Contains(request.Payloads["payload-file"], []byte("file-path")) {
		t.Fatalf("input path was not replaced: %#v", request)
	}
	encoded, _ := json.Marshal(request)
	if _, err := DecodeRequest(encoded); err != nil {
		t.Fatalf("safe transferred input rejected: %v", err)
	}
	request, err = Parse([]string{"capability", "write", "im.sdk.message.send", "--file-path", path, "--payload-file", "-"}, strings.NewReader(`{}`))
	if err != nil || string(request.Payloads["file-path"]) != "uploaded content" {
		t.Fatalf("explicit field: %#v %v", request, err)
	}
}

func TestUnsafeAndOversizedInputRejected(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err == nil {
		if _, err := Parse([]string{"task-link", "create", "--payload-file", link}, nil); err == nil {
			t.Fatal("symlink input accepted")
		}
	}
	if _, err := Parse([]string{"task-link", "create", "--payload-file", root}, nil); err == nil {
		t.Fatal("directory input accepted")
	}
	if _, err := Parse([]string{"task-link", "create", "--payload-file", "-"}, strings.NewReader(strings.Repeat("x", MaxPayloadBytes+1))); err == nil {
		t.Fatal("oversized stdin accepted")
	}
}

func TestOfflineCommandsDoNotReadSettingsOrCreateDataRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"help"}, {"--version"}, {"capabilities"}, {"capability", "catalog"}, {"events", "catalog"}, {"profile", "catalog"}} {
		var output bytes.Buffer
		if err := Run(context.Background(), root, args, nil, &output); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if output.Len() == 0 {
			t.Fatalf("%v returned no output", args)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("offline commands created data root: %v", err)
	}
}

func TestStoppedApplicationCannotExecuteOrReadConfiguration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"status"}, {"snapshot"}, {"doctor"}, {"targets", "init"}, {"send", "--target", "alias", "--text-file", "-"}} {
		var output bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := Run(ctx, root, args, strings.NewReader("hello"), &output)
		cancel()
		if !errors.Is(err, localipc.ErrNotRunning) || !strings.Contains(err.Error(), "service unavailable") || output.Len() != 0 {
			t.Fatalf("%v: err=%v output=%s", args, err, output.String())
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("stopped CLI created data root: %v", err)
	}
}

func TestRunForwardsTypedRequestAndPreservesJSONEnvelope(t *testing.T) {
	root := t.TempDir()
	received := make(chan Request, 1)
	server, err := localipc.Listen(root, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != MethodExecute {
			return nil, errors.New("wrong method")
		}
		request, err := DecodeRequest(params)
		if err != nil {
			return nil, err
		}
		received <- request
		return json.RawMessage(`{"status":"ok","count":9007199254740993,"result":{"value":"unchanged"}}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := Run(ctx, root, []string{"task-link", "create", "--payload-file", "-"}, strings.NewReader(`{"threadId":"thread"}`), &output); err != nil {
		t.Fatal(err)
	}
	request := <-received
	if request.Command != "task-link" || request.Action != "create" || string(request.Payloads["payload-file"]) != `{"threadId":"thread"}` {
		t.Fatalf("request=%#v", request)
	}
	if !strings.Contains(output.String(), `9007199254740993`) || !json.Valid(output.Bytes()) {
		t.Fatalf("JSON envelope changed: %s", output.String())
	}
}

func TestRejectsBeforeReadingStdin(t *testing.T) {
	reader := &countingReader{}
	_, err := Parse([]string{"task-link", "create", "--payload-file", "-", "--arbitrary"}, reader)
	if err == nil || reader.reads != 0 {
		t.Fatalf("err=%v reads=%d", err, reader.reads)
	}
}

func TestSerializedPayloadAlwaysFitsTransportFrame(t *testing.T) {
	request := Request{Command: "send", Options: map[string]string{"target": "alias"}, Payloads: map[string][]byte{"text-file": bytes.Repeat([]byte("x"), 2*1024*1024)}}
	if err := Validate(request); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(request)
	if len(encoded)+65536 > privateipc.MaxFrameBytes {
		t.Fatal("accepted request exceeds framed transport budget")
	}
	request.Payloads["text-file"] = bytes.Repeat([]byte("x"), MaxPayloadBytes)
	if err := Validate(request); err != nil {
		t.Fatalf("valid logical request should use chunk transfer: %v", err)
	}
	root := filepath.Join(t.TempDir(), "absent")
	var output bytes.Buffer
	err := Run(context.Background(), root, []string{"send", "--target", "alias", "--text-file", "-"}, bytes.NewReader(request.Payloads["text-file"]), &output)
	if !errors.Is(err, localipc.ErrNotRunning) {
		t.Fatalf("logical request must fail closed without a running application: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("oversized request produced output")
	}
}

type countingReader struct{ reads int }

func (reader *countingReader) Read(buffer []byte) (int, error) { reader.reads++; return 0, io.EOF }

func TestRetiredDocumentCommandsAreRejectedBeforeStateAccess(t *testing.T) {
	for _, args := range [][]string{{"doc", "create", "--content-file", "/tmp/retired"}, {"doc", "update", "--target", "legacy", "--content-file", "/tmp/retired"}, {"result", "docbox", "DOC-retired"}, {"recent", "docbox"}} {
		if _, err := Parse(args, strings.NewReader("")); err == nil {
			t.Fatalf("retired command accepted: %v", args)
		}
	}
}
