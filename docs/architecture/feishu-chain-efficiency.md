# Feishu task chain efficiency

This change bounds background recovery and separates display observations from
control preflight. It does not move card writes out of the control action lock,
change token accounting, or expand authorization.

## Delivery recovery

Each durable delivery attempt runs once. Transient failure releases its worker
and schedules 1/5/15/30/60-second delays, then 5-minute delays, with deterministic
±20% per-item jitter. Retry-After is honored when provided by the CLI envelope.
Twenty attempts or one hour since first receipt pauses work as `needs_review`;
explicit retry starts a new bounded budget without resetting historical attempts.
Startup recovery pauses legacy over-budget records before dispatch. Shutdown
cancellation preserves work without charging a failed attempt.

Private records retain payloads for review; public diagnostics expose identifiers,
counts, safe error classifications, and timestamps only. Permission, binding,
validation, and uncertain-outcome errors are not background-replayed.

Managed CLI, through the running Core gateway:

- `client events review`
- `client events retry --id <one-work-id>`
- `client task-link sync-review`
- `client task-link sync-retry --id <one-link-id>`
- `client task-link diagnostics`

Event retry checks the original payload digest and Core receipt before changing
state: accepted deliveries complete their handoff, unknown outcomes stay paused,
and only absent receipts permit delivery to resume with the original identity.
The delivery path still checks current operator authorization and bindings.

## Display observations

Desktop observers consume an owner- and connection-bound snapshot cache. Initial
and periodic loads are coalesced per task. Trusted pushed snapshots update the
cache; unchanged revisions bypass card projection. Full-history calibration is
scheduled at most once per minute after a successful read; failed reads back off.
Disconnect invalidates the old state. Owner changes require a new authoritative
snapshot. Interactive question submission and plan validation retain the fresh
owner-bound preflight API. Missing or incomplete turn history does not replace a
card with an older turn.

## Card synchronization

Per-link file locking serializes all task-card sync entrypoints. The coordinator
reloads the latest durable link before rendering, persists its pending target
before sending, and only acknowledges the version it actually sent. Changes
arriving during a write remain pending. Unchanged content avoids a network PATCH.

The additive `cardSync` record stores state, target/synced fingerprints, current
failure budget, classified error, next attempt, and last synchronized lifecycle.
Permanent or uncertain failures enter `needs_review`; transient errors use the
same bounded retry policy. Maintenance checks due work on its existing 30-second
cycle, so a scheduled short retry may wait until that cycle. Content changes do
not reset an outage budget. Released/expired links render their terminal card,
and no longer observe the task. Expiry after successful sync also schedules the
terminal card. Released records with no delivered message ID are marked synchronized
without a network request (`no_card_created`). Explicit retry verifies binding/permissions through the normal
patch path; it does not send another card or execute a Codex turn.

## Compatibility and rollback

Inbound schema 3 and task-link schema 2 are retained with additive fields/status.
An older binary does not understand paused inbound records: rollback must restore
the corresponding pre-upgrade private state with the previous app, while the app
is stopped. Do not independently downgrade the binary against migrated state.
Keep backups private; do not commit local state or identifiers.

## Verification — 2026-09-07

Current Go suite, related race checks (Feishu, integration, Desktop IPC, service),
Go vet, 88 Swift tests, 145 Windows tests, four Core target builds, universal2
macOS packaging/signature verification, and 776 pinned CLI contracts passed.
Regression coverage includes bounded/paused delivery, restart and explicit retry,
receipt conflicts/unknown outcomes, owner rejection and disconnect invalidation,
coalesced initialization/calibration, pushed state racing a history response,
permanent card failures, persisted backoff, Retry-After, concurrent card writes,
and released records with no delivered message.

After installation, 21 read-only samples spanning 600 seconds recorded:

- Four over-budget deliveries stayed paused with unchanged lifetime counts.
- Seven old cards stayed in review; four never-delivered released records ended
  synchronization without network requests.
- Nine additional full-history reads and 191 cache hits for the active task.
- Thirteen successful task-card updates; the final active card had no pending sync.
- Zero sampling errors; the final managed runtime doctor reported healthy.

This window verifies runtime observation and card synchronization. It does not
measure model-generation speed or simulate every external permission failure.
The previous app and affected private state were backed up before replacement;
no Git staging, commit, or publication was performed for this change.
