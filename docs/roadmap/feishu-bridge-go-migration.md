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

- Complete: local Node baseline tag; managed Go child lifecycle; private settings
  and resumable setup state; setup/settings/restart RPC; macOS and Windows setup
  UI; fixed 23-event replay ingress; four-target bridge cross-compilation;
  pinned lark-cli packaging; public-source hygiene, license policy and SPDX SBOM.
- Production remains Node. The Go executable is packaged behind the explicit
  preview flag and cannot become the production consumer before every hardware
  gate passes.
- Remaining parity: secure Keychain/DPAPI credential loading, queue and
  authorization state, all 219 capability executions, Codex control, automatic
  current-user alias binding, platform configuration verification, and card
  callback transport. The public WebSocket API in `oapi-sdk-go/v3.11.0` does not
  currently expose the card-message handler used by the Node compatibility
  implementation, so this requires an audited adapter or an upstream-supported
  API before cutover.
- Remaining acceptance: real macOS arm64 end-to-end testing plus macOS x64,
  Windows x64 and Windows arm64 installation, setup, send/receive, recovery,
  exit and upgrade tests.

## Done

- macOS arm64/x64 and Windows x64/arm64 pass the same parity harness.
- Existing Node test scenarios have equivalent Go results.
- Users do not reconfigure credentials or private data.
- The installer contains no Node runtime, npm, or Node production dependency.
- No dual-running production mechanism remains.
