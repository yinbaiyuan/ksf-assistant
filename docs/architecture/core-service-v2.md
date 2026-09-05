# CodexAssistant Core v2

## Product Boundary

`codex-usage-bar` is the only product, repository, installer, and release unit. CodexAssistant Desktop owns native UI behavior. CodexAssistant Core is the sole business service and the sole owner of Codex App Server, Desktop IPC, and KSF context resolution. It supervises CodexAssistant Feishu. The Feishu service owns only Feishu transport, cards, fixed `lark-cli` capabilities, queues, authorization, redaction, idempotency, and audit.

The native Feishu service source lives under `Core`. All macOS and Windows packages carry the Go service and pinned `lark-cli 1.0.92` without a separate Node runtime, Feishu npm production dependencies, or JavaScript services. The frozen implementation under `Services/FeishuBridge` is an offline replay baseline only and is never packaged or started. Runtime code never downloads packages or invokes npm.

## Lifecycle

The product has one visible lifecycle:

1. Starting CodexAssistant starts the private Go core.
2. CodexAssistant Core starts CodexAssistant Feishu as its child process.
3. Hiding or closing the panel leaves the product running in the tray or menu bar.
4. Explicitly quitting CodexAssistant sends `shutdown`, stops the Feishu service process tree, closes Codex clients, then exits the desktop app.

Legacy LaunchAgent or Windows Scheduled Task definitions are stopped and removed during migration without deleting `~/.config/feishu-bridge`. They are not recreated by v2.

Codex, Desktop, KSF, `lark-cli`, and Feishu SDK failures are component states, not process-lifetime decisions. CodexAssistant Feishu remains alive and exposes a degraded aggregate snapshot unless the single-instance lock fails, its parent pipe closes, or private-data safety cannot be guaranteed.

## Private Contract

- Host/Core protocol: `codex-usage-core-v2`
- Core/Feishu service protocol: `codexassistant-core-bridge-v1`
- Transport: bidirectional NDJSON JSON-RPC over the Core-created anonymous stdin/stdout pipes
- Limits: 4 MiB per frame, concurrent out-of-order responses, deadlines, cancellation, and EOF ownership
- DTO decoding: protocol methods reject unknown fields, trailing JSON values, malformed optional payloads, and parameters on parameterless methods; JSON numbers retain their exact representation
- Core methods exposed to the Feishu service: capabilities, bounded Codex control/projection, and KSF context
- Feishu service methods exposed to Core: aggregate snapshot, task-link lifecycle, message test, profile, auth, permissions, and settings reload
- Native bridge binary: injected by the signed host through `CODEX_USAGE_BAR_FEISHU_BRIDGE`
- Mutable Feishu service data: `~/.config/feishu-bridge`

The renderer cannot submit a service executable path. Service controls are restricted to `start` and `restart`; event profiles are restricted to `primary` and `manual-only`.

No arbitrary Codex App Server, Desktop IPC, filesystem, or Feishu OpenAPI method is passed through the private contract. stdout is protocol-only; Feishu service diagnostics go to private audit or stderr.

The Feishu daemon constructs exactly one `CapabilityService` and injects it into private RPC, scheduling, directory resolution, and reconciliation. A native one-shot client likewise constructs one service for that invocation and passes it to every command adapter; command adapters never construct competing executors.

## Capability governance

The v2 registry contains 816 capabilities: the frozen 219 baseline, fixed long-tail capabilities, the reviewed Approval surface, and service-native message/document/Approval-event-subscription entries. Every published capability has one backend (`go-sdk` or pinned `lark-cli`), a bounded input schema, identity, scopes, risk, effect, reversibility, queue, redaction rules, and any required preflight/reread steps. Callers cannot provide an OpenAPI method or path.

`lark-cli` is not marked ready from file presence alone. A cached local probe rejects unsafe/non-executable paths, executes `--version`, requires exactly `1.0.92`, and performs a bounded schema lookup before advertising the fixed capability backend. The probe never calls a remote Feishu API during Dashboard refresh.

`feishu-capability-policy-v1` applies `disabled`, `confirm_each`, or `allowed` after the outbound, identity, dry-run, scope, queue, and platform-permission gates. Reads and ordinary writes default to allowed; immediate sends, high-impact writes, and remote operations default to per-operation confirmation. Destructive operations default to disabled. Their risk-wide default cannot be enabled: all 101 published destructive capabilities may be opened only as individual `confirm_each` overrides. Every one has an explicit versioned guard record. A `strong` guard has authoritative preflight and verification; a `bounded` guard has a content-free local or remote preflight and may finish as `outcome_unknown` when the remote state cannot be proved. Destructive operations can never be configured as confirmation-free `allowed`.

Core exposes the fixed host-facing methods `feishu/operation/prepare|confirm|cancel|status` and `feishu/policy/read|update`, forwarding only to their matching private bridge methods. This is the phase-two UI/Skill attachment point; no generic private-RPC or OpenAPI passthrough is exposed.

Confirmation challenges expire after five minutes and bind the capability, normalized input fingerprint, preflight evidence, and policy revision. Expiry proves that no operation ran. Confirmed operations use `queued`, `running`, `verifying`, `succeeded`, `failed`, `outcome_unknown`, `expired`, and `cancelled` as stable states. A timeout after submission never triggers an automatic side-effect replay. Reread-capable operations receive at most three background reconciliation attempts; operations that cannot be safely reread immediately require manual review. Once automatic reconciliation ends, private request content is removed while the content-free operation status and audit remain.

The existing 23 event types, the mail-received event, and the two reviewed Approval status events share one Go SDK long connection. Approval event delivery additionally requires an explicit per-user `INVOLVED_APPROVAL` or `MANAGED_APPROVAL` subscription through one of four fixed, confirmation-governed subscription capabilities. Mail events are locally disabled by default; while disabled their payload is discarded before inbox persistence or workflow dispatch. Approval event records persist fingerprints and bounded metadata only; approval forms and identifiers are not written to the event history.

Approval reads are allowed by default. Creating an instance, copying participants, approving, rejecting, reminding, adding signers, transferring, and changing event subscriptions require a one-time confirmation. Instance cancellation and task rollback are destructive and disabled by default; they can only be opened as individual `confirm_each` overrides with the fixed instance preflight/reread guards intact.

## Durable work and retention

Actionbox, Docbox, and Outbox use the internal V3 work store. Each operation is a private JSON file moving atomically through `pending`, `running`, and `terminal`; result lookup reads the operation file directly and queue health reads an atomic O(1) index. The one-time v2 migration imports safe pending work, converts already-processed side effects without results to `outcome_unknown`, and moves the untouched v2 files into a 30-day private archive. Migration failure disables new writes instead of running two consumers.

One `WorkScheduler` provides four total slots, at most two `lark-cli` slots and one long-remote slot. Conflict keys serialize writes to the same target while unrelated targets progress fairly. Wakeups are event-driven; a one-minute orphan pass exists only for recovery. A claimed side effect interrupted by process loss becomes `outcome_unknown` and is never replayed automatically.

Terminal queue items, direct results, event records, and inbound receipts are kept for 30 days or 20,000 records per class. Operation records are kept for 90 days or 10,000 records. Audit logs are daily/size segmented and retained for 180 days or 200 MiB. `outcome_unknown` and manual-review records are protected for at most 90 days, followed by a content-free abandonment audit. Terminal inbound work is immediately rewritten without its raw payload. Child stderr is continuously drained into a 0600 diagnostic ring of five 1 MiB segments with seven-day retention; unstructured text is represented only by category, length, and fingerprint.

## Snapshot and polling

CodexAssistant Feishu owns one in-memory aggregate snapshot containing process, transport, capability, target, task-link, and queue health. It pushes a complete revisioned snapshot when state changes; Core rejects stale revisions and serves the latest value from memory. The compatibility command `codex-feishu-bridge client snapshot` provides the same content-free aggregate diagnostic for operators and KSF Skill use.

Dashboard refreshes are coalesced in each desktop app. Active panels use 3 seconds, idle panels 15 seconds, and background quota refresh uses 5 minutes. Static settings, pricing, setup, and permission reads are page/action driven. Feishu-owned turns and Desktop-owned links each have exactly one observer; the full link scan is only startup recovery plus a low-frequency orphan check.

## Task-link retention

Task-link schema v2 remains unchanged and unknown fields survive every Go write. Active records are retained. Released or expired records are removed after 7 days; terminal history is capped at 500 oldest-first. Pending card sync, attachment cleanup, and recovery protect a record for at most 30 days, after which a content-free abandonment audit is written before cleanup. The UI derives presentation from `linkState`, `turnState`, `turnOwner`, and `controls`; legacy `state` exists only in the Node compatibility projection.

## Deployment

Windows and macOS package architecture-specific CodexAssistant Feishu and `lark-cli` binaries. Windows copies the packaged binaries into the CodexAssistant user-data directory using `staging -> current`, retains one `previous` release, and verifies the staged Go service before switching. A failed staging or post-switch check preserves or restores `current`. Failure is visible on both platforms and never falls back to Node.

## Codex Migration

`codex-control-v1` is frozen under `docs/protocol`. CodexAssistant Core owns task creation, continuation, steering, interruption and question answers on every supported platform. CodexAssistant Feishu owns transport, attachments and card callbacks and reaches Codex only through the private capability port. Exactly one Go service may consume real Feishu events for a given application.

Desktop `requestUserInput` IDs retain their original JSON string/integer type and bytes through projection, task-link storage, and submission. Card actions carry only a revision hash bound to task, turn, owner, typed request ID, and full question definition. `{ok:true}` from the follower call is merely an acknowledgement: success requires a strictly newer authoritative snapshot in which the exact request ID has disappeared. Verification waits synchronously for five seconds and continues for at most 30 seconds in total; otherwise the action is `outcome_unknown`, options reopen, and the answer is not replayed. Three consecutive live Plan selections that release Desktop waiting remain a release gate.
