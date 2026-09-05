# Bounded CLI uploads

The approved upload protocol restores `send --media-file` inputs up to 30 MiB
without changing `privateipc.MaxFrameBytes` (4 MiB). Ordinary private inputs keep
their aggregate 4 MiB bound. The transport does not use files, queue records,
configuration, credentials, remote networking, or application startup.

## Integration

```go
gatewayHandler := feishucli.NewUploadHandler(gateway, feishucli.MethodExecute)
daemonHandler := feishucli.NewUploadHandler(daemon, feishuprotocol.ClientExecute)
err := feishucli.CallRequest(ctx, session.Call, fixedMethod, request, &result)
```

`localipc.Open` creates one authenticated session. All calls for one upload use
that session. Core-to-daemon forwarding uses one generation-bound supervisor
context: a restart cancels the upload rather than resuming it on another peer.
`privateipc.ConnectionContext` supplies the stable connection owner; ordinary
per-RPC contexts cannot own an upload because they are cancelled after each call.

The constructor and caller only accept `cli/execute` or `bridge/client/execute`
as their fixed method. Wire callers cannot choose a destination. Non-upload
methods continue to the existing next handler and its own method allowlist.

## Wire sequence

The protocol transfers the serialized typed `Request`, not argv or an arbitrary
RPC method. For a fixed destination `M`, the reviewed methods are:

1. `M/upload/start`: `UploadStart{Version: 1, Size, SHA256}`. The server checks the
   bound, reserves memory, and returns a cryptographically random 32-byte transfer
   ID and a fixed chunk size of 256 KiB.
2. `M/upload/chunk`: `UploadChunk{TransferID, Offset, Data}`. Data is base64 encoded
   by JSON. Offset must exactly equal the received byte count. Duplicate,
   out-of-order, empty, oversized, and overflowing chunks are rejected; invalid
   sequencing consumes the transfer instead of allowing an ambiguous retry.
3. `M/upload/finish`: `UploadFinish{TransferID}`. The full length and SHA-256 must
   match. The assembled request is strictly decoded and validated before invoking
   the constructor-bound method exactly once. The normal JSON result is returned.
4. `M/upload/abort`: the same identity-only input explicitly discards a partial
   upload. The client makes a bounded best-effort abort when an upload fails.

Control messages reject unknown/duplicate fields and unsupported versions.
Transfer IDs are bound to both the handler and connection lifetime; knowing an ID
does not authorize another connection to append, finish, or abort it. Finished
IDs are removed before execution, so concurrent or later finishes cannot replay
the action. An uncertain execution outcome must not automatically start another
upload or repeat the business operation.

## Bounds and cleanup

- Assembled JSON: at most 44 MiB. Individual RPC params retain 64 KiB of headroom
  below the existing 4 MiB frame limit; upload controls are at most 512 KiB.
- One shared process-wide reservation budget: 512 MiB across all handler instances
  and callers, with at most eight transfer reservations. Receivers reserve eight
  times the declared JSON size for assembly and temporary decoding/execution
  copies; senders reserve three times the encoded upper bound. Control processing
  reserves 2 MiB, with at most eight control handlers active. Capacity failures
  return the existing explicit busy error before allocating a transfer buffer.
- These are conservative transport memory reservations, not a cap on the entire
  Go process RSS or unrelated SDK/application allocations. Buffers are allocated
  at their declared final size, never grown with an unbounded append.
- Partial uploads expire after 30 seconds idle or two minutes absolute lifetime.
  Disconnect cancels their owner and immediately discards their buffers. Finish
  execution is bounded to four minutes and inherits caller/connection cancellation.
  An executing handler retains its reservation until it returns, avoiding buffer
  races or releasing capacity while an operation still uses the data.
- Terminal cleanup stops timers and owner callbacks, clears owned buffers,
  removes IDs, and returns reservations. Assembly never starts command execution
  or touches storage; existing authorized command handling only begins at finish.

The budget intentionally applies across wrappers in the same process. Tests cover
shared-capacity rejection instead of increasing the limit to emulate several
production processes in one test process. The 30 MiB two-hop test uses isolated
test CLI, gateway, and daemon process boundaries, matching real ownership; no
desktop application, production configuration, or external API is involved.
