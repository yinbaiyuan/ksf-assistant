# Codex Usage Bar

A native macOS menu bar app for KSF teams. It shows remaining Codex quota, live top-level task activity, a compact KSF project workbench, and a private WeChat message channel.

Current team preview: `0.4.0-internal.1`. The app is MIT-licensed; internal binary builds are ad-hoc signed and not notarized.

Private source repository: `git@gitlab.houzzkit.com:costudyteam/codex-usage-bar.git`.

Protocol source: [OpenAI Codex App Server — rate limits](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt).

## Product promise

- The menu bar percentage is remaining capacity for the general `codex` bucket: `100 - usedPercent`, using the most constrained general window.
- The popover intentionally omits model-specific buckets; they never change the headline value.
- Token activity is historical activity, not an exact remaining-token balance. The account metric shows the latest server-published daily bucket as today, yesterday, or an older date; this Mac's previous- and current-day totals plus today's ordinary-input, cached-input, and output split are derived independently from active and archived local Codex session counter events. The Token heading also opens a 30-day local trend backed by a monotonic daily cache under the configured KSF root. Account and local scopes are not assumed comparable.
- While at least one top-level task is confirmed running, this Mac's current-day Token total refreshes immediately and then every 10 seconds. The local-only timer stops when running reaches zero or task state becomes unavailable; popover-open, wake, and the existing 30-minute account refresh remain unchanged.
- The app reuses Codex's managed login through `codex app-server`; it never reads or copies credential files.
- The menu bar renders `Codex 图标 N% N.NM [play.fill]N [person.fill.questionmark]N`. `N.NM` is this Mac's current-date usage and always stays in one-decimal million units, including below one million and above one billion; running and waiting counts remain present, including zero.
- Task activity covers top-level local and remote Codex tasks managed by the desktop app, across projects. Waiting tasks are excluded from the running count; ChatGPT conversations, standalone CLI sessions, and sub-agents are excluded.
- The 336pt Simplified Chinese popover puts general quota and Token activity first, with a right-aligned history icon that opens a compact, non-scrolling 30-day local-Token trend, then shows project containers limited to pinned projects and projects with running or waiting tasks. Each container lists every real active task with its original Codex name, exact waiting/running state, and its own KSF route; clicking the row opens that exact Codex task. Pinned projects stay above current-task projects, project order survives restarts, and task order stays stable for the app session. The first four projects are included by default with natural height and no internal project scrollbar; `更多` grows the Popover with the remaining workset. The project-list icon opens the complete catalog at once, grows the Popover to fit every row, and repeats each project's home Footer with Token values and project actions instead of status subtitles. A catalog-only archive icon immediately before pin creates and opens a scoped Codex archive task; the App itself does not mutate project files.
- KSF project assignment is conservative: a task needs a verified local KSF projection or an exact engineering-root `cwd` match. Non-project tasks remain outside project totals.
- Project Token accounting starts at verified binding time, follows child agents, and splits later increments when one task changes projects. Remote or missing local logs are reported as incomplete instead of zero.
- Project launch recognizes only an executable `start.sh` directly inside the KSF project directory. It never guesses commands from Git roots, `package.json`, Makefiles, or other project files.
- Each project Cell has an explicit new-task shortcut. It creates the task with the configured KSF root as `cwd`, so Codex Desktop places it in the saved KSF project. Every declared Git mapping is passed only as business context. The first turn reads the KSF entry, project memory card, and minimum required context, then waits for the user's next instruction without beginning implementation.
- Settings can connect one WeChat account through Tencent iLink QR authorization. The app sends internal notices to the authorizing user and stores that user's inbound text commands in a bounded encrypted queue; it does not execute them or route them into Codex.

## Requirements

- macOS 13 or newer on Apple silicon
- Swift 5.8 or newer Command Line Tools
- A Codex CLI build that supports `account/rateLimits/read` and `account/usage/read`
- ChatGPT-backed Codex authentication
- Codex desktop app for live task counts; when it is not running, the counts are `0 / 0`
- A compatible KSF root containing `AGENTS.md` and the standard panel bridge

See [compatibility](docs/COMPATIBILITY.md) for protocol requirements and independent failure boundaries.

## First launch

The first-run page suggests `~/Documents/KSF` only when it exists. The user must confirm or choose the KSF root, then the app validates `AGENTS.md`, the panel bridge, and the catalog protocol before enabling the project workbench and v2 Token history. Login launch and quota-reset notifications are off until the user explicitly enables them. WeChat remains disconnected until QR authorization.

## Build and install

```bash
scripts/run-tests.sh
scripts/live-smoke-test.sh
scripts/build-app.sh
scripts/install-local.sh
open "$HOME/Applications/Codex Usage Bar.app"
```

The build produces a universal2 app for Apple Silicon and Intel. `scripts/install-local.sh` installs to `$HOME/Applications` by default; override with `INSTALL_ROOT=/explicit/path`. If that folder contains the old personal Bundle ID, the script preserves it and installs the team build as `Codex Usage Bar Team.app`. `INSTALL_APP_NAME` can explicitly select another single filename. The app has no Dock icon and does not register as a login item until the user opts in.

`SIGNING_MODE=adhoc` is the default and is used for the internal ZIP. To use a local certificate-backed identity, run `SIGNING_MODE=identity SIGNING_IDENTITY="Your Identity" scripts/build-app.sh`. No private certificate name or Keychain path is stored in the repository.

The shared ZIP is not notarized. Verify `SHA256SUMS`, unzip it, then use Finder's **Open** context-menu action if Gatekeeper blocks the first launch. Team members must understand that macOS can ask again after an update. Do not remove quarantine attributes with broad recursive commands.

On a current Command Line Tools installation, the test command uses Swift Package Manager and XCTest. A direct-compile fallback covers older Command Line Tools. No Xcode project is required.

To prepare a clean release after committing all reviewed changes, run `scripts/package-release.sh`. It creates the universal ZIP, source archive, checksums, and an explicit non-notarized release notice. Follow the [release checklist](docs/RELEASE_CHECKLIST.md).

## KSF project bridge

KSF owns project-card interpretation and exports `ksf-panel-catalog-v1`; the app does not parse arbitrary Chinese Markdown. After an ordinary `verified-route-projection-v6` succeeds with exactly one project-memory root card, the wrapper can publish a display-only `ksf-task-project-projection-v1`. Raw task IDs are HMACed with a local 0600 key before any filename or projection is written. A missing bridge, an incompatible protocol, or an unavailable KSF directory only disables the project region; quota and the global task counter continue independently.

Project launch is a fixed convention: `<projectDirectory>/start.sh`. The script must be a current-user-owned regular file, cannot be a symbolic link or group/other writable, and must have owner execution permission. A click revalidates it, opens a visible Terminal in the KSF project directory, executes the script, displays the exit status, and leaves the shell open. A missing or unsafe script never falls back to another command or Git engineering root.

The project's `plus.bubble` shortcut first creates an idle task through Codex App Server `thread/start` and assigns the explicit name `项目名 · 新任务` through `thread/name/set`. It then opens that exact task, waits for the visible Codex Desktop window to report that it is following the task, and submits the bootstrap input directly to that same window through desktop `thread-follower-start-turn` v2. A fresh window can report following just before it finishes becoming the stream owner, so the adapter retries only the unambiguous `no-client-found` response for up to 10 seconds. It does not retry timeouts or other errors, and it does not run a second owner-discovery round trip. The independent App Server never starts the first turn, so the prompt and live reply belong to the foreground page. `thread/start.cwd` and the submitted turn `cwd` are both the configured KSF root, which is the Codex Desktop project root. The compact bootstrap input identifies the project and its memory card, asks KSF to load the normal base context, forbids concrete work/file changes/implementation plans, and waits for the user's next instruction. KSF's own rules remain responsible for context loading, while the user's existing thread settings remain unchanged.

## Privacy and scope

Quota and Token activity use the documented Codex App Server account endpoints. Live task state uses a separate desktop IPC adapter because an independent App Server process cannot observe other desktop-managed processes in real time. The read-only thread catalog and conservative KSF assignment produce an in-memory set of local, top-level project candidates; the adapter actively discovers their desktop owners so a task remains observable while it runs behind another Codex page. Window `following` broadcasts remain a fast discovery hint, but losing the last window follower no longer proves that the task stopped. The adapter connects only to `~/.codex/ipc/ipc.sock`, requires a current-user-owned Unix Socket with no group/other permissions, and currently supports `thread-owner-discovery` v1, `thread-stream-following-changed` v1, `thread-stream-state-changed` v11, plus the narrowly scoped `thread-follower-start-turn` v2 used only after the user presses a project's new-task button. A desktop update can make this private protocol temporarily incompatible; that failure is isolated from quota reads.

The app stores normalized quota snapshots and per-project Token totals under `~/Library/Application Support/com.ksf.codexusagebar`. The full-Mac daily Token cache is stored at `.agents/runtime-data/codex-usage-bar/token-history-v2.json` inside the configured KSF root, is ignored by Git, and contains only dates, aggregate Token counts, composition, and observation timestamps. Live task identity, original names, state, project task arrays, first-seen task order, new-task bootstrap prompts, and returned raw thread IDs remain memory-only and are never written to UserDefaults or app logs. A bootstrap prompt is retained inside the user-created Codex task itself, as expected for its first turn. The KSF projection stores an HMAC task key, relative project card, compact route names/IDs, binding timestamps, and a receipt hash; it excludes raw task IDs, task titles, conversation content, full receipts, and governance evidence. Project Token inspection selects only session identity/parent metadata, timestamps, and `token_count` cumulative counters from local JSONL and never caches raw events. The app excludes raw Codex protocol payloads, Codex account identity, Codex authentication tokens, credential paths, and reset-credit identifiers. The team preview does not take over Codex login, redeem credits, modify account or KSF project state beyond that scoped local cache, expose a local server, accept remote commands, or publish telemetry.

The WeChat bot token and queue encryption key are stored in macOS Keychain. Account routing state, sync cursor, context token, deduplication keys, and at most 100 commands are stored as one AES-GCM encrypted Application Support file and expire after 30 days. Only the QR-authorizing user's text DMs are accepted. Message bodies, user identifiers, tokens, and context tokens are excluded from logs. Disconnecting deletes the credentials and encrypted state. WeChat failures are isolated from all Codex and KSF functions.

See `docs/prd/v0.3.md` for current behavior and acceptance rules, `PRODUCT.md` for the product brief, and `DESIGN.md` for visual rules.
