# Shared Core v2

## Product Boundary

`codex-usage-bar` is the only product, repository, installer, and release unit. The Swift and Electron hosts own native UI behavior. `codex-usage-core-v2` owns shared business logic and supervises the packaged Feishu Bridge process. The bridge owns Feishu transport, fixed capabilities, queues, authorization, redaction, idempotency, and audit.

The bridge source lives at `Services/FeishuBridge`. Packaged releases carry a checksum-pinned Node 24 LTS runtime and production dependencies. Runtime code never downloads packages or invokes npm.

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
- Node runtime: injected by the signed host through `CODEX_USAGE_BAR_NODE`
- Mutable bridge data: `~/.config/feishu-bridge`

The renderer cannot submit a bridge path. Service controls are restricted to `start` and `restart`; event profiles are restricted to `primary` and `manual-only`.

## Deployment

Windows copies the packaged service and runtime into the CodexAssistant user-data directory using `staging -> current`, retains one `previous` release, and checks both Node entry points before switching. A failed staging or post-switch check preserves or restores `current`. macOS carries architecture-specific Node binaries and the service under app Resources; the same release manifest and checksum source are used at build time.

## Codex Migration

The current bridge-owned Codex path remains active in the first v2 preview. `codex-control-v1` is frozen under `docs/protocol` before behavior moves into Go. During parity work, only one implementation may consume real Feishu events. Go cannot become the production owner until task creation, continuation, steering, interruption, question answers, attachments, and card callbacks pass the shared fixtures.
