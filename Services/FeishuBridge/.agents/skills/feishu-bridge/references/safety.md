# Safety, risk, and identity boundaries

## Risk levels

- `read`: direct execution with an exact target and enforced range.
- `write`: current-turn authorization, dry-run, new real request ID, queue, dedupe, redaction, audit, and terminal verification.
- `high-impact-write`: all write rules plus explicit user wording, `--confirm-high-impact`, preflight or snapshot, and reread.
- `remote-operation`: actionbox submission and polling through a fixed status capability. Timeout preserves the original local request ID.

## Content and identifiers

- Pass message bodies, document bodies, Apps prompts, and structured payloads through stdin or private files.
- Do not place secrets or sensitive content in command arguments.
- Never display or persist real target IDs, message IDs, event IDs, task/record/session/release IDs, tokens, or test-asset values outside private runtime storage.
- Public output may include aliases, types, bounded previews, counts, links approved by the bridge, and irreversible fingerprints.

## Permission model

- Installed Feishu scopes are prerequisites, not authorization for a current write.
- Outbound has no target allowlist. Inbound direct-chat and group-control permissions remain separate and must not be broadened during outbound work.
- `permissions` compares application scopes and user OAuth scopes. Extra scopes do not become capabilities.
- The official SDK is only the single inbound event transport. All Feishu API reads and writes use fixed `lark-cli` adapters.

## Failure stops

- Missing/ambiguous/stale target: stop before queueing.
- Failed document version, schema read, preflight, or snapshot: do not write.
- Permission error: report the required identity/resource access; do not silently change target or identity.
- Partial success: report the split and do not retry the whole operation automatically.
- Timeout: return the original local request ID and fixed result query.
