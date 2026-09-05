package feishu

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recordingSender struct{ calls []string }

func (sender *recordingSender) Send(_ context.Context, _ MessageTarget, format, value, id string) (string, error) {
	sender.calls = append(sender.calls, format+":"+value)
	return "om_" + id, nil
}

func TestOutboxProcessesSplitMessageAndPersistsState(t *testing.T) {
	root := t.TempDir()
	box := NewOutbox(root)
	sender := &recordingSender{}
	request := OutboxRequest{ID: "OUT-test", Type: "text", Target: MessageTarget{Type: "open_id", ID: "ou_test"}, Text: string(make([]byte, 0)), ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC()}
	request.Text = "hello"
	if err := box.Submit(request); err != nil {
		t.Fatal(err)
	}
	if err := box.Process(context.Background(), sender, false); err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 1 || sender.calls[0] != "text:hello" {
		t.Fatalf("unexpected sends: %#v", sender.calls)
	}
	if err := box.Process(context.Background(), sender, false); err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("request was replayed: %#v", sender.calls)
	}
}

func TestOutboxDryRunDoesNotSend(t *testing.T) {
	root := t.TempDir()
	box := NewOutbox(root)
	sender := &recordingSender{}
	request := OutboxRequest{ID: "OUT-dry", Type: "text", Target: MessageTarget{Type: "chat_id", ID: "oc_test"}, Text: "hello", ExplicitAuthorization: true, Source: "test", CreatedAt: time.Now().UTC()}
	if err := box.Submit(request); err != nil {
		t.Fatal(err)
	}
	if err := box.Process(context.Background(), sender, true); err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 0 {
		t.Fatal("dry run sent a real message")
	}
}

func TestOutboxRemovesStagedMediaAfterDryRun(t *testing.T) {
	root := t.TempDir()
	mediaRoot := filepath.Join(root, "private-cache", "media")
	if err := os.MkdirAll(mediaRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mediaRoot, "OUT-MEDIA.txt")
	if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := OutboxRequest{ID: "OUT-MEDIA", Type: "file", Target: MessageTarget{Type: "open_id", ID: "ou_test"}, FilePath: path, ExplicitAuthorization: true, DryRun: true, Source: "test"}
	result := NewOutbox(root).processRequest(context.Background(), nil, request, false)
	if result.Status != "dry_run" {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("staged media survived terminal dry-run: %v", err)
	}
}

func TestOfficialMessageContent(t *testing.T) {
	msgType, content, err := officialMessageContent("markdown", "**hello**")
	if err != nil || msgType != "interactive" || content == "" {
		t.Fatalf("markdown conversion failed: %s %s %v", msgType, content, err)
	}
	if _, _, err := officialMessageContent("card", "not-json"); err == nil {
		t.Fatal("invalid card accepted")
	}
}
