# Independent task runtime v1

This package is a local, advisory reporting store, not a workflow engine, task
discovery source, KSF authority, or proof of skill execution. It works while the
Desktop app, Core and Feishu bridge are closed. It never changes KSF governance,
validation statuses, credentials, root selection, or the old project bridge.

## CLI / skill contract

`ksf-assistant-task report|get|list|doctor --root PATH --host codex`

`ksf-assistant-task --version` is a separate read-only packaging probe: no root,
stdin, key or process startup is needed. It returns the normal success envelope
plus the current KSFAssistant product version in `software_version`. Protocol `version` remains `1`.

One bounded UTF-8 JSON object on stdin, one JSON response on stdout, then exit.
There is no daemon, socket, shell execution, production startup or account access.
`--root` is required and must identify an existing KSF workspace (`AGENTS.md` and
`.agents`); there is no environment/CWD/default-root fallback. `--host` defaults
to `codex`, the only supported host. No identity flag is accepted.

All requests contain `"protocol":"ksfassistant-task-runtime-v1","version":1`.
The command is positional, not repeated in JSON. `host` may be omitted in JSON;
if present it must match `--host`. Unknown fields (including nested fields),
duplicate keys, trailing JSON, unsupported versions, invalid UTF-8 and oversized
input fail closed. No JSON patches or silent field dropping.

Report (replace the placeholder with an actual, complete v6 receipt object):

```json
{
  "protocol": "ksfassistant-task-runtime-v1",
  "version": 1,
  "event_id": "independent-random-event-uuid",
  "expected_revision": 0,
  "state": {
    "scope": "project",
    "project_card": "10项目/Example/项目记忆卡.md",
    "reported_status": "running",
    "receipt": "REPLACE WITH COMPLETE V6 OBJECT",
    "progress": {"summary": "Implementing the storage contract", "percent": 20}
  }
}
```

For an as-yet unrouted task, use
`"state":{"scope":"unresolved","reported_status":"running"}` without a receipt.
`reported_status`: `unknown`, `running`, `waiting`, `blocked`, `completed`.
Progress is optional; summary is at most 512 UTF-8 bytes, percent is optional
0–100. Every report replaces the entire reported state; omitted progress clears
it. No automatic transitions, required steps, scheduling or completion authority.

For report/get, raw identity comes only from `CODEX_THREAD_ID` or private stdin
`thread_id`. If both are supplied they must match. Use a private pipe, not a
world-readable JSON file, shell history, argv or log. Do not copy raw identity
into event IDs, progress, task intent or receipt fields. The runtime rejects
payloads containing it. Identity is an opaque 8–256 byte non-control string.
Never emit raw identity in tool/skill output. Get accepts identity **or** the
returned `task_id` (not both); list/doctor accept neither.

```json
{"protocol":"ksfassistant-task-runtime-v1","version":1}
```

That object is sufficient for get with `CODEX_THREAD_ID`, and for list/doctor.
List additionally accepts `limit` (1–100, default 25) and `after` (the previous
`next_after` task ID). Task IDs sort lexically; pagination is not a transactional
cross-task snapshot. Get by public ID uses `task_id`. List/doctor never consume
an ambient thread identity. Report/get reject fields belonging to other commands.

Success: `{"protocol":"ksfassistant-task-runtime-v1","version":1,"ok":true,...}`.
Report/get return `task` (snapshot with freshness/provenance), `history` (ordered
changes); report also returns `replayed`. List returns `tasks` and optional
`next_after`. Doctor returns `doctor` with store/key/verifier availability and
bounded record integrity diagnostics; it is read-only and creates no key/files.
Doctor strings are exact: `store: missing|available|degraded|unavailable`,
`key: missing|available|unavailable`, `verifier: available|unavailable`, plus
`records` (integer) and `diagnostics` (array). `missing` means proven absent,
not unreadable/corrupt. Get-first is the recommended Skill flow: CLI get returns
`not_found` if a valid-key lookup has no task, or if it proves `store=missing`,
`key=missing`, `records=0` with no integrity diagnostics. That error permits a
report with expected revision 0. No separate doctor call is required by the Skill.
Other key failures remain `key_unavailable`; no public ID is invented without
the key. Standalone Go `TaskID` lookup remains fail-closed if the key is missing.
When records exist but the identity key is missing or invalid, report itself
refuses initialization. It never treats orphaned records as an empty workspace.
Reported snapshots include task_id, host, revision, created_at, reported_at,
workspace_digest, state and derived project_card when uniquely resolved.
`report_freshness` is `recent` for <=15 minutes, otherwise `stale` (including
future timestamps). `route_freshness` is `current`, `stale`, `unavailable` or
`unverified`; it never changes reported state. Non-current routes are not trusted
for project assignment. Runtime does not claim to observe Desktop status.

Failure: `{"protocol":"ksfassistant-task-runtime-v1","version":1,"ok":false,
"error":{"code":"revision_conflict","message":"..."}}`, exit 1. Success exits 0.
Codes include `invalid_request`, `unsupported_protocol`, `invalid_identity`,
`invalid_root`, `unsafe_path`, `not_found`, `key_unavailable`, `revision_conflict`,
`event_conflict`, `route_invalid`, `verifier_unavailable`, `corrupt_record`,
`limit_exceeded`, `busy`, `io_error`. Errors do not echo private input or verifier
stdout/stderr. On I/O failure after commit, get and retry the **identical** event.

`expected_revision` is required (0 to create). A successful event increments by
one. Retrying the same event ID and canonical payload returns the original event
snapshot, even if later events exist or workspace sources changed. Reusing an
event ID with different content or revision returns `event_conflict`. A new event
with an outdated revision returns `revision_conflict`. Do not change a request
while retrying it. Generate a fresh event ID for a changed report.

## Provenance and scope

The full `verified-route-projection-v6` receipt is retained without inventing an
alternate authority. The current workspace's platform binary at
`.agents/bin/ksf-<GOOS>-<GOARCH>[.exe]` is invoked with `route --verify-route` and
arguments reconstructed from that receipt. No Ruby fallback, PATH binary, custom
verifier flag or executable from the payload. The canonical workspace is always
passed as explicit `--root`; inherited `KSF_ROOT` cannot redirect verification.
`CODEX_THREAD_ID` and `KSF_ROOT` are excluded from
the child environment, preventing the existing panel publisher from writing.
Source paths, file sizes and hashes are checked, and the **entire** parsed receipt
must match the current verifier output. A fabricated self-consistent receipt hash
is insufficient. Missing or incompatible current verifier fails closed.

Scope is explicitly declared by the reporting Agent, never inferred from the
number of referenced project cards. `project` requires `state.project_card`, an
exact workspace-relative `10项目/<project>/项目记忆卡.md` present among the verified
receipt context roots. Multiple reference roots are allowed; only that explicit
primary project is bound. There is no implicit single-root binding convenience.
`ksf` and `unresolved` forbid `project_card` and allow any number of reference
roots. `unresolved` may also omit the receipt. Project memory descendants cannot
substitute for root cards. Snapshot `project_card` repeats the validated explicit
selection for consumers; it must equal `state.project_card`, not a root-count
inference. A workspace
digest binds the canonical root path; copying records to a different workspace
does not transfer provenance. Moving a workspace requires a new report, not an
implicit migration. A task's ID stays stable across route/project changes.

## Storage and privacy

Records: `<root>/.agents/runtime-data/ksfassistant/tasks-v1/<task_id>.json`.
Persistent `<task_id>.lock` files use OS advisory locks, automatically released
on process death; lock files are not deleted (avoids split-lock races). Each JSON
creation additionally takes `.index.lock` so concurrent new tasks cannot exceed
the workspace capacity. Existing-task updates retain independent per-task locks.
Each JSON
record contains the current snapshot and full ordered change history in **one**
atomic replacement. No independently committed history sidecar. Temp files are
private and same-directory; file data is synced before replace, then directory
metadata is synced on Unix. Interrupted writes leave the previous complete
record; orphan `.task-*.tmp` files are ignored, never treated as tasks.

IDs are `task_` + HMAC-SHA256(private random 32-byte per-user key,
`ksfassistant-task-runtime-v1\0codex\0<raw-thread-id>`). Neither route digest nor
plain thread hash is an identity. The key lives in
`<os.UserConfigDir()>/KSFAssistant/task-runtime-v1/identity.key` (macOS Application
Support, Windows AppData, Linux user config); it is not a Codex credential and is
not stored in the workspace. Creation is serialized; only report creates it.
Tests inject a private key directory via Go Options, never real credentials.
No CLI/env key override. Key deletion/rotation is not automatic and breaks lookup
of existing IDs; no migration or credential access is attempted.

Bounds: request/receipt 256 KiB, record 4 MiB, history 256 events, 4096 records,
directory scan 16384 entries, each source file 8 MiB, total sources 32 MiB,
128 explicit context entries. Limits fail explicitly; history is never silently
pruned. Unix key/runtime files are 0600 and leaf directories 0700; Windows leaf
directories receive a current-user-only inherited ACL. Canonical workspace roots
may resolve a caller's symlink, but descendants may not be symlinks/reparse paths,
nonregular files or traversal paths. Workspace ancestors are not chmodded.
Storage is a same-user local filesystem boundary, not protection against a
malicious process already running as that user or network filesystem semantics.

## Core integration

Core reads only records for already-known, current local Desktop observations.
It never discovers a task from this directory or changes running/waiting counts,
classification, completion or observation timestamps from reported state.
The optional task runtime projection keeps reported and observed status separate.
Current verified project routes enrich assignment; KSF-wide, unresolved or stale
records are unassigned rather than rebound through CWD/old project metadata.
Tasks without records retain the existing project bridge fallback. Read failures
surface a safe diagnostic and cannot overwrite records or initialize the key.

## Validation

`go test ./internal/taskruntime ./cmd/ksf-assistant-task ./internal/service ./internal/domain`
and `go test -race ./internal/taskruntime ./cmd/ksf-assistant-task ./internal/service`
exercise CAS, retries, crash-release, bounded creation, atomic readers, raw-ID
exclusion, unknown-schema refusal, source provenance, explicit project selection
and Core-closed CLI operation with isolated homes/keys. No real credentials.

Optional current-verifier integration:
`KSF_TASK_TEST_VERIFIER_SOURCE=/path/to/ksf go test ./internal/taskruntime -run TestCurrentKSFVerifier -count=1`.
It reads/copies only the platform binary, runtime manifest and three mechanism
scripts, then creates synthetic governance/cards in `t.TempDir()`. It never writes
the source workspace, reads credentials, or copies private project material.
