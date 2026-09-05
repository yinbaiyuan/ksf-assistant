package feishucli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ksfassistant/core/internal/localipc"
	"ksfassistant/core/internal/privateipc"
)

func assertUploadBudgetEmpty(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		processUploads.mu.Lock()
		used, transfers := processUploads.bytes, processUploads.transfers
		processUploads.mu.Unlock()
		if used == 0 && transfers == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	processUploads.mu.Lock()
	defer processUploads.mu.Unlock()
	t.Fatalf("upload budget leaked: bytes=%d transfers=%d", processUploads.bytes, processUploads.transfers)
}

func uploadTestHandler(t *testing.T, next privateipc.Handler, method string) (*uploadHandler, *localipc.Session) {
	t.Helper()
	handler := NewUploadHandler(next, method).(*uploadHandler)
	root := t.TempDir()
	server, err := localipc.Listen(root, handler)
	if err != nil {
		t.Fatal(err)
	}
	session, err := localipc.Open(context.Background(), root)
	if err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(); _ = server.Close(); assertUploadBudgetEmpty(t) })
	return handler, session
}

func TestRunTransfersThirtyMiBMediaOnOneBoundConnection(t *testing.T) {
	assertUploadBudgetEmpty(t)
	root := t.TempDir()
	payload := bytes.Repeat([]byte("m"), MaxMediaBytes)
	expected := sha256.Sum256(payload)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	payload = nil
	var executions atomic.Int32
	next := privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != MethodExecute {
			return nil, privateipc.ErrMethodNotFound
		}
		request, err := DecodeRequest(params)
		if err != nil {
			return nil, err
		}
		if len(request.Payloads["media-file"]) != MaxMediaBytes || sha256.Sum256(request.Payloads["media-file"]) != expected {
			return nil, errors.New("media changed")
		}
		executions.Add(1)
		return map[string]any{"status": "ok", "bytes": len(request.Payloads["media-file"]), "submitted": true}, nil
	})
	handler := NewUploadHandler(next, MethodExecute)
	var mu sync.Mutex
	var connection <-chan struct{}
	starts, chunks, finishes := 0, 0, 0
	observed := privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		owner, ok := privateipc.ConnectionContext(ctx)
		if !ok {
			return nil, errors.New("connection context missing")
		}
		mu.Lock()
		if connection == nil {
			connection = owner.Done()
		}
		if connection != owner.Done() {
			mu.Unlock()
			return nil, errors.New("upload changed connection")
		}
		if len(params) >= privateipc.MaxFrameBytes {
			mu.Unlock()
			return nil, errors.New("oversized frame")
		}
		switch {
		case strings.HasSuffix(method, "/start"):
			starts++
		case strings.HasSuffix(method, "/chunk"):
			chunks++
		case strings.HasSuffix(method, "/finish"):
			finishes++
		}
		mu.Unlock()
		return handler.HandlePrivateRPC(ctx, method, params)
	})
	server, err := localipc.Listen(root, observed)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var output bytes.Buffer
	if err := Run(ctx, root, []string{"send", "--target", "alias", "--format", "file", "--media-file", path}, nil, &output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status    string `json:"status"`
		Bytes     int    `json:"bytes"`
		Submitted bool   `json:"submitted"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.Bytes != MaxMediaBytes || !result.Submitted || executions.Load() != 1 {
		t.Fatalf("result=%s executions=%d", output.String(), executions.Load())
	}
	mu.Lock()
	if starts != 1 || finishes != 1 || chunks < 100 {
		t.Errorf("starts=%d chunks=%d finishes=%d", starts, chunks, finishes)
	}
	mu.Unlock()
	assertUploadBudgetEmpty(t)
}

func TestUploadForwardingUsesFixedDaemonDestination(t *testing.T) {
	request := Request{Command: "send", Options: map[string]string{"target": "alias", "format": "file"}, Payloads: map[string][]byte{"media-file": bytes.Repeat([]byte("d"), 4*1024*1024)}}
	var executions atomic.Int32
	_, daemon := uploadTestHandler(t, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != "bridge/client/execute" {
			return nil, errors.New("wrong daemon destination")
		}
		value, err := DecodeRequest(params)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(value.Payloads["media-file"], request.Payloads["media-file"]) {
			return nil, errors.New("payload changed")
		}
		executions.Add(1)
		return map[string]any{"status": "ok", "operation": map[string]any{"id": "operation-same"}}, nil
	}), "bridge/client/execute")
	_, gateway := uploadTestHandler(t, privateipc.HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != MethodExecute {
			return nil, errors.New("wrong gateway destination")
		}
		value, err := DecodeRequest(params)
		if err != nil {
			return nil, err
		}
		var result json.RawMessage
		err = CallRequest(ctx, daemon.Call, "bridge/client/execute", value, &result)
		return result, err
	}), MethodExecute)
	var result json.RawMessage
	if err := CallRequest(context.Background(), gateway.Call, MethodExecute, request, &result); err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 1 || !strings.Contains(string(result), "operation-same") {
		t.Fatalf("result=%s count=%d", result, executions.Load())
	}
	assertUploadBudgetEmpty(t)
}

func TestNormalPrivateInputAndMediaLimitsRemainDistinct(t *testing.T) {
	request := Request{Command: "send", Options: map[string]string{"target": "alias", "format": "file"}, Payloads: map[string][]byte{"media-file": make([]byte, MaxMediaBytes)}}
	if err := Validate(request); err != nil {
		t.Fatal(err)
	}
	request.Payloads["media-file"] = make([]byte, MaxMediaBytes+1)
	if err := Validate(request); err == nil {
		t.Fatal("media above 30 MiB accepted")
	}
	request = Request{Command: "send", Options: map[string]string{"target": "alias"}, Payloads: map[string][]byte{"text-file": make([]byte, MaxPayloadBytes+1)}}
	if err := Validate(request); err == nil {
		t.Fatal("normal private input above 4 MiB accepted")
	}
	if _, err := Parse([]string{"send", "--target", "alias", "--text-file", "-"}, bytes.NewReader(request.Payloads["text-file"])); err == nil {
		t.Fatal("oversized private stdin accepted")
	}
}

func TestCallRequestKeepsSmallRequestsDirect(t *testing.T) {
	calls := 0
	caller := func(ctx context.Context, method string, params, target any) error {
		calls++
		if method != MethodExecute {
			return errors.New("small request was chunked")
		}
		return nil
	}
	if err := CallRequest(context.Background(), caller, MethodExecute, Request{Command: "status"}, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if err := CallRequest(context.Background(), caller, "arbitrary/api", Request{Command: "status"}, nil); err == nil {
		t.Fatal("arbitrary upload destination accepted")
	}
	if calls != 1 {
		t.Fatal("invalid destination invoked caller")
	}
}

func startTestTransfer(t *testing.T, handler *uploadHandler, owner context.Context, size int) UploadStarted {
	t.Helper()
	result, err := handler.start(owner, UploadStart{Version: 1, Size: size, SHA256: strings.Repeat("0", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func inertUploadHandler() *uploadHandler {
	return NewUploadHandler(privateipc.HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("must not execute")
	}), MethodExecute).(*uploadHandler)
}

func TestUploadIdentitySequenceAndOneUse(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	other, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	handler := inertUploadHandler()
	started := startTestTransfer(t, handler, owner, MaxRequestBytes+1)
	defer handler.discard(started.TransferID)
	if _, err := handler.chunk(other, UploadChunk{TransferID: started.TransferID, Data: []byte("x")}); err == nil {
		t.Fatal("different connection stole upload")
	}
	if _, err := handler.abort(other, UploadFinish{TransferID: started.TransferID}); err == nil {
		t.Fatal("different connection aborted upload")
	}
	if _, err := handler.chunk(owner, UploadChunk{TransferID: started.TransferID, Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.chunk(owner, UploadChunk{TransferID: started.TransferID, Offset: 0, Data: []byte("x")}); err == nil {
		t.Fatal("duplicate chunk accepted")
	}
	if _, err := handler.finish(context.Background(), owner, UploadFinish{TransferID: started.TransferID}); err == nil {
		t.Fatal("failed upload could execute")
	}
	assertUploadBudgetEmpty(t)
}

func TestUploadIncompleteAndChecksumFailureReleaseBudget(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := inertUploadHandler()
	for _, complete := range []bool{false, true} {
		started := startTestTransfer(t, handler, owner, MaxRequestBytes+1)
		if complete {
			for offset := 0; offset < MaxRequestBytes+1; {
				size := min(UploadChunkBytes, MaxRequestBytes+1-offset)
				if _, err := handler.chunk(owner, UploadChunk{TransferID: started.TransferID, Offset: offset, Data: make([]byte, size)}); err != nil {
					t.Fatal(err)
				}
				offset += size
			}
		}
		if _, err := handler.finish(context.Background(), owner, UploadFinish{TransferID: started.TransferID}); err == nil {
			t.Fatal("invalid upload executed")
		}
		assertUploadBudgetEmpty(t)
	}
}

func TestUploadProcessMemoryBudgetIsSharedAcrossHandlers(t *testing.T) {
	assertUploadBudgetEmpty(t)
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := inertUploadHandler(), inertUploadHandler()
	started := startTestTransfer(t, first, owner, MaxUploadRequestBytes)
	defer first.discard(started.TransferID)
	if _, err := second.start(owner, UploadStart{Version: 1, Size: MaxUploadRequestBytes, SHA256: strings.Repeat("0", 64)}); !errors.Is(err, privateipc.ErrBusy) {
		t.Fatalf("process memory bound bypassed: %v", err)
	}
	first.discard(started.TransferID)
	assertUploadBudgetEmpty(t)
}

func TestUploadTimeoutAndDisconnectCleanPartialBuffers(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	handler := inertUploadHandler()
	handler.idleTimeout = 15 * time.Millisecond
	startTestTransfer(t, handler, owner, MaxRequestBytes+1)
	assertUploadBudgetEmpty(t)
	handler.idleTimeout = time.Minute
	startTestTransfer(t, handler, owner, MaxRequestBytes+1)
	cancel()
	assertUploadBudgetEmpty(t)
}

func TestUploadSessionDisconnectAndCrossSessionRejection(t *testing.T) {
	root := t.TempDir()
	handler := inertUploadHandler()
	server, err := localipc.Listen(root, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	first, err := localipc.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := localipc.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	startMethod, chunkMethod, _, _, _ := uploadMethods(MethodExecute)
	var started UploadStarted
	if err := first.Call(context.Background(), startMethod, UploadStart{Version: 1, Size: MaxRequestBytes + 1, SHA256: strings.Repeat("0", 64)}, &started); err != nil {
		t.Fatal(err)
	}
	var progress UploadProgress
	if err := second.Call(context.Background(), chunkMethod, UploadChunk{TransferID: started.TransferID, Data: []byte("x")}, &progress); err == nil {
		t.Fatal("cross-session transfer accepted")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	assertUploadBudgetEmpty(t)
}

func TestCallRequestCancellationAbortsWithoutExecution(t *testing.T) {
	var executions atomic.Int32
	_, session := uploadTestHandler(t, privateipc.HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
		executions.Add(1)
		return map[string]bool{"ok": true}, nil
	}), MethodExecute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aborts := 0
	caller := func(ctx context.Context, method string, params, target any) error {
		err := session.Call(ctx, method, params, target)
		if strings.HasSuffix(method, "/chunk") {
			cancel()
		}
		if strings.HasSuffix(method, "/abort") {
			aborts++
		}
		return err
	}
	request := Request{Command: "send", Options: map[string]string{"target": "alias"}, Payloads: map[string][]byte{"text-file": make([]byte, MaxPayloadBytes)}}
	if err := CallRequest(ctx, caller, MethodExecute, request, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if aborts != 1 || executions.Load() != 0 {
		t.Fatalf("aborts=%d executions=%d", aborts, executions.Load())
	}
	assertUploadBudgetEmpty(t)
}

func TestUploadRejectsUntypedControlAndUnboundContext(t *testing.T) {
	handler, session := uploadTestHandler(t, privateipc.HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("unexpected dispatch")
	}), MethodExecute)
	startMethod, _, _, _, _ := uploadMethods(MethodExecute)
	if _, err := handler.HandlePrivateRPC(context.Background(), startMethod, json.RawMessage(`{}`)); err == nil {
		t.Fatal("unbound upload accepted")
	}
	for _, params := range []json.RawMessage{
		json.RawMessage(`{"version":1,"size":4194304,"sha256":"` + strings.Repeat("0", 64) + `","method":"arbitrary"}`),
		json.RawMessage(`{"version":1,"version":2,"size":4194304,"sha256":"` + strings.Repeat("0", 64) + `"}`),
		json.RawMessage(`{"version":1,"size":-1,"sha256":"` + strings.Repeat("0", 64) + `"}`),
	} {
		if err := session.Call(context.Background(), startMethod, params, nil); err == nil {
			t.Fatalf("invalid control accepted: %s", params)
		}
	}
	assertUploadBudgetEmpty(t)
}

func TestFinishedUploadCannotBeReplayed(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	var executions int
	handler := NewUploadHandler(privateipc.HandlerFunc(func(context.Context, string, json.RawMessage) (any, error) {
		executions++
		return map[string]bool{"ok": true}, nil
	}), MethodExecute).(*uploadHandler)
	request := Request{Command: "send", Options: map[string]string{"target": "alias"}, Payloads: map[string][]byte{"text-file": make([]byte, MaxPayloadBytes)}}
	encoded, _ := json.Marshal(request)
	digest := sha256.Sum256(encoded)
	started, err := handler.start(owner, UploadStart{Version: 1, Size: len(encoded), SHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(encoded); {
		end := min(offset+UploadChunkBytes, len(encoded))
		if _, err := handler.chunk(owner, UploadChunk{TransferID: started.TransferID, Offset: offset, Data: encoded[offset:end]}); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	if _, err := handler.finish(context.Background(), owner, UploadFinish{TransferID: started.TransferID}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.finish(context.Background(), owner, UploadFinish{TransferID: started.TransferID}); err == nil {
		t.Fatal("finished upload replayed")
	}
	if executions != 1 {
		t.Fatalf("executions=%d", executions)
	}
	assertUploadBudgetEmpty(t)
}
