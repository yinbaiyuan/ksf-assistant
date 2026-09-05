package localipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ksfassistant/core/internal/privateipc"
)

func TestDarwinRootCaseAliasUsesSameListenerAndIdentity(t *testing.T) {
	parent := t.TempDir()
	root, alias := filepath.Join(parent, "DataRoot"), filepath.Join(parent, "dataroot")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(alias); os.IsNotExist(err) {
		t.Skip("case-sensitive volume")
	} else if err != nil {
		t.Fatal(err)
	}
	server, err := Listen(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if duplicate, err := Listen(alias, nil); err == nil {
		duplicate.Close()
		t.Fatal("case alias bypassed listener ownership")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var remote *privateipc.RPCError
	if err := Call(ctx, alias, "unknown", nil, nil); !errors.As(err, &remote) || remote.Code != privateipc.ErrMethodNotFound.Code {
		t.Fatalf("case alias failed handshake: %v", err)
	}
}
