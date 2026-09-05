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
