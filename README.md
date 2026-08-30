# Codex Usage Bar

A native macOS menu bar app that shows remaining Codex quota, live top-level task activity, a compact KSF project workbench, and a private WeChat message channel.

Protocol source: [OpenAI Codex App Server — rate limits](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt).

## Product promise

- The menu bar percentage is remaining capacity for the general `codex` bucket: `100 - usedPercent`, using the most constrained general window.
- The popover intentionally omits model-specific buckets; they never change the headline value.
- Token activity is historical activity, not an exact remaining-token balance. The account metric shows the latest server-published daily bucket as today, yesterday, or an older date; this Mac's previous- and current-day totals plus today's ordinary-input, cached-input, and output split are derived independently from local Codex session counter events. Account and local scopes are not assumed comparable.
- While at least one top-level task is confirmed running, this Mac's current-day Token total refreshes immediately and then every 10 seconds. The local-only timer stops when running reaches zero or task state becomes unavailable; popover-open, wake, and the existing 30-minute account refresh remain unchanged.
- The app reuses Codex's managed login through `codex app-server`; it never reads or copies credential files.
- The menu bar renders `Codex 图标 N% N.NM [play.fill]N [person.fill.questionmark]N`. `N.NM` is this Mac's current-date usage and always stays in one-decimal million units, including below one million and above one billion; running and waiting counts remain present, including zero.
- Task activity covers top-level local and remote Codex tasks managed by the desktop app, across projects. Waiting tasks are excluded from the running count; ChatGPT conversations, standalone CLI sessions, and sub-agents are excluded.
- The 336pt Simplified Chinese popover puts general quota and Token activity first, then shows project containers limited to pinned projects and projects with running or waiting tasks. Each container lists every real active task with its original Codex name, exact waiting/running state, and its own KSF route; clicking the row opens that exact Codex task. Pinned projects stay above current-task projects, project order survives restarts, and task order stays stable for the app session. The first four projects are included by default; only their project region scrolls when complete task content exceeds about 360pt, while `更多` adds the remaining workset and the project-list icon opens a compact complete catalog.
- KSF project assignment is conservative: a task needs a verified local KSF projection or an exact engineering-root `cwd` match. Non-project tasks remain outside project totals.
- Project Token accounting starts at verified binding time, follows child agents, and splits later increments when one task changes projects. Remote or missing local logs are reported as incomplete instead of zero.
- Project launch actions are opt-in structured manifests. The app never guesses scripts from `package.json`, Makefiles, or other project files.
- Each project Cell has an explicit new-task shortcut. It creates the task with the configured KSF root as `cwd`, so Codex Desktop places it in the saved KSF project. Every declared Git mapping is passed only as business context. The first turn reads the KSF entry, project memory card, and minimum required context, then waits for the user's next instruction without beginning implementation.
- Settings can connect one WeChat account through Tencent iLink QR authorization. The app sends internal notices to the authorizing user and stores that user's inbound text commands in a bounded encrypted queue; it does not execute them or route them into Codex.

## Requirements

- macOS 13 or newer on Apple silicon
- Swift 5.8 or newer Command Line Tools
- A Codex CLI build that supports `account/rateLimits/read` and `account/usage/read`
- ChatGPT-backed Codex authentication
- Codex desktop app for live task counts; when it is not running, the counts are `0 / 0`
- KSF at `/Users/lawis/Documents/KSF` for the project workbench; a different root can be selected in settings

Verified locally with Codex CLI 0.144.1 on macOS 14.4.1.

## Build and install

```bash
scripts/run-tests.sh
scripts/live-smoke-test.sh
scripts/build-app.sh
scripts/install-local.sh
open "/Users/lawis/Applications/Codex Usage Bar.app"
```

The install script places a locally signed app at `/Users/lawis/Applications/Codex Usage Bar.app`. The app has no Dock icon and registers itself as a login item on first successful launch unless the user turns that setting off.

For local self-use, every build is signed with the trusted `Codex Usage Bar Local Signing` identity in the user's login Keychain. Its certificate-backed designated requirement remains stable across rebuilds, allowing macOS to retain Documents access and Keychain authorization without recurring password prompts. The build fails closed when that identity is missing rather than falling back to ad-hoc signing. A future distributed release must replace this local-only identity with Developer ID signing.

On a current Command Line Tools installation, the test command uses Swift Package Manager and XCTest. The local machine's older CLT 14.3 cannot report an SDK `PlatformPath`, so the same command automatically runs direct-compile model/protocol tests and an arm64 app build instead. No full Xcode installation is required.

## KSF project bridge

KSF owns project-card interpretation and exports `ksf-panel-catalog-v1`; the app does not parse arbitrary Chinese Markdown. After an ordinary `verified-route-projection-v6` succeeds with exactly one project-memory root card, the wrapper can publish a display-only `ksf-task-project-projection-v1`. Raw task IDs are HMACed with a local 0600 key before any filename or projection is written. A missing bridge, an incompatible protocol, or an unavailable KSF directory only disables the project region; quota and the global task counter continue independently.

Each mapped Git root may declare `codex-usage-bar.actions.json`:

```json
{
  "protocol": "codex-usage-bar-actions-v1",
  "actions": [
    {
      "id": "dev",
      "title": "启动开发环境",
      "symbol": "play.fill",
      "primary": true,
      "executable": "/usr/bin/env",
      "arguments": ["swift", "run"],
      "workingDirectory": "."
    }
  ]
}
```

The working directory must resolve inside that Git root. The current user must own the regular manifest file, and group/other write permissions are rejected. Up to three `primary` actions appear on the home page; all actions remain available in project details. A click opens a visible Terminal window, runs the exact executable plus argument array, displays the exit status, and leaves a shell open.

The project's `plus.bubble` shortcut first creates an idle task through Codex App Server `thread/start` and assigns the explicit name `项目名 · 新任务` through `thread/name/set`. It then opens that exact task, waits for the visible Codex Desktop window to report that it is following the task, and submits the bootstrap input directly to that same window through desktop `thread-follower-start-turn` v2. A fresh window can report following just before it finishes becoming the stream owner, so the adapter retries only the unambiguous `no-client-found` response for up to 10 seconds. It does not retry timeouts or other errors, and it does not run a second owner-discovery round trip. The independent App Server never starts the first turn, so the prompt and live reply belong to the foreground page. `thread/start.cwd` and the submitted turn `cwd` are both the configured KSF root, which is the Codex Desktop project root. The compact bootstrap input identifies the project and its memory card, asks KSF to load the normal base context, forbids concrete work/file changes/implementation plans, and waits for the user's next instruction. KSF's own rules remain responsible for context loading, while the user's existing thread settings remain unchanged.

## Privacy and scope

Quota and Token activity use the documented Codex App Server account endpoints. Live task state uses a separate desktop IPC adapter because an independent App Server process cannot observe other desktop-managed processes in real time. The read-only thread catalog and conservative KSF assignment produce an in-memory set of local, top-level project candidates; the adapter actively discovers their desktop owners so a task remains observable while it runs behind another Codex page. Window `following` broadcasts remain a fast discovery hint, but losing the last window follower no longer proves that the task stopped. The adapter connects only to `~/.codex/ipc/ipc.sock`, requires a current-user-owned Unix Socket with no group/other permissions, and currently supports `thread-owner-discovery` v1, `thread-stream-following-changed` v1, `thread-stream-state-changed` v11, plus the narrowly scoped `thread-follower-start-turn` v2 used only after the user presses a project's new-task button. A desktop update can make this private protocol temporarily incompatible; that failure is isolated from quota reads.

The app stores normalized quota/Token snapshots and per-project Token totals under Application Support. Live task identity, original names, state, project task arrays, first-seen task order, new-task bootstrap prompts, and returned raw thread IDs remain memory-only and are never written to UserDefaults or app logs. A bootstrap prompt is retained inside the user-created Codex task itself, as expected for its first turn. The KSF projection stores an HMAC task key, relative project card, compact route names/IDs, binding timestamps, and a receipt hash; it excludes raw task IDs, task titles, conversation content, full receipts, and governance evidence. Project Token inspection selects only session identity/parent metadata, timestamps, and `token_count` counters from local JSONL and never caches raw events. The app excludes raw Codex protocol payloads, Codex account identity, Codex authentication tokens, credential paths, and reset-credit identifiers. v0.3 does not take over Codex login, redeem credits, modify account or KSF project state, expose a local server, or publish telemetry.

The WeChat bot token and queue encryption key are stored in macOS Keychain. Account routing state, sync cursor, context token, deduplication keys, and at most 100 commands are stored as one AES-GCM encrypted Application Support file and expire after 30 days. Only the QR-authorizing user's text DMs are accepted. Message bodies, user identifiers, tokens, and context tokens are excluded from logs. Disconnecting deletes the credentials and encrypted state. WeChat failures are isolated from all Codex and KSF functions.

See `docs/prd/v0.3.md` for current behavior and acceptance rules, `PRODUCT.md` for the product brief, and `DESIGN.md` for visual rules.
