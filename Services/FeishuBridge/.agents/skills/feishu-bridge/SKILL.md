---
name: feishu-bridge
description: Install, configure, diagnose, or use the local Feishu Bridge for explicit Feishu messaging, contacts, Docs/Wiki/comments, Whiteboard/Mindnotes/Markdown, calendar/tasks, Sheets/Base, meetings/minutes, Apps, fixed Events, workflows, task cards, and result auditing. Do not use for general Feishu discussion, local-only drafts, Approval, deletion, permission/member management, arbitrary OpenAPI/EventKey, live meeting control, or background full-data crawling.
---

# Feishu Bridge

Use the bridge's unified client as the only Feishu execution entry. Never call a Feishu write API directly, invoke arbitrary `lark-cli` commands, edit a queue by hand, or expose secrets and real remote IDs.

## Locate the bridge

Run the packaged wrapper from this skill directory:

```bash
node <skill-directory>/scripts/bridge.js <command>
```

The wrapper resolves the project from `FEISHU_BRIDGE_PROJECT_ROOT`, the repository copy of this skill, or the USER-scope `installation.json` created by `npm run skill:install`. If it reports `bridge_project_not_found`, read [references/installation.md](references/installation.md).

Do not hardcode the original author's home directory or assume the user's clone path.

## Route the request

- Installation, upgrade, service setup, permissions, diagnostics, target initialization, results, or audit: read [references/operations.md](references/operations.md).
- Sending, reading messages, contacts, group directories, inbound attachments, cards, or Codex task links: read [references/messaging.md](references/messaging.md).
- Docs, Drive, Wiki, comments, Whiteboard, Mindnotes, or Feishu Markdown: read [references/knowledge.md](references/knowledge.md).
- Calendar, tasks, Sheets, Base, meetings, Note, Minutes, Apps, Events, or combined workflows: read [references/work.md](references/work.md).
- Before any write, high-impact action, identity choice, or capability-boundary decision: read [references/safety.md](references/safety.md).

For the exact current machine interface, run:

```bash
node <skill-directory>/scripts/bridge.js --help
node <skill-directory>/scripts/bridge.js capabilities
node <skill-directory>/scripts/bridge.js capability get <capability-id>
```

Never guess a capability ID, input field, identity, flag, or `lark-cli` shortcut.

## Authorization

- A current user message containing the action, target, and content authorizes only that stated action.
- Missing target or content means no queue submission. Complete safe read-only checks, then ask one concise question.
- Every real write must first run a request-level dry-run using the same business input. Submit the real write with a new request ID only after the dry-run passes.
- High-impact writes require explicit high-impact wording from the user plus `--confirm-high-impact`.
- Do not treat a previous action, broad system permission, installed OAuth scope, or successful test as authorization for a new write.

## Target resolution

- Resolve a person by confirmed binding, unique exact name, configured alias, or an explicit `open_id:` supplied in the current request.
- Resolve a group by confirmed binding, unique exact name among groups the bot has joined, configured alias, or an explicit `chat_id:` supplied in the current request.
- Stop on ambiguity, stale binding, missing directory, or fuzzy-only matches. Show only names, necessary department/group hints, and redacted fingerprints.
- Never echo, document, or commit real `open_id`, `chat_id`, tokens, message IDs, record IDs, or asset bindings.

## Read and write flow

1. Confirm the action is registered and in scope.
2. For writes, check `status`. Start the service at most once only when the current action is already authorized and the bridge is not running.
3. Use bounded reads: exact target, time window, page size, A1 range, field projection, or character limit as required.
4. Run the write as dry-run, then submit the real request with a new ID.
5. Wait for the terminal result unless the user explicitly requests `--async`.
6. If wake fails, keep polling. On timeout, preserve the original request ID; never create a retry automatically.
7. Verify the returned result with `result`, reread, revision, or the capability's declared verification step.

## Identity rules

- Group message reads and bot-owned message operations use the registered bot identity.
- Personal data and personal Drive/Docs/Calendar/Task/Sheets/Base/VC/Minutes/Apps reads use the registered user identity.
- Personal cloud document writes use `user`.
- Wiki URLs, Wiki nodes, Wiki documents, and bridge-managed Wiki Mindnotes use `bot`; the Wiki must grant the bot edit access.
- Never switch identity after a permission failure unless the capability registry itself declares that identity.

## Document protection

For an existing Docx update:

1. Inspect title, revision, length, and only the needed content.
2. Briefly state the current state, intended change, and risk.
3. Default to append and `official_before_update`.
4. Use overwrite or exact string replacement only when explicitly requested and confirmed as high impact.
5. Require the bridge's read → official version → write → reread sequence. A version-creation failure must prevent the body write.

## Result handling

Report only the useful action, redacted target, bounded range, local request ID, terminal status, safe result link/preview, and any remaining risk.

- `dry_run`: validation passed; nothing was written remotely.
- `accepted`: received by a queue, not yet complete.
- `completed` / `sent`: external action completed according to the bridge result.
- `failed` / `denied` / `invalid`: report the returned stage and safe retry condition.
- `duplicate`: retrieve the original request result; do not submit a new ID.
- `partial_sent`: distinguish successful and failed segments.
- `timeout`: keep and return the original request ID and query command.

## Hard boundaries

Reject or explain that the bridge does not expose:

- Approval APIs or Approval events.
- Delete, clear, recall, remove, permission, role, member, public-sharing, or Wiki-move operations.
- Contact writes, arbitrary OpenAPI, arbitrary shortcuts, arbitrary EventKeys, or background full-data crawling.
- Complex arbitrary Doc blocks, live meeting controls, raw Minutes media download, Base workflows, Apps automation, Apps secrets, or database operations.

Extra Feishu permissions do not expand this skill or the bridge's fixed registry.
