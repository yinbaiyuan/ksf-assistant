# Operations and audit

Use the wrapper shown in `SKILL.md` for every command.

## Read-only checks

```text
status
doctor
profile catalog|show|set <primary|manual-only>
capabilities
permissions
auth configure-existing
auth start-config
auth start-user
auth finish-user
capability catalog
capability get <id>
events catalog|status|recent|get
result <outbox|docbox|actionbox> <request-id>
recent <outbox|docbox|actionbox|tasks|messages|audit>
targets list
targets directory status|search
targets group-directory status|search
task-link protocol|list|status
```

`doctor.health=degraded` means the bridge is usable with a warning, such as stale cache or queue size. Only `failed` is a hard readiness failure.

## Mutating operations

- `auth configure-existing` configures an existing Feishu app from local stdin or a private file. It may change `lark-cli` and Windows DPAPI credential state, but must not put App Secret in chat, command arguments, versioned files, or logs.
- `auth start-config --create-new` starts new Feishu CLI app setup and returns a QR code from the platform private directory. Do not use it unless the user explicitly asks to create a new app. Plain `auth start-config` is fail-closed when no existing app configuration is available.
- `auth start-user` starts user OAuth for an already configured app and returns a QR code from the platform private directory. It does not submit bridge write queues, but it changes local credential state after the user scans and confirms.
- `auth finish-user` completes the previously started user OAuth device flow. Do not expose the stored device code unless the user needs to resume from another terminal.
- `start` and `restart` change local service state. Use only when requested, or once when an already-authorized write finds the bridge stopped.
- `profile set primary|manual-only` changes only this machine's inbound long-connection role. `primary` receives messages and card callbacks; `manual-only` disconnects inbound while preserving local/outbound capabilities. For one shared Feishu app, keep exactly one machine on `primary`.
- Manual directory sync is a bounded local read/cache mutation; use when requested or as one fallback during authorized exact-name sending.
- Binding or unbinding people, groups, targets, task links, test assets, or event watches changes local state and requires an explicit target.
- Do not edit `client.json`, queue JSONL, PID state, event inbox, or audit files by hand.

## Queue behavior

- Message sends use outbox.
- Docx body writes use docbox.
- Other registered writes use actionbox.
- Queue wake carries no business payload. Wake failure falls back to polling.
- Default to terminal-state waiting. Use `--async` only when the user requests it, then return the original local request ID.

For exact syntax, run the bridge help and read the repository's `docs/bridge-client.md`.
