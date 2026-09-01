# Codex Usage Bar engineering guide

## KSF context

When work changes the product goal, scope, or long-lived product decisions, read:

- `$HOME/Documents/KSF/AGENTS.md`
- `$HOME/Documents/KSF/10项目/Codex Usage Bar/项目记忆卡.md`

Code, architecture, build, test, packaging, and release facts belong to this repository.

## Repository rules

- Target macOS 13+ with a universal2 (`arm64` + `x86_64`) release artifact.
- Keep the app read-only with respect to Codex account state. Never read or copy Codex credential files.
- Use the documented Codex App Server account endpoints through a child process.
- Keep protocol and presentation logic in `CodexUsageCore`; keep AppKit, SwiftUI, notifications, and login-item integration in `CodexUsageBar`.
- Treat KSF project cards as externally interpreted data. Consume the versioned local catalog/projection bridge; do not parse KSF Markdown in the app.
- Keep project assignment conservative and project actions structured, user-initiated, path-contained, and visible in Terminal.
- Build with Swift Package Manager or the documented direct compiler fallback. Do not require an Xcode project.
- Run `swift test` and `scripts/build-app.sh` after code changes.
- Do not create remotes, commit, push, notarize, or publish without explicit authorization.
