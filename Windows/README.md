# CodexAssistant for Windows

Windows 10/11 system-tray host for the shared CodexAssistant core.

This page is for source contributors. Packaged-app users follow the repository
README and configure everything in the application.

## Architecture

- `src/main.cjs` owns the tray, frameless window, native dialogs, login launch, Codex deep links and visible PowerShell project launch.
- `src/preload.cjs` exposes a fixed IPC allowlist.
- `src/renderer` is a sandboxed, Node-free presentation layer.
- `../Core` owns all Codex, Token, KSF, task-state and Feishu business behavior.
- The host persists the selected API estimate plan and up to 20 custom prices; the shared core validates the catalog and computes all USD estimates.

The renderer cannot spawn processes or read files. Project task creation and launch accept only a project ID; the core resolves current catalog data and validates paths again before returning an action.

## Packaged runtime

- Windows 10/11 on x64 or arm64.
- Codex CLI/Desktop installed and signed in.
- No system Node.js, Go, Ruby, local bridge checkout, environment variable or
  configuration-file edit is required.
- KSF and Feishu are optional integrations. Their absence does not block quota,
  Token history or the rest of the application.

## Development

Development requires Node.js 20+ and Go 1.23+. Ruby 3.2+ is needed only when a
contributor exercises the optional KSF adapter.

```powershell
npm ci
npm test
npm start
```

The development host expects a matching core binary under `../dist/core`. Build it with:

```powershell
npm run build:core
```

## Package

```powershell
npm run dist:win
```

This creates per-user NSIS installers for x64 and arm64. Preview builds are unsigned unless signing is configured outside the repository.

Project launch follows one explicit Windows convention: `<projectDirectory>\start.ps1`. The core accepts only a regular, non-symlink file at that exact location. The host opens it in a visible PowerShell window; no command guessing or hidden execution is allowed.

Live task state and automatic first-turn submission require a compatible Codex
Desktop named pipe. A packaged build accepts only a host-managed current-user
pipe and never asks an end user to locate or type its path. Contributors testing
against a private compatible Desktop build may inject `CODEX_DESKTOP_IPC_PATH`.
Without that integration, desktop-owned live state and direct task submission
report their own unavailable status while quota, Token history, KSF projects and
Feishu bridge health continue independently.
