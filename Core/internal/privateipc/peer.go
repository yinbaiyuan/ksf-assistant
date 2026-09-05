// Package privateipc implements the bounded, bidirectional JSON-RPC transport
// used by KSFAssistant's managed child pipes and private local gateway.
package privateipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const MaxFrameBytes = 4 * 1024 * 1024

const MaxConcurrentHandlers = 32
const MaxPendingCalls = 64
const MaxQueuedWrites = 64
const WriteTimeout = 5 * time.Second

var (
	ErrPeerClosed     = errors.New("private IPC peer closed")
	ErrFrameTooLarge  = errors.New("private IPC frame exceeds 4 MiB")
	ErrMethodNotFound = NewError(-32601, "method not found")
	ErrBusy           = NewError(-32029, "private IPC capacity exceeded")
)

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
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

type connectionContextKey struct{}

func ConnectionContext(ctx context.Context) (context.Context, bool) {
	if ctx == nil {
		return nil, false
	}
	connection, ok := ctx.Value(connectionContextKey{}).(context.Context)
	return connection, ok
}

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

type writeRequest struct {
	ctx  context.Context
	data []byte
	done chan error
}

type Peer struct {
	reader  io.Reader
	writer  io.Writer
	handler Handler

	writes      chan writeRequest
	writerStart sync.Once
	slots       chan struct{}
	mu          sync.Mutex
	pending     map[string]pendingCall
	active      map[string]context.CancelFunc
	closed      chan struct{}
	close       sync.Once
	nextID      atomic.Uint64
}

func NewPeer(reader io.Reader, writer io.Writer, handler Handler) *Peer {
	return &Peer{
		reader:  reader,
		writer:  writer,
		handler: handler,
		pending: map[string]pendingCall{},
		active:  map[string]context.CancelFunc{},
		closed:  make(chan struct{}),
		writes:  make(chan writeRequest, MaxQueuedWrites),
		slots:   make(chan struct{}, MaxConcurrentHandlers),
	}
}

func (peer *Peer) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = context.WithValue(ctx, connectionContextKey{}, ctx)
	stop := context.AfterFunc(ctx, peer.closePeer)
	defer stop()
	go func() {
		<-peer.closed
		cancel()
	}()
	scanner := bufio.NewScanner(peer.reader)
	scanner.Buffer(make([]byte, 64*1024), MaxFrameBytes+2)
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
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if len(peer.pending) >= MaxPendingCalls {
		peer.mu.Unlock()
		return ErrBusy
	}
	peer.pending[id] = pendingCall{response: response}
	peer.mu.Unlock()

	rawID, _ := json.Marshal(id)
	if err := peer.write(ctx, frame{JSONRPC: "2.0", ID: rawID, Method: method, Params: encoded}); err != nil {
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
		encoded, _ := json.Marshal(map[string]string{"id": id})
		peer.enqueue(frame{JSONRPC: "2.0", Method: "$/cancelRequest", Params: encoded})
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
	return peer.write(ctx, frame{JSONRPC: "2.0", Method: method, Params: encoded})
}

func (peer *Peer) accept(parent context.Context, line []byte) {
	var incoming frame
	if err := DecodeStrict(line, &incoming, true); err != nil {
		peer.enqueue(frame{JSONRPC: "2.0", Error: NewError(-32700, "invalid JSON")})
		return
	}
	if incoming.JSONRPC != "2.0" {
		peer.enqueue(frame{JSONRPC: "2.0", ID: incoming.ID, Error: NewError(-32600, "invalid JSON-RPC request")})
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
	select {
	case peer.slots <- struct{}{}:
	default:
		if idKey(incoming.ID) != "" {
			peer.enqueue(frame{JSONRPC: "2.0", ID: incoming.ID, Error: ErrBusy})
		}
		return
	}
	ctx, cancel := context.WithCancel(parent)
	id := idKey(incoming.ID)
	peer.mu.Lock()
	if _, duplicate := peer.active[id]; id != "" && duplicate {
		peer.mu.Unlock()
		cancel()
		<-peer.slots
		peer.enqueue(frame{JSONRPC: "2.0", ID: incoming.ID, Error: NewError(-32600, "duplicate active request id")})
		return
	}
	if id != "" {
		peer.active[id] = cancel
	}
	peer.mu.Unlock()
	go peer.dispatch(ctx, cancel, incoming)
}

func (peer *Peer) dispatch(ctx context.Context, cancel context.CancelFunc, request frame) {
	id := idKey(request.ID)
	defer func() {
		<-peer.slots
		cancel()
		if id != "" {
			peer.mu.Lock()
			delete(peer.active, id)
			peer.mu.Unlock()
		}
	}()
	if ctx.Err() != nil {
		return
	}
	if peer.handler == nil {
		if id != "" {
			_ = peer.write(ctx, frame{JSONRPC: "2.0", ID: request.ID, Error: ErrMethodNotFound})
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
		_ = peer.write(ctx, frame{JSONRPC: "2.0", ID: request.ID, Error: rpcErr})
		return
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		_ = peer.write(ctx, frame{JSONRPC: "2.0", ID: request.ID, Error: NewError(-32603, "unable to encode result")})
		return
	}
	_ = peer.write(ctx, frame{JSONRPC: "2.0", ID: request.ID, Result: encoded})
}

func (peer *Peer) write(ctx context.Context, value frame) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > MaxFrameBytes {
		return ErrFrameTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, WriteTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	peer.writerStart.Do(func() { go peer.writeLoop() })
	request := writeRequest{ctx: ctx, data: append(encoded, '\n'), done: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-peer.closed:
		return ErrPeerClosed
	case peer.writes <- request:
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		peer.closePeer()
		return ctx.Err()
	case <-peer.closed:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrPeerClosed
	}
}

func (peer *Peer) enqueue(value frame) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxFrameBytes {
		peer.closePeer()
		return
	}
	peer.writerStart.Do(func() { go peer.writeLoop() })
	select {
	case <-peer.closed:
	case peer.writes <- writeRequest{ctx: context.Background(), data: append(encoded, '\n'), done: make(chan error, 1)}:
	default:
		peer.closePeer()
	}
}

func (peer *Peer) writeLoop() {
	for {
		select {
		case <-peer.closed:
			return
		case request := <-peer.writes:
			select {
			case <-peer.closed:
				return
			default:
			}
			if err := request.ctx.Err(); err != nil {
				request.done <- err
				continue
			}
			ctx, cancel := context.WithTimeout(request.ctx, WriteTimeout)
			stop := context.AfterFunc(ctx, peer.closePeer)
			if writer, ok := peer.writer.(interface{ SetWriteDeadline(time.Time) error }); ok {
				deadline, _ := ctx.Deadline()
				_ = writer.SetWriteDeadline(deadline)
			}
			written, err := peer.writer.Write(request.data)
			if err == nil && written != len(request.data) {
				err = io.ErrShortWrite
			}
			stop()
			if ctx.Err() != nil {
				err = ctx.Err()
			} else if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				err = context.DeadlineExceeded
			}
			cancel()
			request.done <- err
			if err != nil {
				peer.closePeer()
				return
			}
		}
	}
}

func (peer *Peer) Close() error { peer.closePeer(); return nil }

func (peer *Peer) Done() <-chan struct{} { return peer.closed }

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
		if reader, ok := peer.reader.(io.Closer); ok {
			_ = reader.Close()
		}
		if writer, ok := peer.writer.(io.Closer); ok {
			_ = writer.Close()
		}
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
