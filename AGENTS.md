# CodexAssistant engineering guide

## Optional KSF context

Contributors may use their own KSF workspace for project context, but KSF is never required to build, test, run, or contribute to this repository. Do not commit personal KSF paths or content.

Code, architecture, build, test, packaging, and release facts belong to this repository.

## Repository rules

- Target macOS 13+ with a universal2 (`arm64` + `x86_64`) app and Windows 10/11 with x64 and arm64 installers.
- Keep the app read-only with respect to Codex account state. Never read or copy Codex credential files.
- Use the documented Codex App Server account endpoints through a child process.
- Keep cross-platform business rules and external protocol access in the Go `Core`; platform hosts consume only its versioned JSON-RPC contract.
- Keep AppKit/SwiftUI, notifications, login items, visible Terminal integration, Electron/Windows tray behavior, dialogs, and PowerShell windows in their platform hosts.
- Treat KSF project cards as externally interpreted data. Consume the versioned local catalog/projection bridge; do not parse KSF Markdown in the app.
- Keep project assignment conservative and project actions structured, user-initiated, path-contained, and visible in Terminal.
- Build macOS with Swift Package Manager or the documented direct compiler fallback. Build Windows with Go, npm and electron-builder; do not require Xcode or Visual Studio projects.
- Run Go tests, `swift test`, Windows Node tests, both core cross-compiles, and the relevant platform package build after code changes.
- Do not create remotes, push, notarize, or publish without explicit authorization.
