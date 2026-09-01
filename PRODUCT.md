# Codex Usage Bar product brief

## Product

Codex Usage Bar is a private macOS menu-bar instrument for KSF team members. It combines account capacity, live task attention, and each user's KSF project system in one glanceable surface.

## Current release

The `0.4.0-internal.1` team preview keeps the private WeChat message channel and KSF project workbench, adds explicit first-run configuration and isolated application data, and ships as an unnotarized universal2 build. The menu-bar label remains the stable quota and task signal; WeChat is an isolated transport for outbound notices and inbound queued commands, not a Codex conversation surface.

Because WeChat only permits bot replies within 24 hours of the user's latest message, the connector sends one renewal reminder after 23 hours of user inactivity. Only a new inbound message from the authorized user renews this window; app-originated messages do not.

## Confirmed hierarchy

1. General Codex quota plus six Token activity cells: this Mac's ordinary input, cached input, output, previous-day total, and current-day total alongside the account's latest dated daily bucket. A separate history control opens this Mac's recent daily totals.
2. A vertical KSF project workset containing pinned projects, projects bound to currently running or waiting tasks, and one transient `无项目` Cell when active top-level tasks have no resolvable project. The transient Cell appears first so it cannot be hidden beyond the initial four rows.
3. Each project row's task state and task-owned route details, plus project Token totals, folders, engineering roots, and one direct `项目目录/start.sh` launch action when available.
4. This Mac's 30-day Token history, the complete KSF project catalog, and settings as secondary pages.

## Interaction contract

- Fixed 336pt popover width with content-driven height. The home project workset and complete project catalog have no internal height cap or scroll view; their full contents expand the Popover naturally.
- Every new opening returns to the home page and collapses the project list back to four rows.
- The right-aligned Token-history icon opens a `本机每日 Token` secondary page. It reads the most recent 30 local natural days on demand and presents one non-scrolling bar trend with 30-day total, daily average, active-day count, a factual average reference, and exact selected-day composition. The latest day is selected by default; click, drag, or accessibility adjustment moves the selection. Zero-use dates remain explicit and account usage never enters this view.
- The project-list icon opens the complete KSF catalog as one continuous list. Every project appears at once without pagination or an internal scrollbar, and the Popover height follows the current catalog size.
- Catalog rows expose project identity, a catalog-only archive-task control, and an independent pin control on the first line, then reuse the home project's `累计 / 今日` Token values and five project actions on the second line. The archive control appears immediately before pin and creates and opens a named Codex task for archiving that exact project; the App never archives or moves project files itself. Catalog rows do not show `已固定`, `项目库`, aggregate task-state copy, task rows, or task routes; pin state remains visible only through the pin control.
- `无项目` appears first when needed. Normal project rows keep pinned projects first and preserve their stable first-seen order instead of moving with activity or recency.
- Project assignment requires a verified KSF projection or an exact engineering-root `cwd` match.
- An active top-level task that cannot resolve either assignment remains visible in `无项目`; this Cell is runtime-only, never enters the complete KSF catalog, and has no project Token, pin, memory, folder, launch, or new-task actions.
- Each task row shows its own category, main job, named abilities, and dispatchable-Skill count. Clicking the task body expands that task's complete route in place; a separate trailing control opens the exact Codex task. Only one task is expanded across the home surface at a time.
- The home project container opens the authoritative project memory card directly; the app has no project-detail page or selected-project state.
- Opening the popover, waking the Mac, and existing timers refresh data; there is no refresh button.
- New tasks always use the configured KSF root as their Codex Desktop project root and receive the explicit name `项目名 · 新任务`. The exact task opens first; its visible Codex Desktop window then submits the compact KSF context-preparation turn and immediately shows the native active-reply state. Engineering-folder actions still use a menu when multiple roots exist.
- Project launch is a direct user action and only recognizes an executable `start.sh` in the KSF project directory. It opens a visible Terminal in that directory and executes the script; Git mappings and action manifests do not participate.
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

Other script discovery, background launch actions, launch arguments or environment injection, project mutation, remote Token reconstruction, historical project attribution, long-range analytics dashboards, public distribution, KSF-wide bulk edits, WeChat groups, media, multiple accounts, arbitrary recipients, external IPC, or remote command execution.
