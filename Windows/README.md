# Codex Usage Bar for Windows

Windows 10/11 system-tray host for the shared Codex Usage Bar core.

## Architecture

- `src/main.cjs` owns the tray, frameless window, native dialogs, login launch, Codex deep links and visible PowerShell project launch.
- `src/preload.cjs` exposes a fixed IPC allowlist.
- `src/renderer` is a sandboxed, Node-free presentation layer.
- `../Core` owns all Codex, Token, KSF, task-state and Feishu business behavior.
- The host persists the selected API estimate plan and up to 20 custom prices; the shared core validates the catalog and computes all USD estimates.

The renderer cannot spawn processes or read files. Project task creation and launch accept only a project ID; the core resolves current catalog data and validates paths again before returning an action.

## Runtime requirements

- Windows 10/11 on x64 or arm64.
- Codex CLI/Desktop. Set `CODEX_BIN` when `codex.exe` is not on `PATH` or in a standard install location.
- Ruby 3.2+ on `PATH` for the KSF-owned catalog/projection bridge.
- Node.js for Feishu Bridge operations; the Usage Bar UI and core themselves do not require a system Node.js after packaging.
- A compatible KSF root and, for Feishu control, a local `feishu-bot-bridge` checkout.

## Development

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

This creates per-user NSIS installers for x64 and arm64. Internal builds are unsigned unless signing is configured outside the repository.

Project launch follows one explicit Windows convention: `<projectDirectory>\start.ps1`. The core accepts only a regular, non-symlink file at that exact location. The host opens it in a visible PowerShell window; no command guessing or hidden execution is allowed.

Live task state and automatic first-turn submission require the compatible Codex Desktop named pipe. This preview intentionally does not guess a private endpoint; set `CODEX_DESKTOP_IPC_PATH` to the current-user pipe exposed by a compatible Codex Desktop build. Without that pipe, quota, Token history, KSF projects and Feishu bridge health continue independently; desktop-owned live state and direct task submission report their own unavailable status.
