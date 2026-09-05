package localipc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/feishucli"
	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
)

func TestSessionTransfersChunkedCLIRequest(t *testing.T) {
	root := t.TempDir()
	var executed atomic.Int32
	handler := feishucli.NewUploadHandler(privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		owner, ok := privateipc.ConnectionContext(ctx)
		if !ok || owner.Err() != nil {
			return nil, errors.New("upload lost its live connection owner")
		}
		if method != feishucli.MethodExecute {
			return nil, privateipc.ErrMethodNotFound
		}
		request, err := feishucli.DecodeRequest(params)
		if err != nil {
			return nil, err
		}
		executed.Add(1)
		return map[string]int{"bytes": len(request.Payloads["text-file"])}, nil
	}), feishucli.MethodExecute)
	server, err := localipc.Listen(root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := localipc.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := feishucli.Request{Command: "send", Options: map[string]string{"target": "fixture"}, Payloads: map[string][]byte{"text-file": bytes.Repeat([]byte("x"), 4*1024*1024)}}
	var result struct {
		Bytes int `json:"bytes"`
	}
	if err := feishucli.CallRequest(ctx, session.Call, feishucli.MethodExecute, request, &result); err != nil {
		t.Fatal(err)
	}
	if result.Bytes != len(request.Payloads["text-file"]) || executed.Load() != 1 {
		t.Fatalf("upload executed=%d transferred=%d", executed.Load(), result.Bytes)
	}
}

func TestSessionUploadRejectsCrossConnectionOwner(t *testing.T) {
	root := t.TempDir()
	handler := feishucli.NewUploadHandler(privateipc.HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("incomplete fixture upload executed")
		return nil, nil
	}), feishucli.MethodExecute)
	server, err := localipc.Listen(root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := localipc.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := localipc.Dial(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	digest := sha256.Sum256(nil)
	var upload feishucli.UploadStarted
	start := feishucli.UploadStart{Version: 1, Size: feishucli.MaxRequestBytes + 1, SHA256: hex.EncodeToString(digest[:])}
	if err := first.Call(ctx, feishucli.MethodExecute+"/upload/start", start, &upload); err != nil {
		t.Fatal(err)
	}
	var aborted feishucli.UploadAborted
	finish := feishucli.UploadFinish{TransferID: upload.TransferID}
	if err := second.Call(ctx, feishucli.MethodExecute+"/upload/abort", finish, &aborted); err == nil {
		t.Fatal("another connection claimed upload")
	}
	if err := first.Call(ctx, feishucli.MethodExecute+"/upload/abort", finish, &aborted); err != nil || !aborted.Aborted {
		t.Fatalf("own connection lost upload: %v", err)
	}
}
