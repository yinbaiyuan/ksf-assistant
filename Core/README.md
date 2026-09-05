# KSFAssistant Core

`ksf-assistant-core` is the platform-neutral source of truth for KSFAssistant.
It owns Codex App Server access, local token accounting, KSF project projection,
task-state classification, Feishu task-link orchestration, and the public
dashboard snapshot consumed by both platform hosts.

The on-demand `token/history/read` method remains the compatibility 30-day
local-history reader. `token/history/compare` aligns those local buckets with
the server-published account history by date, preserving absent server dates as
unavailable. Hosts render the comparison but do not scan Codex session logs or
join account data themselves while the KSFAssistant Core is healthy.

The pricing package owns seven versioned API presets, custom-plan validation,
and fixed-precision micro-USD estimates. `pricing/catalog/read` exposes the
validated catalog. Dashboard and history reads accept the same pricing
selection, while `repriceOnly` reuses loaded history instead of rescanning
Codex sessions when a host changes plans.

The core speaks newline-delimited JSON-RPC 2.0 over stdin/stdout. Platform hosts
must not infer business state from raw Codex, KSF, or Feishu payloads.

Build locally:

```bash
go test ./...
go build -o ../dist/core/darwin-arm64/ksf-assistant-core ./cmd/ksf-assistant-core
GOOS=windows GOARCH=amd64 go build -o ../dist/core/windows-x64/ksf-assistant-core.exe ./cmd/ksf-assistant-core
```
