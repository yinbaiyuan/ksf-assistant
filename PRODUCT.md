# Codex Usage Bar product brief

## Product

Codex Usage Bar is a private macOS menu-bar instrument for one heavy Codex user. It combines account capacity, live task attention, and the user's whole KSF project system in one glanceable surface.

## Current release

v0.3 adds a private WeChat message channel beside the existing KSF project workbench. The menu-bar label remains the stable quota and task signal; WeChat is an isolated transport for outbound notices and inbound queued commands, not a Codex conversation surface.

Because WeChat only permits bot replies within 24 hours of the user's latest message, the connector sends one renewal reminder after 23 hours of user inactivity. Only a new inbound message from the authorized user renews this window; app-originated messages do not.

## Confirmed hierarchy

1. General Codex quota plus six Token activity cells: this Mac's ordinary input, cached input, output, previous-day total, and current-day total alongside the account's latest dated daily bucket.
2. A vertical KSF project workset containing only pinned projects and projects bound to currently running or waiting tasks, ordered by attention and recent activity, with the first four rows visible by default.
3. Each project row's stage or focus, task state, compact route, Token totals, folders, engineering roots, and declared launch actions.
4. The complete KSF project catalog, complete project details, and settings as secondary pages.

## Interaction contract

- Fixed 336pt popover. Its quota and Token regions never scroll; the project list gains a bounded vertical scroll area only after the user expands more than four projects.
- Every new opening returns to the home page and collapses the project list back to four rows.
- The project-list icon opens the complete KSF catalog as one continuous list; long catalogs scroll inside a bounded viewport without pagination or content-width changes when the scroll indicator appears.
- Project ordering is waiting first, running second, pinned-only last; recency breaks ties.
- Project assignment requires a verified KSF projection or an exact engineering-root `cwd` match.
- Opening the popover, waking the Mac, and existing timers refresh data; there is no refresh button.
- New tasks always use the configured KSF root as their Codex Desktop project root and receive the explicit name `项目名 · 新任务`. The exact task opens first; its visible Codex Desktop window then submits the compact KSF context-preparation turn and immediately shows the native active-reply state. Engineering-folder actions still use a menu when multiple roots exist.
- Only explicitly declared structured actions can run, and each opens a visible Terminal window.
- Settings owns a secondary WeChat connection page for QR authorization, connection status, test delivery, and disconnect. It never displays command contents or chat history.

## Trust boundaries

- KSF owns Markdown interpretation and exports `ksf-panel-catalog-v1`; the app is a read-only consumer.
- Route projection is display-only and never replaces the verified receipt, task trace, project memory contract, or governance authority.
- Raw task IDs are HMAC-addressed on disk. Task titles, conversation content, complete receipts, and governance evidence are never persisted.
- Project Token totals begin at verified binding time, include child agents by inheritance, split on later project bindings, and report incomplete local coverage instead of showing a false zero.
- Quota and the global task counter remain available when the KSF bridge fails.
- WeChat accepts text DMs only from the QR-authorizing user. It queues but never interprets or executes commands, never injects messages into Codex tasks, and never exposes a local network service.
- The bot token and queue encryption key stay in Keychain; the bounded command queue and protocol cursor are encrypted at rest.

## Not in v0.3

Automatic script discovery, background launch actions, environment variables or secrets in action manifests, project mutation, remote Token reconstruction, historical project attribution, charts, public distribution, KSF-wide bulk edits, WeChat groups, media, multiple accounts, arbitrary recipients, external IPC, or command execution.
