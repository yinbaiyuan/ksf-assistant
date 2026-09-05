# KSFAssistant product brief

KSFAssistant means Knowledge–Skills–Flywheel Assistant（知识－技能－飞轮－助手）.

## Product

KSFAssistant is an open-source macOS menu-bar and Windows system-tray instrument for Codex users. It combines account capacity and live task attention in one glanceable surface, with optional KSF and Feishu integrations.

## Current release

The prepared `0.10.0-preview.1` public preview keeps the platform UI, shared Go core, and supervised Feishu Bridge in one product. Ordinary users configure integrations entirely inside the software and do not install runtimes or operate background services.

## Confirmed hierarchy

1. General Codex quota plus six Token activity cells: this device's ordinary input, cached input, output, previous-day total, and current-day total alongside the account's latest dated daily bucket. A compact footer shows the selected API estimate and today's amount. A separate history control opens a 30-day comparison of server-published account usage and this device's local usage.
2. A vertical KSF project workset containing pinned projects and unpinned projects with currently running or waiting tasks, plus one transient `无项目` Cell when an active top-level task has no resolvable project. Completed tasks remain available inside a visible project Cell so the user can reopen and continue them, but completed tasks alone never keep an unpinned project on the home surface. The transient Cell appears first so it cannot be hidden beyond the initial four rows.
3. Each project row's task state and task-owned route details, plus project Token totals, folders, engineering roots, and one direct platform launch action (`start.sh` on macOS or `start.ps1` on Windows) when available.
4. The 30-day server/device Token comparison, the complete KSF project catalog, and settings as secondary pages.

## Interaction contract

- Fixed 336pt popover width with content-driven height. The home project workset and complete project catalog have no internal height cap or scroll view; their full contents expand the Popover naturally.
- Every new opening returns to the home page and collapses the project list back to four rows.
- The right-aligned Token-history icon opens a `每日 Token` secondary page. It reads the most recent 30 natural days on demand, aligns server-published account totals with local reconstructed totals, and presents one non-scrolling overlay trend with local summary, exact selected-day comparison, local/server percentage, and local composition. The latest day is selected by default; click, drag, or accessibility adjustment moves the selection. Zero-use local dates remain explicit, while absent server dates remain unavailable.
- The daily page selects one current API pricing plan and immediately revalues already loaded local history without rescanning session logs. It shows the known 30-day subtotal, selected-day total, and component costs; days without complete Token composition remain unpriced and are counted as missing. Seven built-in plans are read-only, while up to 20 custom plans remain local to one device.
- The project-list icon opens the complete KSF catalog as one continuous list. Every project appears at once without pagination or an internal scrollbar, and the Popover height follows the current catalog size.
- Catalog rows expose project identity, a catalog-only archive-task control, and an independent pin control on the first line, then reuse the home project's `累计 / 今日` Token values and five project actions on the second line. The archive control appears immediately before pin and creates and opens a named Codex task for archiving that exact project; the App never archives or moves project files itself. Catalog rows do not show `已固定`, `项目库`, aggregate task-state copy, task rows, or task routes; pin state remains visible only through the pin control.
- `无项目` appears first when needed. Normal project rows keep pinned projects first and preserve their stable first-seen order instead of moving with activity or recency.
- Project assignment requires a verified KSF projection or an exact engineering-root `cwd` match.
- An active top-level task that cannot resolve either assignment remains visible in `无项目`; this Cell is runtime-only, never enters the complete KSF catalog, and has no project Token, pin, memory, folder, launch, or new-task actions.
- Each task row shows its own category, main job, named abilities, and dispatchable-Skill count in three stable visual levels. Running, waiting, and completed states use blue, orange, and green semantic treatments; category, job, ability, and Skill use indigo, purple, teal, and blue accents that always retain their icon or text label. Clicking the task body opens the exact Codex task. A separate `info.circle` control opens a task-detail secondary page with the complete route plus Codex and Feishu actions; the home surface has no inline task expansion.
- The home project container opens the authoritative project memory card directly; the app has no project-detail page or selected-project state.
- Opening the panel, waking the device, and existing timers refresh data; platform shells may expose an explicit refresh control when that matches native conventions.
- New tasks always use the configured KSF root as their Codex Desktop project root and receive the explicit name `项目名 · 新任务`. The exact task opens first; its visible Codex Desktop window then submits the compact KSF context-preparation turn and immediately shows the native active-reply state. Engineering-folder actions still use a menu when multiple roots exist.
- Project launch is a direct user action and only recognizes `start.sh` on macOS or `start.ps1` on Windows in the KSF project directory. It opens a visible Terminal or PowerShell window in that directory and executes the script; Git mappings and action manifests do not participate.
- Settings owns a resumable Feishu setup page with new-app and existing-app paths, factual bridge status, sanitized target-alias selection, and explicit test delivery. It never displays real target IDs, credentials, command contents, or chat history.
- The daily Token page uses the server-published account bucket as the full bar and overlays this device's independently reconstructed local usage. Selecting a date shows both exact totals and the unclamped local/server percentage. A missing server date remains `未同步`, never zero; the page states that server publication can lag and retains the local input/cache/output composition.

## Trust boundaries

- Platform renderers do not receive filesystem, process or credential capabilities. They call a narrow host API, and the host delegates business actions to `ksf-assistant-core` over private stdio JSON-RPC.
- The core re-resolves project IDs against the current KSF catalog before task creation or project launch. It does not trust renderer-provided project paths, titles or state.
- KSF owns Markdown interpretation and exports `ksf-panel-catalog-v1`; the app is a read-only consumer.
- Route projection is display-only and never replaces the verified receipt, task trace, project memory contract, or governance authority.
- Raw task IDs are HMAC-addressed on disk. Task titles, conversation content, complete receipts, and governance evidence are never persisted.
- Project Token totals begin at verified binding time, include child agents by inheritance, split on later project bindings, and report incomplete local coverage instead of showing a false zero.
- Quota and the global task counter remain available when the KSF bridge fails.
- KSFAssistant owns the isolated Feishu Bridge process but not its authority. The bridge implements transport and inbound handling, derives authoritative task state, enforces the exact direct-message operator, observes/steers/interrupts Codex turns, handles ordinary attachments and non-secret input, and owns the 24-hour inactivity lease, redaction and wake assertion.
- The bot token and queue encryption key stay in Keychain; the bounded command queue and protocol cursor are encrypted at rest.

## Not in this preview

Automatic updates, other script discovery, background launch actions, launch arguments or user environment injection, project mutation, remote Token reconstruction, historical project attribution, long-range analytics dashboards, KSF-wide bulk edits, automatic global task linking, group control, arbitrary target IDs, secret relay, or native Codex remote-pairing transport.
