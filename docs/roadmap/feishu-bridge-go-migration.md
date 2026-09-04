# TODO-FEISHU-GO-001: 将飞书桥从 Node 完整迁移到 Go

Target: CodexAssistant `0.10.0-preview.1` public preview.

## Goal

Move the packaged Feishu Bridge implementation from Node.js to Go without changing user data, credentials, authorization, or observable behavior. Node is a compatibility implementation and must not gain new Node-specific architecture.

## Contracts To Preserve

- messages and cards;
- all 219 fixed capabilities and all 23 reviewed non-Approval events;
- outbox, docbox, and actionbox terminal-state behavior;
- idempotency, permission checks, redaction, and audit;
- existing OAuth material, target aliases, profiles, queues, and task links under `~/.config/feishu-bridge`;
- exactly one real event consumer for a shared bot.

## Platform Boundary

Platform differences may exist only in adapters. macOS uses Keychain, POSIX permissions, and Unix Socket behavior. Windows uses DPAPI, ACLs, and Named Pipe behavior. Product lifetime is shared: explicitly quitting CodexAssistant stops every product-managed server on both platforms.

## Sequence

1. Freeze JSON contracts and language-neutral parity fixtures, beginning with `codex-control-v1`.
2. Replace event ingress, capability execution, queues, Codex control, and platform hosting one module at a time.
3. Run Node and Go in a short comparison period with fake transport or replay fixtures; never permit two real consumers.
4. Switch the production owner only after parity acceptance.
5. Remove Node service source, Node Runtime, npm and Node production dependencies; preserve the old implementation only in private Git history and the local baseline tag.

## Current checkpoint

- Complete: language-neutral capability/event/permission snapshots; managed Go
  lifecycle; Keychain/DPAPI adapters; lossless private-state migration; all 219
  capability executions; three queues; audit/redaction; official Go SDK message
  and card ingress; attachments; Codex task control; four-target cross-build;
  pinned lark-cli packaging; macOS arm64 install, restart recovery and real
  outbound acceptance and a real inbound message round trip through Codex and
  the Feishu reply path.
- macOS arm64 now uses Go in production. Node is retained only for explicit
  manual rollback and is not an automatic failure path. Windows remains on Node
  until its platform acceptance is complete.
- Remaining parity is limited to the old convenience-command orchestration
  layer (directory/name lookup, selected domain shortcuts and read-only composed
  workflows). The fixed 219-capability execution surface is already native.
- Remaining live acceptance: a card click on the switched macOS arm64 build,
  then macOS x64, Windows x64 and Windows arm64 installation,
  setup, send/receive, recovery, exit and upgrade tests.

## Done

- macOS arm64/x64 and Windows x64/arm64 pass the same parity harness.
- Existing Node test scenarios have equivalent Go results.
- Users do not reconfigure credentials or private data.
- The installer contains no Node runtime, npm, or Node production dependency.
- No dual-running production mechanism remains.
