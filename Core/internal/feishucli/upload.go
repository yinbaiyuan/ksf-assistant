package feishucli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"ksfassistant/core/internal/privateipc"
)

const MaxMediaBytes = 30 * 1024 * 1024
const MaxUploadRequestBytes = 44 * 1024 * 1024
const UploadChunkBytes = 256 * 1024
const UploadMemoryLimit = 512 * 1024 * 1024
const UploadTransferLimit = 8
const UploadIdleTimeout = 30 * time.Second
const UploadLifetime = 2 * time.Minute
const UploadExecutionTimeout = 4 * time.Minute

const uploadControlBytes = 512 * 1024
const uploadScratchCharge = 2 * 1024 * 1024

type UploadStart struct {
	Version int    `json:"version"`
	Size    int    `json:"size"`
	SHA256  string `json:"sha256"`
}

type UploadStarted struct {
	TransferID string `json:"transferId"`
	ChunkBytes int    `json:"chunkBytes"`
}

type UploadChunk struct {
	TransferID string `json:"transferId"`
	Offset     int    `json:"offset"`
	Data       []byte `json:"data"`
}

type UploadFinish struct {
	TransferID string `json:"transferId"`
}

type UploadProgress struct {
	Received int `json:"received"`
}

type UploadAborted struct {
	Aborted bool `json:"aborted"`
}

type uploadBudget struct {
	mu        sync.Mutex
	bytes     int
	transfers int
}

var processUploads uploadBudget
var uploadDecoders = make(chan struct{}, 8)

func (budget *uploadBudget) reserve(bytes, transfers int) (func(), error) {
	budget.mu.Lock()
	if bytes < 0 || bytes > UploadMemoryLimit-budget.bytes || transfers > UploadTransferLimit-budget.transfers {
		budget.mu.Unlock()
		return nil, privateipc.ErrBusy
	}
	budget.bytes += bytes
	budget.transfers += transfers
	budget.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			budget.mu.Lock()
			budget.bytes -= bytes
			budget.transfers -= transfers
			budget.mu.Unlock()
		})
	}, nil
}

func uploadMethods(fixedMethod string) (string, string, string, string, error) {
	if fixedMethod != MethodExecute && fixedMethod != "bridge/client/execute" {
		return "", "", "", "", errors.New("unsupported fixed CLI upload destination")
	}
	return fixedMethod + "/upload/start", fixedMethod + "/upload/chunk", fixedMethod + "/upload/finish", fixedMethod + "/upload/abort", nil
}

func requestWireBound(request Request) (int, error) {
	header := request
	header.Payloads = nil
	encoded, err := json.Marshal(header)
	if err != nil {
		return 0, err
	}
	size := len(encoded) + 64
	for name, value := range request.Payloads {
		key, err := json.Marshal(name)
		if err != nil {
			return 0, err
		}
		size += len(key) + base64.StdEncoding.EncodedLen(len(value)) + 8
	}
	return size, nil
}

func inputLimit(request Request, name string) int {
	if request.Command == "send" && name == "media-file" {
		return MaxMediaBytes
	}
	return MaxPayloadBytes
}

func CallRequest(ctx context.Context, call func(context.Context, string, any, any) error, fixedMethod string, request Request, target any) error {
	startMethod, chunkMethod, finishMethod, abortMethod, err := uploadMethods(fixedMethod)
	if err != nil {
		return err
	}
	if call == nil {
		return errors.New("CLI upload requires a session caller")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Validate(request); err != nil {
		return err
	}
	size, err := requestWireBound(request)
	if err != nil {
		return err
	}
	if size <= MaxRequestBytes {
		return call(ctx, fixedMethod, request, target)
	}
	if size > MaxUploadRequestBytes {
		return errors.New("CLI request exceeds bounded upload limit")
	}
	release, err := processUploads.reserve(size*3, 1)
	if err != nil {
		return err
	}
	defer release()
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	defer clear(encoded)
	if len(encoded) <= MaxRequestBytes {
		return call(ctx, fixedMethod, json.RawMessage(encoded), target)
	}
	digest := sha256.Sum256(encoded)
	transferCtx, cancel := context.WithTimeout(ctx, UploadLifetime)
	defer cancel()
	var started UploadStarted
	err = call(transferCtx, startMethod, UploadStart{Version: 1, Size: len(encoded), SHA256: hex.EncodeToString(digest[:])}, &started)
	if err != nil {
		return err
	}
	if !validTransferID(started.TransferID) {
		return errors.New("invalid upload start response")
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		var aborted UploadAborted
		_ = call(cleanupCtx, abortMethod, UploadFinish{TransferID: started.TransferID}, &aborted)
	}()
	if started.ChunkBytes != UploadChunkBytes {
		return errors.New("invalid upload chunk size response")
	}
	for offset := 0; offset < len(encoded); {
		end := min(offset+UploadChunkBytes, len(encoded))
		var progress UploadProgress
		if err := call(transferCtx, chunkMethod, UploadChunk{TransferID: started.TransferID, Offset: offset, Data: encoded[offset:end]}, &progress); err != nil {
			return err
		}
		if progress.Received != end {
			return errors.New("invalid upload acknowledgement")
		}
		offset = end
	}
	finishCtx, finishCancel := context.WithTimeout(ctx, UploadExecutionTimeout)
	defer finishCancel()
	err = call(finishCtx, finishMethod, UploadFinish{TransferID: started.TransferID}, target)
	if err != nil {
		return err
	}
	complete = true
	return nil
}

func validTransferID(id string) bool {
	if len(id) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 32
}

type uploadTransfer struct {
	id        string
	owner     context.Context
	buffer    []byte
	received  int
	digest    [sha256.Size]byte
	deadline  time.Time
	expires   time.Time
	timer     *time.Timer
	stopOwner func() bool
	release   func()
}

type uploadHandler struct {
	next        privateipc.Handler
	fixedMethod string
	mu          sync.Mutex
	transfers   map[string]*uploadTransfer
	idleTimeout time.Duration
	lifetime    time.Duration
}

func NewUploadHandler(next privateipc.Handler, fixedMethod string) privateipc.Handler {
	if _, _, _, _, err := uploadMethods(fixedMethod); err != nil {
		panic(err)
	}
	if next == nil {
		panic("CLI upload requires a fixed next handler")
	}
	return &uploadHandler{next: next, fixedMethod: fixedMethod, transfers: map[string]*uploadTransfer{}, idleTimeout: UploadIdleTimeout, lifetime: UploadLifetime}
}

func (handler *uploadHandler) HandlePrivateRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	startMethod, chunkMethod, finishMethod, abortMethod, _ := uploadMethods(handler.fixedMethod)
	if method != startMethod && method != chunkMethod && method != finishMethod && method != abortMethod {
		return handler.next.HandlePrivateRPC(ctx, method, params)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owner, ok := privateipc.ConnectionContext(ctx)
	if !ok || owner == nil || owner.Done() == nil || owner.Err() != nil {
		return nil, errors.New("upload requires a live bound IPC connection")
	}
	if len(params) > uploadControlBytes {
		return nil, privateipc.ErrFrameTooLarge
	}
	select {
	case uploadDecoders <- struct{}{}:
	default:
		return nil, privateipc.ErrBusy
	}
	defer func() { <-uploadDecoders }()
	release, err := processUploads.reserve(uploadScratchCharge, 0)
	if err != nil {
		return nil, err
	}
	defer release()
	switch method {
	case startMethod:
		var request UploadStart
		if err := decodeUpload(params, &request); err != nil {
			return nil, err
		}
		return handler.start(owner, request)
	case chunkMethod:
		var request UploadChunk
		if err := decodeUpload(params, &request); err != nil {
			return nil, err
		}
		defer clear(request.Data)
		return handler.chunk(owner, request)
	case finishMethod:
		var request UploadFinish
		if err := decodeUpload(params, &request); err != nil {
			return nil, err
		}
		return handler.finish(ctx, owner, request)
	case abortMethod:
		var request UploadFinish
		if err := decodeUpload(params, &request); err != nil {
			return nil, err
		}
		return handler.abort(owner, request)
	}
	return nil, privateipc.ErrMethodNotFound
}

func decodeUpload(data []byte, target any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return errors.New("invalid typed CLI upload object")
	}
	allowed := map[string]bool{}
	switch target.(type) {
	case *UploadStart:
		allowed = map[string]bool{"version": true, "size": true, "sha256": true}
	case *UploadChunk:
		allowed = map[string]bool{"transferId": true, "offset": true, "data": true}
	case *UploadFinish:
		allowed = map[string]bool{"transferId": true}
	default:
		return errors.New("unsupported upload control type")
	}
	if len(fields) != len(allowed) {
		return errors.New("missing upload control field")
	}
	for name, value := range fields {
		if !allowed[name] {
			return errors.New("unknown upload control field")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("null upload control field")
		}
	}
	if err := privateipc.DecodeStrict(data, target, true); err != nil {
		return errors.New("invalid typed CLI upload request")
	}
	return nil
}

func (handler *uploadHandler) start(owner context.Context, request UploadStart) (UploadStarted, error) {
	digest, err := hex.DecodeString(request.SHA256)
	if err != nil || len(digest) != sha256.Size || request.Version != 1 || request.Size <= MaxRequestBytes || request.Size > MaxUploadRequestBytes {
		return UploadStarted{}, errors.New("invalid upload size, version, or digest")
	}
	release, err := processUploads.reserve(request.Size*8, 1)
	if err != nil {
		return UploadStarted{}, err
	}
	identifier := make([]byte, 32)
	if _, err := rand.Read(identifier); err != nil {
		release()
		return UploadStarted{}, err
	}
	transfer := &uploadTransfer{id: hex.EncodeToString(identifier), owner: owner, buffer: make([]byte, request.Size), release: release}
	copy(transfer.digest[:], digest)
	now := time.Now()
	transfer.expires = now.Add(handler.lifetime)
	transfer.deadline = minDeadline(now.Add(handler.idleTimeout), transfer.expires)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if owner.Err() != nil {
		clear(transfer.buffer)
		release()
		return UploadStarted{}, owner.Err()
	}
	handler.transfers[transfer.id] = transfer
	transfer.timer = time.AfterFunc(time.Until(transfer.deadline), func() { handler.expire(transfer.id) })
	transfer.stopOwner = context.AfterFunc(owner, func() { handler.discard(transfer.id) })
	return UploadStarted{TransferID: transfer.id, ChunkBytes: UploadChunkBytes}, nil
}

func minDeadline(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func (handler *uploadHandler) owned(owner context.Context, id string) (*uploadTransfer, error) {
	transfer := handler.transfers[id]
	if !validTransferID(id) || transfer == nil || transfer.owner.Done() != owner.Done() {
		return nil, errors.New("unknown upload for this connection")
	}
	if transfer.owner.Err() != nil || !time.Now().Before(transfer.deadline) {
		handler.remove(transfer)
		return nil, errors.New("upload expired or disconnected")
	}
	return transfer, nil
}

func (handler *uploadHandler) chunk(owner context.Context, request UploadChunk) (UploadProgress, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	transfer, err := handler.owned(owner, request.TransferID)
	if err != nil {
		return UploadProgress{}, err
	}
	if request.Offset != transfer.received || len(request.Data) == 0 || len(request.Data) > UploadChunkBytes || len(request.Data) > len(transfer.buffer)-transfer.received {
		handler.remove(transfer)
		return UploadProgress{}, errors.New("invalid upload chunk size or sequence")
	}
	copy(transfer.buffer[transfer.received:], request.Data)
	transfer.received += len(request.Data)
	transfer.deadline = minDeadline(time.Now().Add(handler.idleTimeout), transfer.expires)
	transfer.timer.Reset(time.Until(transfer.deadline))
	return UploadProgress{Received: transfer.received}, nil
}

func (handler *uploadHandler) detach(transfer *uploadTransfer) {
	delete(handler.transfers, transfer.id)
	transfer.timer.Stop()
	transfer.stopOwner()
}

func (handler *uploadHandler) remove(transfer *uploadTransfer) {
	handler.detach(transfer)
	clear(transfer.buffer)
	transfer.buffer = nil
	transfer.release()
}

func (handler *uploadHandler) discard(id string) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if transfer := handler.transfers[id]; transfer != nil {
		handler.remove(transfer)
	}
}

func (handler *uploadHandler) expire(id string) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	transfer := handler.transfers[id]
	if transfer == nil {
		return
	}
	if time.Now().Before(transfer.deadline) {
		transfer.timer.Reset(time.Until(transfer.deadline))
		return
	}
	handler.remove(transfer)
}

func (handler *uploadHandler) abort(owner context.Context, request UploadFinish) (UploadAborted, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	transfer, err := handler.owned(owner, request.TransferID)
	if err != nil {
		return UploadAborted{}, err
	}
	handler.remove(transfer)
	return UploadAborted{Aborted: true}, nil
}

func (handler *uploadHandler) finish(ctx, owner context.Context, request UploadFinish) (any, error) {
	handler.mu.Lock()
	transfer, err := handler.owned(owner, request.TransferID)
	if err != nil {
		handler.mu.Unlock()
		return nil, err
	}
	if transfer.received != len(transfer.buffer) || sha256.Sum256(transfer.buffer) != transfer.digest {
		handler.remove(transfer)
		handler.mu.Unlock()
		return nil, errors.New("upload incomplete or checksum mismatch")
	}
	handler.detach(transfer)
	handler.mu.Unlock()
	defer func() { clear(transfer.buffer); transfer.buffer = nil; transfer.release() }()
	if _, err := DecodeRequest(transfer.buffer); err != nil {
		return nil, err
	}
	executeCtx, cancel := context.WithTimeout(ctx, UploadExecutionTimeout)
	defer cancel()
	stopOwner := context.AfterFunc(owner, cancel)
	defer stopOwner()
	if err := owner.Err(); err != nil {
		return nil, err
	}
	if err := executeCtx.Err(); err != nil {
		return nil, err
	}
	result, err := handler.next.HandlePrivateRPC(executeCtx, handler.fixedMethod, json.RawMessage(transfer.buffer))
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxRequestBytes {
		return nil, privateipc.ErrFrameTooLarge
	}
	return json.RawMessage(encoded), nil
}

func uploadLimitError(name string, limit int) error {
	return fmt.Errorf("--%s input exceeds %d MiB", name, limit/(1024*1024))
}
