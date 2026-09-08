# Shared Core v1

## Boundary

`ksf-assistant-core` is the single cross-platform business implementation. It is a native Go child process started by each desktop host and communicates over newline-delimited JSON-RPC 2.0 on private stdin/stdout.

```text
macOS SwiftUI/AppKit ─┐
                     ├─ ksf-assistant-core ─ Codex App Server
Windows Electron ────┘                   ├ KSF bridge
                                         ├ Feishu bridge
                                         ├ local session counters
                                         └ Codex Desktop IPC
```

The core owns:

- account quota and Token normalization;
- local Token delta accounting and project attribution;
- KSF catalog/projection reads and conservative task assignment;
- running/waiting/completed classification;
- authoritative task bootstrap creation;
- project launch discovery and revalidation;
- Feishu readiness, task links, interruption and test delivery;
- refresh cadence and stale-data behavior.

The hosts own only platform interaction:

- tray/menu-bar lifecycle and content-driven panel geometry;
- native directory pickers and login-item integration;
- Codex deep-link opening;
- visible Terminal or PowerShell windows;
- platform typography, controls, accessibility and theme behavior.

## Trust model

Renderers submit identifiers and user intent, not authoritative paths or state. For project task creation and launch, the core reloads the current KSF catalog, resolves the project ID, canonicalizes paths and rejects entries outside the configured KSF root. The Windows renderer runs with Node disabled, context isolation and Chromium sandboxing; only preload allowlisted actions can reach the main process.

No credential material crosses this protocol. Codex login stays managed by Codex, while Feishu identity, IDs, lease state, messages and secrets remain bridge-owned.

KSF Markdown interpretation remains in the KSF-owned Ruby bridge. The Go process supplies a platform-correct per-user support directory and consumes only the versioned catalog/projection JSON. This keeps one KSFAssistant business core without creating a second KSF parser.

## Protocol

- Name: `ksf-assistant-core-v1`
- Core version: `0.8.0`
- Transport: UTF-8 NDJSON, one JSON-RPC object per line
- Current methods: `initialize`, `health/read`, `dashboard/read`, `pricing/catalog/read`, `token/history/read`, `token/history/compare`, `task/create`, `task/submit`, `project/launch/prepare`, `feishu/taskLink/create`, `feishu/taskLink/release`, `feishu/taskLink/interrupt`, `feishu/test`, `shutdown`

`dashboard/read` is a sanitized aggregate. It excludes raw Codex protocol traffic, prompts, responses, credentials and internal Feishu identifiers. Expensive sources have independent refresh intervals so a three-second live-state poll does not repeatedly scan all session history or KSF state.

Local Token history treats `total_token_usage` as a cumulative counter scoped to a Codex task lineage, not to each JSONL file. Session metadata (`id`, `parent_thread_id` and `forked_from_id`) forms that lineage. Top-level tasks remain independent, while parent and sub-agent files in one lineage are merged into a monotonic cumulative envelope before natural-day deltas are calculated. This prevents the same task-wide counter propagated to many sub-agent logs from being counted repeatedly; active/archive copies are still deduplicated by session ID first. A counter decrease starts a new cumulative segment, and both its total and composition restart from zero so one reset cannot invalidate the day's breakdown. The compatibility Swift reader mirrors the same rule.

`token/history/compare` uses the local 30-day calendar as its date spine and joins `account/usage/read.dailyUsageBuckets` by exact `startDate`. It returns nullable `serverTokens`, local totals and local composition for each day. Missing server dates remain unavailable rather than becoming zero, and the hosts calculate the displayed local/server percentage only when the same-date server denominator is positive. The older local-only method remains available for host compatibility.

API pricing is a shared-core business rule. `pricing/catalog/read` combines seven versioned read-only presets with at most 20 validated host-provided custom plans. `dashboard/read` and `token/history/compare` accept the same `pricingSelection`; the core calculates all amounts as integer micro-USD and reports complete, partial, or unavailable coverage. `repriceOnly` reuses already loaded local and server history, so changing a selector never triggers another session scan or account request. Hosts persist only the selected ID and custom definitions; invalid or removed IDs fall back to the default preset.

## Compatibility fallback

The macOS host retains the pre-0.8 Swift clients as a temporary startup fallback if the bundled shared core cannot launch. They are not used when the packaged core is healthy and may be removed after the Windows and macOS 0.8 field-validation window.

### Desktop task creation handoff

`task/create` returns `submission: "desktop-prepared-context"`. Core creates a dedicated draft and persists only factual task context (`taskName`, `workingDirectory`) via the documented [thread/inject_items API](https://learn.chatgpt.com/docs/app-server#inject-items-into-a-thread), without generation. It waits for that writer process to exit before the host opens the task. The actual bootstrap instruction is not injected: raw prompt history has no canonical `userMessage` event and does not produce a Desktop message bubble.

`task/submit` accepts only the matching, unexpired preparation from this Core lifetime, consumes it before dispatch, then sends the exact prompt as one normal text input through Desktop. This generates one persistent user message followed by the assistant response. Model, permissions, sandbox and reasoning settings continue to come from Desktop. Task control uses a short-lived IPC connection without dashboard subscriptions. Ownership queries may retry within a bounded readiness window; the turn-start operation is never automatically replayed.

The small initialization context is a compatibility adapter for Codex releases that reserve empty thread IDs without materializing resumable history. It adds task metadata to prompt history but no model turn or duplicate instruction. Remove this adapter when a supported Codex API can create a durable empty draft; validate cross-process resume, writer release, and one visible canonical user message before switching. Do not fabricate assistant history, rewrite Codex session files, or start a background turn to force persistence.

Pending preparations are memory-only and expire after 10 minutes; restarting Core deliberately does not replay them. If startup cannot be confirmed, the host directs the user to continue in the existing task. Existing completed tasks are not retroactively rewritten or rerun. Validation on bundled Codex 0.153.4 covers the previous `rollout_not_found` failure and checks that the repaired flow retains exactly one user message and one final reply in turn history. Unsupported injection fails explicitly.
