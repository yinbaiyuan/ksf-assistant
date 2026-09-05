// Package privateipc implements the bounded, bidirectional JSON-RPC transport
// used exclusively between the CodexAssistant Core and its managed Feishu child.
package privateipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

const MaxFrameBytes = 4 * 1024 * 1024

var (
	ErrPeerClosed     = errors.New("private IPC peer closed")
	ErrFrameTooLarge  = errors.New("private IPC frame exceeds 4 MiB")
	ErrMethodNotFound = NewError(-32601, "method not found")
)

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (err *RPCError) Error() string {
	if err == nil {
		return ""
	}
	return err.Message
}

func NewError(code int, message string) *RPCError {
	return &RPCError{Code: code, Message: message}
}

type Handler interface {
	HandlePrivateRPC(context.Context, string, json.RawMessage) (any, error)
}

type HandlerFunc func(context.Context, string, json.RawMessage) (any, error)

func (handler HandlerFunc) HandlePrivateRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return handler(ctx, method, params)
}

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type pendingCall struct {
	response chan frame
}

type Peer struct {
	reader  io.Reader
	writer  io.Writer
	handler Handler

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]pendingCall
	active  map[string]context.CancelFunc
	closed  chan struct{}
	close   sync.Once
	nextID  atomic.Uint64
}

func NewPeer(reader io.Reader, writer io.Writer, handler Handler) *Peer {
	return &Peer{
		reader:  reader,
		writer:  writer,
		handler: handler,
		pending: map[string]pendingCall{},
		active:  map[string]context.CancelFunc{},
		closed:  make(chan struct{}),
	}
}

func (peer *Peer) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(peer.reader)
	scanner.Buffer(make([]byte, 64*1024), MaxFrameBytes)
	defer peer.closePeer()
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(line) > MaxFrameBytes {
			return ErrFrameTooLarge
		}
		peer.accept(ctx, line)
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) || len(scanner.Bytes()) >= MaxFrameBytes {
			return ErrFrameTooLarge
		}
		return err
	}
	return nil
}

func (peer *Peer) Call(ctx context.Context, method string, params any, target any) error {
	if method == "" {
		return errors.New("private IPC method is required")
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode private IPC params: %w", err)
	}
	id := strconv.FormatUint(peer.nextID.Add(1), 10)
	response := make(chan frame, 1)
	peer.mu.Lock()
	select {
	case <-peer.closed:
		peer.mu.Unlock()
		return ErrPeerClosed
	default:
	}
	peer.pending[id] = pendingCall{response: response}
	peer.mu.Unlock()

	rawID, _ := json.Marshal(id)
	if err := peer.write(frame{JSONRPC: "2.0", ID: rawID, Method: method, Params: encoded}); err != nil {
		peer.removePending(id)
		return err
	}
	select {
	case reply := <-response:
		if reply.Error != nil {
			return reply.Error
		}
		if target == nil || len(reply.Result) == 0 || string(reply.Result) == "null" {
			return nil
		}
		if err := DecodeStrict(reply.Result, target, true); err != nil {
			return fmt.Errorf("decode private IPC result for %s: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		peer.removePending(id)
		_ = peer.Notify(context.Background(), "$/cancelRequest", map[string]string{"id": id})
		return ctx.Err()
	case <-peer.closed:
		peer.removePending(id)
		return ErrPeerClosed
	}
}

func (peer *Peer) Notify(ctx context.Context, method string, params any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode private IPC notification: %w", err)
	}
	return peer.write(frame{JSONRPC: "2.0", Method: method, Params: encoded})
}

func (peer *Peer) accept(parent context.Context, line []byte) {
	var incoming frame
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	if err := decoder.Decode(&incoming); err != nil {
		_ = peer.write(frame{JSONRPC: "2.0", Error: NewError(-32700, "invalid JSON")})
		return
	}
	if incoming.JSONRPC != "2.0" {
		_ = peer.write(frame{JSONRPC: "2.0", ID: incoming.ID, Error: NewError(-32600, "invalid JSON-RPC request")})
		return
	}
	if incoming.Method == "" {
		peer.deliver(incoming)
		return
	}
	if incoming.Method == "$/cancelRequest" {
		var request struct {
			ID string `json:"id"`
		}
		if DecodeStrict(incoming.Params, &request, true) == nil {
			peer.cancelActive(request.ID)
		}
		return
	}
	go peer.dispatch(parent, incoming)
}

func (peer *Peer) dispatch(parent context.Context, request frame) {
	id := idKey(request.ID)
	ctx, cancel := context.WithCancel(parent)
	if id != "" {
		peer.mu.Lock()
		peer.active[id] = cancel
		peer.mu.Unlock()
	}
	defer func() {
		cancel()
		if id != "" {
			peer.mu.Lock()
			delete(peer.active, id)
			peer.mu.Unlock()
		}
	}()
	if peer.handler == nil {
		if id != "" {
			_ = peer.write(frame{JSONRPC: "2.0", ID: request.ID, Error: ErrMethodNotFound})
		}
		return
	}
	result, err := peer.handler.HandlePrivateRPC(ctx, request.Method, request.Params)
	if id == "" {
		return
	}
	if err != nil {
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) {
			rpcErr = NewError(-32000, err.Error())
		}
		_ = peer.write(frame{JSONRPC: "2.0", ID: request.ID, Error: rpcErr})
		return
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		_ = peer.write(frame{JSONRPC: "2.0", ID: request.ID, Error: NewError(-32603, "unable to encode result")})
		return
	}
	_ = peer.write(frame{JSONRPC: "2.0", ID: request.ID, Result: encoded})
}

func (peer *Peer) write(value frame) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > MaxFrameBytes {
		return ErrFrameTooLarge
	}
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	select {
	case <-peer.closed:
		return ErrPeerClosed
	default:
	}
	encoded = append(encoded, '\n')
	_, err = peer.writer.Write(encoded)
	if err != nil {
		return fmt.Errorf("write private IPC frame: %w", err)
	}
	return nil
}

func (peer *Peer) deliver(reply frame) {
	id := idKey(reply.ID)
	peer.mu.Lock()
	call, ok := peer.pending[id]
	if ok {
		delete(peer.pending, id)
	}
	peer.mu.Unlock()
	if ok {
		call.response <- reply
	}
}

func (peer *Peer) removePending(id string) {
	peer.mu.Lock()
	delete(peer.pending, id)
	peer.mu.Unlock()
}

func (peer *Peer) cancelActive(id string) {
	peer.mu.Lock()
	cancel := peer.active[id]
	peer.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (peer *Peer) closePeer() {
	peer.close.Do(func() {
		close(peer.closed)
		peer.mu.Lock()
		for id, cancel := range peer.active {
			cancel()
			delete(peer.active, id)
		}
		peer.mu.Unlock()
	})
}

func idKey(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}
