# Core business integration

`integration` owns task links, Plan/input/follow-up behavior, task observation,
business cards and Core event execution. It imports only local neutral contracts
and `privatestore`; it does not import the Feishu implementation, SDK, CLI,
credentials or private IPC implementation.

## Host wiring

- `NewRuntime(dataRoot, feishuPort, corePort)` constructs without dispatching.
- Call `ResumeActive()` after the Feishu handshake. It resumes task observation
  and starts the event workers; `ResumeEvents()` starts only event workers.
- `AcceptEvent(ctx, feishuprotocol.Event)` accepts `message` and `card` payloads
  using `feishutypes.InboundMessage` and `feishutypes.InboundCardAction`. Keep the
  original event ID. The returned `Accepted` acknowledges durable receipt only.
- `HandleMessage` and `HandleCard` are execution entry points, not transport ACK
  handlers. The daemon must use `AcceptEvent`, not call them directly.
- `CreateTaskLink`, `Release`, `Interrupt`, `Store`, `PublicLinks`,
  `ApplyInputOutcome` and `ReconcileTaskLinkCards` support the Core gateway.
- `Health()` returns `RuntimeHealth{State, Detail}` for snapshot composition,
  including durable-write failures and outcomes requiring reconciliation.
- `ResumeActive()` also starts one maintenance worker. Every 30 seconds it
  reconciles cards and cleans task-link history and expired inbox receipts.
- Call `Close()` before replacing the runtime. It cancels and joins its workers
  and task observers without marking running Codex tasks failed.

`CorePort` exposes the former capability client's local Codex methods with
exported names. `FeishuPort` carries outbound messages, cards, target resolution,
`StageInbound` and `CleanupInbound`. Actual resource parsing, download, path
validation and cleanup live in `feishu/inbound_media.go`; the Core module only
formats staged asset references into the prompt.

Neutral JSON contracts live in `internal/feishutypes`; private JSON, file locking
and atomic replacement helpers live in `internal/privatestore`.

## Codex runtime and turn outcome

The Core resolves an explicit `CODEX_BIN` first, then installed Desktop bundle
runtimes, then PATH and standalone CLI locations. This keeps bridge-owned turns
on the Desktop runtime when an older standalone CLI cannot execute the model
selected in the shared Codex configuration. No model or account settings change.

A Codex `completed` status alone does not prove the request was handled. Both
bridge observation and Desktop card reconciliation require a nonempty final
agent reply and no turn error before displaying success. Legacy agent messages
without a phase remain supported; commentary alone is not a final reply. Empty
completion is displayed as failed with an explanation and can be retried by the
user. It never triggers an automatic replay of the original request.

## Persistence and execution safety

Task storage remains `task-links-v1.json`, protocol
`codex-feishu-task-link-v1`, schema 2. Existing task/link identifiers and unknown
envelope/link JSON fields survive updates. Only Core accesses this store.

The Core inbox is `integration-events-v1.json`. Acceptance is persisted before
dispatch and ACK. Duplicate IDs return the original acceptance; an ID with a
different payload is rejected. Four fixed workers process conversation-hashed
partitions in FIFO order. Runtime action locks also serialize shared task keys.

Only `pending` records may execute. A record is durably marked `running` before
the handler is called. Restarted `running` records and handler errors whose
effects cannot be proven absent become `outcome_unknown`, never automatic
retries. Invalid inactive-card execution is terminally rejected. Manual outcome
reconciliation belongs to the Core host; accepting a duplicate does not retry it.
Persistence failure after execution retries only the receipt write, not the
handler. Terminal payloads are scrubbed; dedupe tombstones are retained for
30 days and then pruned on acceptance and by periodic maintenance. Pending or
running events are never retention-pruned. Transport delivery retention must
respect this 30-day dedupe window. Inbox limits (2,048 receipts, 900 KiB
serialized, 64 KiB per payload) apply visible backpressure rather than dropping
unexpired receipts. Claim/outcome/retention persistence failures surface through
`Health()` and never cause the handler to run without its durable checkpoint.

Released and expired cards cannot control Codex. The management `Release` API
returns the existing released projection on replay, with no remote action.

## Daemon delivery boundary

`feishu.InboundProcessor` only delivers events to Core and waits for a durable
ACK. Lost ACKs may be retried for both messages and cards because Core owns
deduplication. It has four fixed workers, bounded buffers, fresh authorization
reads on every attempt and an exported `Close()` that cancels/joins workers.
Undelivered buffer overflow remains in the existing daemon workbox for recovery.

Offline validation: `GOPROXY=off GOSUMDB=off go test -race ./internal/integration`
and the Feishu `TestDelivery`, `TestInboundProcessor`, `TestNormalizeInbound`,
`TestStageInboundMessage` tests. All new execution tests use fake ports.

## Card delivery and readiness

Task observation persists a latest-value card mailbox independently of network
delivery. A per-link worker checks it every two seconds; the existing sync lock
serializes workers, explicit actions and recovery, and reloads the latest link
under the lock. Updates that arrive during a send replace the pending version,
not a FIFO backlog. Terminal delivery is recorded only after a successful patch
of the still-current card. Bridge-owned turns use the same public progress
projection. Public commentary and partial final answers are included; analysis,
unknown phases and tool payloads are excluded. Completion still requires a final
reply and a successful turn, not merely a partial answer.

Desktop trusted snapshot pushes refresh the observation cache. Five seconds of
silence triggers a full refresh on the next observation (normally every three
seconds), rather than retaining a potentially stale snapshot for a minute. This
is a bounded-staleness fallback, not a per-token latency guarantee; the tradeoff
is more full-history reads while pushes are absent.

Failed card replacements retain a durable retry marker even if content stops
changing. Backoff and the existing 20-attempt/one-hour budget still apply, and
permission/binding failures stop automatic delivery. Only the governed full-card
replacement transport may supersede a *finished* uncertain edit with a new
authorized operation, retaining the original audit record. In-flight/abandoned
writing operations remain fail-closed; creation, replies and task-control actions
never acquire this exception. An existing card is retried instead of sending
duplicate terminal fallback messages. Remote acceptance is not a guarantee of
immediate rendering by every Feishu client.

A durable task-link reservation is not a delivered card. Public projections use
`pending` until Feishu returns the root message ID, then `active`. Failed sends
retain their original link/idempotency identity for an explicit user retry;
refreshing the dashboard must not turn a failed send into a connected UI.

`Health()` includes historical unknown outcomes and old card reconciliation
failures. `CanCreateTaskLink()` separately gates new tasks on current runtime and
storage/worker health. Historical warnings remain visible and are never cleared
or replayed merely to enable a new, unrelated task. Missing/corrupt stores still
block task-link readiness in the host.
