# Shared Core v2

## Product Boundary

`codex-usage-bar` is the only product, repository, installer, and release unit. The Swift and Electron hosts own native UI behavior. `codex-usage-core-v2` owns shared business logic and supervises the packaged Feishu Bridge process. The bridge owns Feishu transport, fixed capabilities, queues, authorization, redaction, idempotency, and audit.

The native bridge source lives under `Core`; the frozen Node compatibility implementation remains under `Services/FeishuBridge` only for Windows rollout and explicit manual rollback. Packaged migration builds carry the Go bridge, pinned `lark-cli 1.0.92`, and a checksum-pinned Node 24 LTS compatibility runtime. Runtime code never downloads packages or invokes npm.

## Lifecycle

The product has one visible lifecycle:

1. Starting CodexAssistant starts the private Go core.
2. The core starts the packaged Feishu Bridge as its child process.
3. Hiding or closing the panel leaves the product running in the tray or menu bar.
4. Explicitly quitting CodexAssistant sends `shutdown`, stops the bridge process tree, closes Codex clients, then exits the host.

Legacy LaunchAgent or Windows Scheduled Task definitions are stopped and removed during migration without deleting `~/.config/feishu-bridge`. They are not recreated by v2.

## Private Contract

- Protocol: `codex-usage-core-v2`
- Transport: NDJSON JSON-RPC over the host-owned stdin/stdout pipe
- Feishu service root: injected by the signed host through `CODEX_USAGE_BAR_FEISHU_SERVICE_ROOT`
- Native bridge binary: injected by the signed host through `CODEX_USAGE_BAR_FEISHU_NATIVE`
- Node runtime: injected through `CODEX_USAGE_BAR_NODE` only on Windows or during explicit manual rollback
- Mutable bridge data: `~/.config/feishu-bridge`

The renderer cannot submit a bridge path. Service controls are restricted to `start` and `restart`; event profiles are restricted to `primary` and `manual-only`.

## Deployment

Windows copies the packaged service and runtime into the CodexAssistant user-data directory using `staging -> current`, retains one `previous` release, and checks both migration entry points before switching. A failed staging or post-switch check preserves or restores `current`. macOS carries architecture-specific Go bridge and `lark-cli` binaries under app Resources. macOS arm64 defaults to Go; Node requires an explicit runtime override and is never selected automatically after a Go failure.

## Codex Migration

`codex-control-v1` is frozen under `docs/protocol`. Go owns task creation, continuation, steering, interruption, question answers, attachments and card callbacks on macOS arm64; bridge-created tasks use controlled App Server context and existing UI-linked tasks use Desktop IPC. During the remaining platform rollout, only one implementation may consume real Feishu events for a given application.
