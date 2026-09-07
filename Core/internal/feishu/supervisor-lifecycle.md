# Supervisor connection generations

## Managed message and card connection

Feishu service owns the existing official CLI message/card inbound lifecycle;
there is no configurable `primary`/`manual-only` event role or additional listener.
The pinned CLI version/schema probe and official Feishu bot identity must pass
before the message client is created. The existing inbound processor, authorized
sender/operator checks, two managed consumers (`im.message.receive_v1` and
`card.action.trigger`), readiness checks and process-tree cleanup remain intact.
Removing an event role does not grant authorization, enable additional event
subscriptions, activate outbound writes or bypass capability/approval policy.
Legacy `manual-only` no longer suppresses the authorized inbound lifecycle.

`feishu/profile/set`, `bridge/profile/set` and CLI `profile set` are retired;
they cannot save settings or restart the service. Compatibility profile reads and
catalogs expose fixed managed intent (`profile: managed`, `profileValid: true`,
`desiredConnection: true`, `managedBy: feishu-service`, `configurable: false`).
Catalogs have no selectable profiles. These fields do not claim a live connection
or authorization; `inboundConnection`, `health.inbound`, readiness blockers and
capability health still describe actual readiness. Snapshot field names/types are
retained for old hosts. `auth.profile=default` is a separate official CLI identity
configuration and is unchanged.

Settings schema stays v1. The old string `profile` field is read-only migration
data: Load does not rewrite it, Save retains the stored value regardless of the
incoming value, and new settings keep an empty string for decoder compatibility.
Safe legacy labels are not interpreted as runtime roles. Existing strict JSON
unknown-field/trailing-data and private-file checks remain; unknown settings are
rejected rather than silently dropped or overwritten. No installed settings,
credentials or production data are migrated by a read or by this source change.

## Connection generations

```go
err := supervisor.SetOnConnect(func(ctx context.Context, generation uint64) error {
    return initializeAndReadSnapshot(ctx, generation)
})
generation := feishu.EpochFromContext(ctx)
current := supervisor.IsCurrentGeneration(generation)
ctx = feishu.WithEpoch(ctx, generation)
```

`SupervisorOptions.OnConnect` has the same signature as the setter. Install the
callback and private RPC handler before `Start`; replacement while running or
waiting for an automatic restart is rejected. No Core/Feishu wire DTO is changed.

Every successfully attached process/pipe pair receives a monotonically increasing
nonzero `uint64` generation, exposed by `Generation()` and `Status().Generation`.
Both automatic and manual restarts invoke the callback once for the new connection,
asynchronously and without holding the supervisor mutex. The callback may call
`Status`, `Call` or `SetConfigured`. Its context carries the epoch and is cancelled
on EOF, process exit, initialization failure or stop. A queued callback can observe
an already-cancelled context and must not publish results for that generation.

With a callback installed, state remains `starting` until initialization succeeds;
then it becomes `running` or `idle_unconfigured`. Callback failure retires the
connection and uses the same bounded restart budget as child failures. Callback
code owns initialization timeouts and must honor cancellation. There is no implicit
business initialization in the supervisor.

Each inbound wrapper binds the actual connection generation, validates it before
dispatch and after handler completion, and injects it into the handler context.
Outbound `Call` rejects an explicit stale epoch, binds the selected peer, and checks
the generation again before decoding a successful result into the target. The
typed `ErrStaleGeneration` code is `-32030`. Zero means an unbound caller, not a
valid connection generation.

Service must keep initialization and snapshot state generation-scoped. Inbound
handler entry checks cannot undo application writes already in progress: compare
the epoch in service's own serialized snapshot update before publishing asynchronous
results. Do not treat `Generation()` alone as liveness; use `IsCurrentGeneration`
and context cancellation. The last numeric generation remains visible when stopped.

Stop invalidates the context and closes IPC before signalling the process tree.
It cancels pending restarts, waits for process reap, and kills after the grace period.
A caller deadline forces a kill and returns its context error; state stays stopping
until reap. Unix process-group cleanup also runs after unexpected parent exit so
orphan descendants cannot survive into the next generation. Windows retains the
existing kill-on-close job-object ownership.
