# TODO-FEISHU-GO-001: 将飞书桥从 Node 完整迁移到 Go

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

Platform differences may exist only in adapters. macOS uses Keychain, POSIX permissions, and Unix Socket behavior. Windows uses DPAPI, ACLs, and Named Pipe behavior. Product lifetime is shared: explicitly quitting Usage Bar stops every product-managed server on both platforms.

## Sequence

1. Freeze JSON contracts and language-neutral parity fixtures, beginning with `codex-control-v1`.
2. Replace event ingress, capability execution, queues, Codex control, and platform hosting one module at a time.
3. Run Node and Go in a short comparison period with fake transport or replay fixtures; never permit two real consumers.
4. Switch the production owner only after parity acceptance.
5. Archive Node service code as read-only history and remove Node Runtime, npm, and Node production dependencies from the installer.

## Done

- macOS arm64/x64 and Windows x64/arm64 pass the same parity harness.
- Existing Node test scenarios have equivalent Go results.
- Users do not reconfigure credentials or private data.
- The installer contains no Node runtime, npm, or Node production dependency.
- No dual-running production mechanism remains.
