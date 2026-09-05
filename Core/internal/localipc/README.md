# Core local gateway

```go
server, err := localipc.Listen(dataRoot, handler)
defer server.Close()
err = localipc.Call(ctx, dataRoot, "cli/execute", request, &response)
```

`handler` implements `privateipc.Handler`. The gateway reserves `localipc/hello`
for its typed protocol/data-root handshake. Application method dispatch,
authorization and DTO validation remain in the handler; the gateway does not
interpret Feishu or Core business contracts. Both sides reuse `privateipc.Peer`.

## Multi-call sessions

```go
session, err := localipc.Open(ctx, dataRoot)
if err != nil {
    return err
}
defer session.Close()
err = session.Call(ctx, method, params, &result)
```

`Open(ctx, root) (*Session, error)` connects and authenticates exactly once.
`Dial` is an equivalent entry point for the CLI caller; both use the same Open
implementation and return the same Session type.
All `Session.Call(ctx, method, params, target) error` calls share that same Peer
and physical connection, including concurrent calls. Chunked upload begin,
append and execute operations must use one Session. No reconnect, upload replay
or cross-connection retry occurs implicitly. Server restart/EOF retires the
session; the caller must explicitly open another and restart its own workflow.

The Open context controls only connection setup/authentication; cancelling it
after a successful Open does not close the Session. Each Call context controls
that request. Call cancellation while awaiting a response does not close the
session, whereas a transport/write failure may. `Close() error` is idempotent,
closes the connection and cancels pending requests. Always defer it.
The one-shot `localipc.Call` helper now uses Open/Call/Close internally.
`localipc/hello` cannot be reissued on an authenticated connection.
Inbound handlers can use `privateipc.ConnectionContext(ctx)` for stable upload
ownership and disconnect cleanup; individual request contexts must not be used
as the upload owner.

## Endpoint ownership

- Only Core calls `Listen`. A client never starts the application, creates a
  listener, directory, lock, queue or offline request file. Dial failure wraps
  `ErrNotRunning`; context errors and remote `*privateipc.RPCError` stay typed.
- Root identity is absolute and symlink-resolved, including existing ancestors
  of a missing root. A connection cannot execute application methods before
  matching both the root identity and `ksfassistant-localipc-v1` protocol.
  macOS also uses the filesystem's canonical path spelling so case aliases on
  case-insensitive volumes cannot acquire separate listeners.
- macOS uses `/tmp/ksfa-<uid>/<root-hash>/rpc.sock`. The user-owned directories
  must be exactly `0700`, socket `0600`; symlinks and unsafe ownership/modes are
  rejected rather than repaired. Hashing avoids Unix socket path-length limits.
- A private `0600` flock file gives one server per root. Only the lock owner
  reclaims a stale socket. `Close` removes the socket before releasing the lock;
  the empty lock file remains to prevent inode-replacement races. It contains
  no request or application data. Clients do not create or modify it.
- Windows uses go-winio named pipes, a protected current-process-user SID-only
  DACL, and go-winio's first-instance exclusive creation. Remote pipe clients
  are rejected by go-winio. There is no TCP fallback or filesystem queue.
- At most 32 client connections are admitted. Each Peer bounds handlers,
  pending calls and writes. An unverified connection has a five-second read
  deadline. `Close` is idempotent, closes all connections and cancels handlers;
  handlers must honor their contexts and must not launch detached work.

Tests use temporary roots and local fixtures only. Windows ACL tests query the
created pipe's actual DACL, but must be run on Windows; cross-compilation is not
an ACL runtime validation.
