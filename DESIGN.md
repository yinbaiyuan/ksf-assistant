---
version: 0.7
name: Native Cross-platform Project Instrument
description: One compact project instrument with platform-native macOS and Windows shells over a shared business core.
colors:
  primary: "#0A84FF"
  success: "#30D158"
  warning: "#FF9F0A"
  error: "#FF453A"
  neutral: "#8E8E93"
  surface: "#F2F2F7"
  on-surface: "#1C1C1E"
typography:
  metric:
    fontFamily: SF Pro Rounded
    fontSize: 30px
    fontWeight: 600
    lineHeight: 1.1
  headline:
    fontFamily: SF Pro Text
    fontSize: 13px
    fontWeight: 600
    lineHeight: 1.2
  body:
    fontFamily: SF Pro Text
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.35
  caption:
    fontFamily: SF Pro Text
    fontSize: 10px
    fontWeight: 400
    lineHeight: 1.3
rounded:
  sm: 6px
  md: 10px
  lg: 14px
  full: 9999px
spacing:
  xs: 4px
  sm: 8px
  md: 12px
  lg: 16px
  xl: 24px
components:
  quota-card:
    rounded: "{rounded.md}"
    padding: "{spacing.md}"
    backgroundColor: "{colors.surface}"
  progress-critical:
    backgroundColor: "{colors.error}"
  quota-healthy:
    textColor: "{colors.success}"
  quota-warning:
    textColor: "{colors.warning}"
  secondary-label:
    textColor: "{colors.neutral}"
  popover:
    width: 336px
---

# KSFAssistant visual system

## Overview

The app should feel like a system instrument: compact, quiet, and precise. It serves any Codex user; KSF is an optional project-workbench integration rather than a prerequisite. macOS follows AppKit/SwiftUI conventions and Windows follows Windows 11 tray-flyout and Fluent conventions. Both keep the same information hierarchy and Feishu setup state machine without forcing identical pixels. Exclude marketing surfaces, custom brand illustration, decorative gradients, and dashboard-like chrome.

## Windows adaptation

Use Segoe UI Variable/Text, a 392px fixed-width frameless tray flyout, semantic Windows surface colors, restrained one-pixel borders, and content-driven height bounded only by the display work area. Keep the renderer Node-free and keyboard accessible. Home, project catalog, project detail and settings remain separate compact surfaces; never put a scrollbar inside the project list. Use Windows system dialogs, login-item behavior and visible PowerShell windows instead of imitating macOS sheets, ServiceManagement or Terminal. Dark mode, high-contrast focus visibility and reduced motion are required.

## Colors

Use native semantic backgrounds and labels wherever SwiftUI provides them. `{colors.primary}` marks interactive or current state, `{colors.success}` healthy capacity, `{colors.warning}` low capacity, and `{colors.error}` exhausted capacity or blocking failures. Task state uses blue for running, orange for waiting, green for completed, and red for failure. On the home workset, route names, ability names, Skill counts, project counts, pins, detail controls, Footer metrics, and idle project actions stay neutral so each task has only one competing semantic state color. On the task-detail page, category, job, ability, and Skill symbols may use indigo, purple, teal, and blue while their values remain neutral; validation and Skill stage use compact semantic text. Tints stay localized to status, small symbols, and the one primary action—never large decorative fields or simultaneous colored paragraphs. Color is always paired with text or an SF Symbol.

## Typography

Use the system font in implementation; the token families describe the macOS roles. The remaining percentage uses `{typography.metric}` with monospaced digits. Section labels use `{typography.headline}`, values use `{typography.body}`, and timestamps use `{typography.caption}`. Never present token activity with greater emphasis than remaining capacity.

## Layout

The popover is fixed at `{components.popover.width}` with `{spacing.md}` outer padding and content-driven height. The home page uses one compact vertical flow: general quota, Token activity, then a vertical KSF project workset containing pinned projects and unpinned projects with running or waiting tasks, plus a transient `无项目` Cell when active top-level tasks have no resolvable project. A visible project Cell may include completed tasks, but completed tasks alone never keep an unpinned project on the home surface. Put the exception Cell first so it remains in the initial viewport; pinned rows then precede unpinned active rows, and first-seen order remains stable inside those normal groups. Each container lists its real task rows, not a summary of one representative task. The first four containers are included by default with no internal workset height cap or scroll view; `更多` includes the remaining workset containers and lets the Popover continue growing naturally. The local Token-history page and complete project catalog likewise use natural content height without internal scrolling. Settings normally grows to its content. Only the Feishu page uses a native scroll fallback when expanded authorization, QR content, or diagnostics would exceed the display work area; never clip recovery controls. Use `{spacing.xs}` inside dense groups, `{spacing.sm}` between related groups, and `{spacing.md}` only between major sections. Do not use tabs or a sidebar.

## Elevation & Depth

Rely on native popover material, grouped tonal surfaces, and dividers. Do not add custom drop shadows inside the panel.

## Shapes

Cards use `{rounded.md}`. Progress tracks and status chips use `{rounded.full}`. Buttons retain native macOS shapes and hit areas.

## Components

- **First run:** Open directly on quota and Token status. Do not gate the app on KSF, Feishu, a project directory, or a separate onboarding completion flag. Hide the KSF project workbench until a directory has been selected and automatically validated in Settings. Describe KSF as an optional enhancement and never silently accept a suggested path.

- **Menu label:** Use the selected 24×24 Codex mark (OpenAI blossom with `>_`) at 16pt, then render `N%`, this Mac's current-date Token total as `N.NM`, `play.fill N`, and `person.fill.questionmark N` in that order. The Token value always uses one-decimal million units: it never switches to K or B, uses `…M` before the first current-date scan, and uses `—M` only when the local source is unavailable. Compose the complete line into one exact-intrinsic-width 18pt-high macOS template `NSImage` and assign it directly to an AppKit `NSStatusItem` button; SwiftUI remains responsible for the popover content, not status-item layout. Use 12pt rounded monospaced digits, a 9pt `play.fill`, and a 13pt `person.fill.questionmark` whose extra intrinsic whitespace requires the larger nominal size; keep 2–3pt icon-to-number spacing and 5–6pt between groups. Counts always remain visible, including zero; use `…` while task state loads and `—` when desktop IPC is unavailable. Keep the label monochrome except for a 5pt solid `{colors.success}` dot over the Codex icon's lower-right corner while Feishu Bridge reports active non-dry-run outbound readiness; hide it for every other bridge state and include `飞书桥已连接` in the accessibility sentence. Fall back to `terminal.fill` only when the bundled resource cannot load. Runtime acceptance requires the local Token and both trailing task groups to contain visible pixels and the installed status item's measured width to match the composed image rather than a quota-only width. Opening the popover must preserve the last confirmed usage and task snapshots while refresh is in flight, and identical presentations must not rewrite the image or status-item length; clicking the item therefore causes neither a transient `0` count nor neighboring menu-bar reflow.
- **Popover header:** Label the home surface `Codex 用量`, keep it left-aligned, and place only the icon-only settings button at the top right. Opening the popover remains the refresh action; do not duplicate it with a manual refresh control.
- **Project section header:** Separate the lower project region with one native divider and a compact `KSF 项目` heading. Put the project-sync indicator, current workset count, and the icon-only complete-project-list control on this row.
- **Project workset:** Use compact vertical project containers rather than a horizontal switcher or a representative-task summary. Include pinned projects and unpinned projects with running or waiting tasks. A completed task remains in its owning project Cell with an explicit `已完成` state while that Cell is visible, allowing the user to reopen and continue it. Completion does not remove the task from project membership, but the final active task completing does remove an unpinned project from the home workset. Keep pinned project rows before unpinned active projects and preserve first-seen relative order. The title row shows plain icon-and-count metadata without a capsule; zero recedes to secondary gray and a nonzero running or waiting count may use its state color. Every task remains a three-line row with 4–6pt internal rhythm: status/name, category/main job, then abilities and Skill count. The status badge is the row's only persistent color block. Category, job, ability, and Skill count use neutral secondary text with SF Symbols; pin, task-detail, idle Feishu, Footer metric, and project-action controls are neutral. A connected Feishu shortcut may use its real state color. The complete task body opens the exact Codex task, while neutral `info.circle` and paper-plane controls remain independent. Use quiet partial-width dividers between tasks and a separated Footer whose Token values outrank their labels. Do not show disclosure chevrons or inline route expansion. The detail page restores hierarchy selectively: state text and the small category/job/ability/Skill symbols carry semantic color while route values stay neutral. Validation and Skill stage appear as compact colored text, not additional filled badges. Separate actions with one divider instead of a third card. Render `打开 Codex` and the dynamic `连接飞书 / 解除飞书` action as equal-width, equal-height buttons in one row; use a compact filled primary style only for Codex and a native bordered secondary style for Feishu. Show the Feishu connection state once as quiet secondary text below the row. Missing route and missing refreshed task data use factual empty states. Default to the first four containers, never truncate tasks inside an included container, and keep the workset free of internal scrolling.
- **Project catalog:** The restored `square.grid.2x2` control opens the complete active KSF catalog plus unavailable pinned placeholders as one continuous list. It never includes the runtime-only `无项目` container. Pinned rows remain at the top; all other rows retain their first-seen order without recency-driven movement. Render every row at once in one natural-height stack: do not add a `ScrollView`, lazy stack, fixed-height frame, estimated viewport, scroller gutter, or pagination. The Popover height follows the current catalog size. Each available row uses two lines: project icon and name first, followed at the trailing edge by a neutral `archivebox` task control and the independent pin control in that order; then the exact shared home-project Footer with `累计 / 今日` Token values and controls for new task, KSF folder, engineering root, direct start, and project memory. The archive control exists only here, never on home, and shows a fixed-size progress indicator while it creates and opens the exact project's archive task. Do not show `已固定`, `项目库`, aggregate task-state copy, task rows, or task routes. Unavailable pinned placeholders keep a factual unavailable label because they have no project object or actions.
- **Main quota card:** A compact horizontal metric row, linear progress track, window rows, reset time, plan, and freshness.
- **Token metrics:** Put an icon-only `chart.bar.xaxis` history control at the far right of the `Token 活动` heading. Six equal cells share one three-column, two-row grouped surface. The first row uses the model's perspective for this Mac's current date: `模型普通输入`, `模型缓存输入`, and `模型输出`. The second row shows the account's latest published dated bucket, `本机昨日`, and `本机今日`. Model input covers user text, instructions, prior context, and tool results sent to the model; model output covers model-generated responses, reasoning, and tool calls. Do not show lifetime or peak-daily summaries. Label the account metric `账号最新 · 今日` or `账号最新 · 昨日` when applicable so the delay needs no mental date conversion; use `账号最新 · M月d日` for older buckets and `未同步` only when no daily bucket exists. Derive all local metrics only from cumulative local Codex `token_count` fields and natural local-day boundaries. Ordinary input is the positive input delta minus its cached-input delta, cached input is the cached-input delta, and output is the output delta. If any contributing counter lacks a complete split, preserve local day totals but show the three split values as unavailable. While a popover-open refresh is in flight, place a native mini progress indicator immediately after the `本机今日` label without changing the metric grid or value. Do not add nested metric cards or a chart to the home Token block.
- **API estimate:** Add one quiet footer to the Token group with `API 估算` immediately followed by the selected model and its tiny filled downward triangle on the same baseline, and `今日 $金额` right-aligned at trailing. The model name and trailing triangle form one inline-text label in a borderless native selection menu, without a filled field or prominent arrow button. On the daily page, place this menu immediately after the 30-day estimate label and right-align the amount at the far edge; omit the standalone selector above the legend. Retain the selected-day estimate and one cost line under each Token component. Use USD with adaptive decimal precision, show `—` when composition is unavailable, and state once that current selected prices revalue history and do not represent an invoice. Pricing management is a separate settings page without a top current-plan form: immutable built-ins first, device-local custom plans second, one editor at a time, and official source links last. This secondary catalog alone may scroll to keep all plans inspectable within the display.
- **Daily Token comparison:** The Token-history control opens a compact secondary page titled `每日 Token`. Use one grouped analysis surface: a three-value local summary row for `本机 30 日`, `本机日均`, and `本机活跃`; a 30-column factual comparison trend; a selected-day row for `服务器当日`, `本机当日`, and `本机占比`; and the selected local day's ordinary-input, cached-input, and output row. The server-published account total is the full-width purple base bar and local usage is a narrow `{colors.primary}` overlay, with a concise direct legend above the surface. Scale both series linearly against their shared visible maximum and keep the quiet dashed local-average reference—never smooth, cap, clamp, or logarithmically distort values or percentages. Select the latest date by default. Clicking or horizontally dragging across the plot changes selection and all selected-day values; VoiceOver exposes the plot as one adjustable control. A server date absent from the account response displays `未同步` and has no server bar; it is not a zero. Keep explicit 2pt local zero-day marks, three sparse date anchors, rounded monospaced values, no scrolling, no decorative gradient, and no tooltip bubble or long-range dashboard. State once that server data may lag and the ratio is a direct same-date comparison. Retain prior data while an on-demand refresh runs and return directly to home.
- **Error state:** One concise explanation, one recovery action, and the last successful timestamp when available.
- **Settings page:** A back control returns to the main page. Show KSF as optional with select-and-validate or replace-directory action, compact rows for login launch and general-quota reset notifications, then a full-width `飞书` navigation row with factual setup and bridge state, followed by a separated red quit action containing only the power icon and `退出`. Closing the popover discards secondary-page navigation but never discards a resumable setup stage. Do not expose the external usage-dashboard action.
- **Feishu setup:** Preserve native controls and keep registration separate from user OAuth. Core supplies authoritative state and action affordances; a running process never implies authorization. The following layout governs both platforms.

Feishu configuration uses two surfaces: one connection card containing the three Core-supplied rows and the current authentication region, followed by collapsed diagnostics. The header has a fixed-size right refresh control; logged-in users have centered red plain-text logout at the page bottom. The menu-bar connection dot requires a logged-in session and a ready task connection; logout hides the dot and removes the connected tooltip phrase. Logout first closes all task connections, attempts terminal card updates for at most five seconds, then revokes authorization even when card updates fail. Failed cards remain in diagnostics without automatic replay; Codex tasks continue running. The connection card centers the login button, QR, verification code, neutral waiting text, and secondary browser/cancel links. User OAuth completion is automatic; no standing manual finish button. Keep application registration distinct. Keep the QR area stable while verifying. Cancellation says `取消本次登录，不撤销已有授权`.

Background polling is silent: retain content, expansion, scroll and focus; do not insert refresh rows, remove QR codes, or disable all actions merely because a read is in flight. Manual refresh uses only the header spinner. Commit coherent observations; retain the last successful display during ordinary read failures while preserving authoritative preflight for mutations. Explicit logout and identity/context changes invalidate old identity immediately. Show one relevant failure inside the connection card; waiting is neutral, and successful login/logout is expressed by the resulting state, never a generic global success banner. Test results belong directly below the centered self-test button.

Diagnostics keep App ID, scope limitations, unresolved named operations, and actual component failures. A centered muted line outside the diagnostics card, directly above logout, always shows actual Feishu service and lark-cli versions (unknown when unavailable); do not repeat versions or healthy connection, identity, permission and toolchain summaries below. Only corresponding failures expose repair/reconnect actions. Self-test is last, for the verified self binding, with native confirmation and single-request receipt semantics. Preserve keyboard access and scroll fallback on short screens. No capability toggles, product dry-run, target selector, or standing reauthorization. Refresh and confirmation never navigate away.
- **Task Feishu link:** Each real task row may expose one compact paper-plane control after the task-detail control. The task-detail page repeats the same connect/release action and adds one quiet status stack: authorized alias, current turn state/controller, remaining lease/last sync, and a bounded phase summary. Never display thread, turn, message, or real Feishu identifiers. Keep the home row compact; progress cards, remote controls, attachments and input remain bridge-owned. In progress cards, put the distinct task name in strong text on the first body row with a compact red-outlined `断开` lifecycle action at the right; long names keep the weighted column and wrap naturally while the action stays top-aligned. The header places one green `默认` or `计划` mode tag immediately after the semantic status tag. Mode and permission never appear in the muted metadata flow; that flow contains only controller, concrete phase, elapsed time, and lease 4px below the identity row, and omits generic `运行中` and `当前进展` phases. If the task name duplicates the project title, do not repeat it and share the first row between metadata and `断开`. Running and waiting states use one operational input row in execution order: compact red-outlined danger `停止`, adaptive input, blue `发送`; stopping is an independent callback and never submits the input. The running row has no redundant field label and uses the concise placeholder `补充或修正`; waiting-for-answer keeps its contextual label. Queueing and Desktop-action states have no input and place the same left-aligned `停止` directly below the body. Terminal states left-group a compact `开始新一轮` heading immediately followed by its default/Plan selector inside the same form and do not show Stop. Mode selection is one-turn intent: once a turn starts, its next-turn selector returns to default, including after Plan completion, failure, interruption, and restart recovery. The current user instruction is a stable content layer labeled `你`: restore it from the authoritative turn after a bridge restart, and when the card exceeds its request budget remove an older plan before removing this current-turn context. A completed Plan awaiting implementation is a distinct orange `等待开始执行` state: preserve the complete authoritative plan body within the card request budget, place one content-width blue `开始执行` button centered immediately after it, then keep the always-visible `修改计划` input and secondary `提交修改` button on one row. Plans beyond Feishu's hard single-card budget continue as complete, idempotent text parts in the same message thread instead of being silently clipped. Never render a separate action row below an input, and never give one card two primary actions. Failed starts keep the same plan and primary action with one concise retry explanation.
- **Feishu message tasks:** Treat each authorized, unquoted root message as a new Codex task rather than a global chat. When KSF is ready, start it at the app-validated KSF root but leave it in the runtime `无项目` group until the authoritative projection identifies a project; never infer or manufacture project membership. As soon as that authoritative binding exists, replace the ordinary conversation card in place with the owning project's task card, preserve the latest `你` and `Codex` content, and route every later reply through Desktop IPC so Feishu and the visible Codex task share one live turn history. On bridge startup, reconcile active ordinary conversations against the same authoritative projection once so completed cards recover without another user message. Card and quoted replies keep the stored task and working directory. Name new tasks `飞书 · <首条消息摘要>` with a compact Unicode-safe summary. Completed conversation cards show `总耗时` and `Codex` separately so queue, transport, and delivery overhead remain visible without being mistaken for model execution time.

- **Conversation disconnect:** Completed and failed ordinary conversation cards use the same compact red-outlined `断开` button as project task cards, at the right of the first metadata row. Do not hide this sole control in an overflow menu. The action closes only the Feishu conversation connection, not the Codex task.

## User operation approval (macOS)

Use a native 440pt content-width panel whose height follows its content up to the display work area. Lead with the reviewed action and exact target, then one compact identity/application line and `仅本次`. Show the complete frozen operation content in a selectable plain-text preview; long content scrolls and can expand. Never fabricate resource names, before-values, diffs or a model-written summary. Permission changes retain their complete scope in the preview. Keep unverified source visible. List existing attachments by name and formatted size; omit empty attachment rows. `查看详情` expands the original complete request, parameters, hashes and expiry inline without issuing a decision. Keep actions outside scrolling content: `取消` and the Core-supplied action verb; destructive operations use red. Return defaults to cancellation, Escape/window close cancels, and changing focus or disclosure never approves. Preserve single-use, heartbeat, expiry and identity checks. The Windows native approval flow retains its existing presentation and accepts the optional preview contract.

## Do's and Don'ts

Feishu layout verification: run `bash scripts/render-feishu-layout.sh` on macOS. It renders light/dark fixtures for setup, activation, ready, unknown, and expanded controls into `.build/feishu-layout-preview/images/`, using a temporary home and a preview-only compiler flag. It starts no Core or Feishu process, performs no authorization, and checks overflow controls remain scroll-reachable. The rendered fixtures do not constitute live Feishu or Windows acceptance.

- Do show remaining percentage and reset time together.
- Do keep the global task count in the menu label; project Cells may list real task identity and route, but never conversation content or generated summaries.
- Do keep running and waiting mutually exclusive and pair every count with both an icon and an accessibility phrase.
- Do make each task body open its exact Codex task and use a separate `info.circle` for task details; never use disclosure arrows, infer a task summary, or silently open a different task.
- Do make new-task creation explicit and project-scoped: name it `项目名 · 新任务`, open the exact task first, and keep the fixed-size progress indicator until that visible Codex window accepts the initial turn; do not wait for completion.
- Do keep task identity, original names, status, and first-seen row order in memory only.
- Do show the account's latest published daily bucket with its source date instead of treating a missing current-date bucket as zero.
- Do keep stale values visible with an explicit stale marker.
- Do show the full Chinese interface and let home, Token history, and the complete project catalog grow to their natural content height without internal scrolling.
- Do keep the Codex mark monochrome, unmodified, and subordinate to the percentage; do not imply OpenAI endorsement.
- Do keep model-specific quota buckets out of the popover; they do not belong to the primary capacity check.
- Do keep Feishu Bridge configuration and recovery inside settings; the menu-bar surface exposes only the ready-state green dot and its accessibility phrase.
- Don't bypass the Feishu Bridge client or present dry-run as a successful connection.
- Don't imply token activity is a quota balance.
- Don't animate continuously or use color as the only status signal.

注销最终策略：本地任务先全部断开，卡片更新最多等待 5 秒；卡片失败也继续注销。失败保留诊断并暂停自动重试，不阻挡退出，不停止 Codex 任务。

The Feishu page places a compact “Codex 飞书技能” surface between connection status and diagnostics. It shows Core-owned installation classification and one centered contextual install/update/repair action. Local modifications and conflicts show file details without an overwrite action. This installation is available independently of login.
