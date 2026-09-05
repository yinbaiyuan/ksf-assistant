# Bounded private RPC transport

`NewPeer(reader, writer, handler)` retains the existing `Call`, `Notify`, `Serve`
and typed `RPCError` contract. `Close() error` is idempotent and `Done()` signals
retirement. The Peer owns its reader/writer closers. Do not reuse a closed transport.

`ConnectionContext(ctx) (context.Context, bool)` retrieves the stable context for
the actual serving connection from an inbound handler context. All requests on
one connection share its Done channel; different Peers never do. It is cancelled
on EOF, Close or Serve-context cancellation, not when one request finishes or is
cancelled. Direct/unbound contexts return `(nil, false)`. This allows connection-
scoped upload ownership and cleanup without trusting IDs supplied by clients.

- Maximum frame: 4 MiB; handlers: 32; pending outbound calls: 64; queued writes: 64.
- Incoming overload returns typed `ErrBusy` (`-32029`) when possible. Cancellation
  and response dispatch bypass handler slots. Active IDs are registered before
  dispatch, so an immediately following cancellation cannot overtake registration.
- A single writer serializes frames. Queue waits and writes observe caller context;
  each write also has a five-second ceiling. A transport timeout/error retires the
  peer instead of allowing an uncertain partial frame to corrupt later traffic.
- Cancel notification is best effort and nonblocking. EOF/Close cancels requests,
  notifications and pending callers. Handlers must honor their context.
- Production transports must implement prompt `Close` and preferably write
  deadlines (OS pipes, UDS and named pipes do). An arbitrary non-closable `io.Writer`
  cannot be forcibly interrupted by Go; callers still return on timeout, with at
  most one blocked writer goroutine per such Peer, not one per request.
- Fixed envelopes/DTOs reject unknown fields and trailing JSON values, use
  `json.Number`, preserve typed RPC errors, and enforce the frame-size bound.
