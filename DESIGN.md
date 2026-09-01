---
version: 0.4
name: Native Project Instrument
description: A dense macOS project instrument that makes KSF project action, quota, and live task attention legible without becoming a dashboard.
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

# Codex Usage Bar visual system

## Overview

The app should feel like a macOS system instrument: compact, quiet, and precise. Its audience is a KSF team member switching among projects while monitoring capacity and whether Codex needs attention. The reference is Activity Monitor's factual density combined with the restraint of native Control Center panels. Exclude marketing surfaces, custom brand illustration, decorative gradients, and dashboard-like chrome.

## Colors

Use native semantic backgrounds and labels wherever SwiftUI provides them. `{colors.primary}` marks interactive or current state, `{colors.success}` healthy capacity, `{colors.warning}` low capacity, and `{colors.error}` exhausted capacity or blocking failures. Color is always paired with text or an SF Symbol.

## Typography

Use the system font in implementation; the token families describe the macOS roles. The remaining percentage uses `{typography.metric}` with monospaced digits. Section labels use `{typography.headline}`, values use `{typography.body}`, and timestamps use `{typography.caption}`. Never present token activity with greater emphasis than remaining capacity.

## Layout

The popover is fixed at `{components.popover.width}` with `{spacing.md}` outer padding and content-driven height. The home page uses one compact vertical flow: general quota, Token activity, then a vertical KSF project workset containing pinned projects, projects bound to running or waiting tasks, and a transient `无项目` Cell when active top-level tasks have no resolvable project. Put that exception Cell first so it remains in the initial viewport; pinned rows then precede unpinned current-task rows, and first-seen order remains stable inside those normal groups. Each container lists its real task rows, not a summary of one representative task. The first four containers are included by default with no internal workset height cap or scroll view; `更多` includes the remaining workset containers and lets the Popover continue growing naturally. The local Token-history page and complete project catalog likewise use natural content height without internal scrolling. Settings does not scroll. Use `{spacing.xs}` inside dense groups, `{spacing.sm}` between related groups, and `{spacing.md}` only between major sections. Do not use tabs or a sidebar.

## Elevation & Depth

Rely on native popover material, grouped tonal surfaces, and dividers. Do not add custom drop shadows inside the panel.

## Shapes

Cards use `{rounded.md}`. Progress tracks and status chips use `{rounded.full}`. Buttons retain native macOS shapes and hit areas.

## Components

- **First-run setup:** Keep setup inside the same 336pt popover. Use one concise introduction, a grouped KSF-path and explicit-consent settings surface, one blocking validation result, and one primary completion action. Suggest `~/Documents/KSF` only when it exists; never silently accept it. Keep WeChat connection after setup and let quota/task loading remain independent.

- **Menu label:** Use the selected 24×24 Codex mark (OpenAI blossom with `>_`) at 16pt, then render `N%`, this Mac's current-date Token total as `N.NM`, `play.fill N`, and `person.fill.questionmark N` in that order. The Token value always uses one-decimal million units: it never switches to K or B, uses `…M` before the first current-date scan, and uses `—M` only when the local source is unavailable. Compose the complete line into one exact-intrinsic-width 18pt-high macOS template `NSImage` and assign it directly to an AppKit `NSStatusItem` button; SwiftUI remains responsible for the popover content, not status-item layout. Use 12pt rounded monospaced digits, a 9pt `play.fill`, and a 13pt `person.fill.questionmark` whose extra intrinsic whitespace requires the larger nominal size; keep 2–3pt icon-to-number spacing and 5–6pt between groups. Counts always remain visible, including zero; use `…` while task state loads and `—` when desktop IPC is unavailable. Keep the label monochrome except for a 5pt solid `{colors.success}` dot over the Codex icon's lower-right corner while WeChat is confirmed connected; hide it for every other connection state and include `微信已连接` in the accessibility sentence. Fall back to `terminal.fill` only when the bundled resource cannot load. Runtime acceptance requires the local Token and both trailing task groups to contain visible pixels and the installed status item's measured width to match the composed image rather than a quota-only width. Opening the popover must preserve the last confirmed usage and task snapshots while refresh is in flight, and identical presentations must not rewrite the image or status-item length; clicking the item therefore causes neither a transient `0` count nor neighboring menu-bar reflow.
- **Popover header:** Label the home surface `Codex 用量`, keep it left-aligned, and place only the icon-only settings button at the top right. Opening the popover remains the refresh action; do not duplicate it with a manual refresh control.
- **Project section header:** Separate the lower project region with one native divider and a compact `KSF 项目` heading. Put the project-sync indicator, current workset count, and the icon-only complete-project-list control on this row.
- **Project workset:** Use compact vertical project containers rather than a horizontal switcher or a representative-task summary. Include pinned projects and projects bound to running or waiting tasks. When an active top-level task has neither a resolvable KSF projection nor an exact engineering-root match, put it in one runtime-only `无项目` container before all project rows. This exception uses the same neutral grouped surface and task disclosure rows, but has no pin, project Token, memory, folder, launch, engineering, or new-task controls; it disappears when its final task is assigned or ends. Keep pinned project rows before unpinned current-task projects and preserve first-seen relative order across task counts, waiting/running transitions, Token changes, and latest-activity updates. A deliberate pin-state change may move a normal project between groups. The title row always shows the `play.fill` running count, including `0`; show a waiting count only when nonzero, and keep pin state in its adjacent control for real projects. Below it, render every real active task as a three-line disclosure row. Line one carries status symbol, original task name, explicit status, a separate icon-only `在 Codex 中打开任务` control, and the disclosure control. Line two shows `工作类别 · 主岗位`; line three names every projected basic ability and ends with the dispatchable-Skill count. Clicking the task body expands its complete route in place without opening Codex; opening another task closes the previous one, and reopening the popover clears expansion. The expanded route shows category, main and collaborating jobs, abilities grouped under their owning job, and dispatchable Skills grouped under their owning ability with `active` or `trial`; unmatched data remains visible in explicit fallback groups. Use `未命名任务` and `未绑定 KSF 路由` as factual fallbacks. Waiting statuses are `待批准`, `待确认计划`, `待回复`, and `待处理`; color supplements rather than replaces the text. Finish each real-project container with lifetime/today Token values and icon-only controls for new task, KSF folder, engineering root, direct project start, and project-memory access. The start control is a direct `play.fill` action only when the KSF project directory contains a safely executable `start.sh`; otherwise show a disabled `play.slash` with the factual help text `项目目录中没有可执行的 start.sh`. Clicking it opens visible Terminal in the project directory and executes that one script without a menu. The new-task control is the first shortcut after the metrics and uses `plus.bubble`; it is always a direct action because every created task belongs to the Codex Desktop KSF project. Replace the control with a fixed-size mini progress indicator from the click until the visible Codex window accepts the bootstrap turn. Open the explicitly named task before submission so Codex itself presents the prompt and native active-reply state. Keep failures inside the owning container. Default to the first four containers, but never truncate tasks inside an included container. Give the home workset its natural content height: it has no internal `ScrollView`, fixed-height frame, scrollbar gutter, or estimated-height threshold. Task disclosure and `更多` therefore expand the Popover itself. Reopening the Popover restores the first-four-container state.
- **Project catalog:** The restored `square.grid.2x2` control opens the complete active KSF catalog plus unavailable pinned placeholders as one continuous list. It never includes the runtime-only `无项目` container. Pinned rows remain at the top; all other rows retain their first-seen order without recency-driven movement. Render every row at once in one natural-height stack: do not add a `ScrollView`, lazy stack, fixed-height frame, estimated viewport, scroller gutter, or pagination. The Popover height follows the current catalog size. Each available row uses two lines: project icon and name first, followed at the trailing edge by a neutral `archivebox` task control and the independent pin control in that order; then the exact shared home-project Footer with `累计 / 今日` Token values and controls for new task, KSF folder, engineering root, direct start, and project memory. The archive control exists only here, never on home, and shows a fixed-size progress indicator while it creates and opens the exact project's archive task. Do not show `已固定`, `项目库`, aggregate task-state copy, task rows, or task routes. Unavailable pinned placeholders keep a factual unavailable label because they have no project object or actions.
- **Main quota card:** A compact horizontal metric row, linear progress track, window rows, reset time, plan, and freshness.
- **Token metrics:** Put an icon-only `chart.bar.xaxis` history control at the far right of the `Token 活动` heading. Six equal cells share one three-column, two-row grouped surface. The first row uses the model's perspective for this Mac's current date: `模型普通输入`, `模型缓存输入`, and `模型输出`. The second row shows the account's latest published dated bucket, `本机昨日`, and `本机今日`. Model input covers user text, instructions, prior context, and tool results sent to the model; model output covers model-generated responses, reasoning, and tool calls. Do not show lifetime or peak-daily summaries. Label the account metric `账号最新 · 今日` or `账号最新 · 昨日` when applicable so the delay needs no mental date conversion; use `账号最新 · M月d日` for older buckets and `未同步` only when no daily bucket exists. The account bucket may have a broader scope than this Mac, so never derive or display an account/local ratio. Derive all local metrics only from cumulative local Codex `token_count` fields and natural local-day boundaries. Ordinary input is the positive input delta minus its cached-input delta, cached input is the cached-input delta, and output is the output delta. If any contributing counter lacks a complete split, preserve local day totals but show the three split values as unavailable. While a popover-open refresh is in flight, place a native mini progress indicator immediately after the `本机今日` label without changing the metric grid or value. Do not add nested metric cards or a chart to the home Token block.
- **Local Token history:** The Token-history control opens a compact secondary page titled `本机每日 Token`. Use one grouped analysis surface: a three-value summary row for `30 日总量`, `日均`, and `活跃天`; a 30-column factual bar trend; and the selected day's ordinary-input, cached-input, and output row. Preserve exact linear scaling against the visible maximum and draw one quiet dashed average reference—never smooth, cap, or logarithmically distort the data. Select the latest date by default. Clicking or horizontally dragging across the plot changes selection, keeps the exact date and total immediately above the bars, and updates the composition below; VoiceOver exposes the plot as one adjustable control. Use `{colors.primary}` only for the selected bar and a low-opacity tint for context bars. Keep explicit 2pt zero-day marks, three sparse date anchors, rounded monospaced values, no scrolling, and no decorative gradient, legend, tooltip bubble, or long-range dashboard. Retain prior data while an on-demand refresh runs and return directly to home.
- **Error state:** One concise explanation, one recovery action, and the last successful timestamp when available.
- **Settings page:** A back control returns to the main page. Show KSF root status and a reselect control, compact rows for login launch and general-quota reset notifications, then a full-width `微信连接` navigation row with factual connection state, followed by a separated red quit action containing only the power icon and `退出`. Closing the popover discards secondary-page state, so every later status-item opening starts on the main page. Do not expose the external usage-dashboard action.
- **WeChat connection:** Use a compact secondary page, not a modal or dashboard. Lead with one native status row, center the scannable QR code only while authorization is active, place test-send and disconnect actions together, and finish with a plain-language trust-boundary note. Never show account identifiers, tokens, command contents, or message history. Pair every state color with an SF Symbol and explicit text.

## Do's and Don'ts

- Do show remaining percentage and reset time together.
- Do keep the global task count in the menu label; project Cells may list real task identity and route, but never conversation content or generated summaries.
- Do keep running and waiting mutually exclusive and pair every count with both an icon and an accessibility phrase.
- Do make each task body a disclosure target for its exact route and provide a separate icon-only control for opening its exact Codex task; never infer a task summary or silently open a different task.
- Do make new-task creation explicit and project-scoped: name it `项目名 · 新任务`, open the exact task first, and keep the fixed-size progress indicator until that visible Codex window accepts the initial turn; do not wait for completion.
- Do keep task identity, original names, status, and first-seen row order in memory only.
- Do show the account's latest published daily bucket with its source date instead of treating a missing current-date bucket as zero.
- Do keep stale values visible with an explicit stale marker.
- Do show the full Chinese interface and let home, Token history, and the complete project catalog grow to their natural content height without internal scrolling.
- Do keep the Codex mark monochrome, unmodified, and subordinate to the percentage; do not imply OpenAI endorsement.
- Do keep model-specific quota buckets out of the popover; they do not belong to the primary capacity check.
- Do keep WeChat recovery inside settings; the menu-bar surface exposes only the connected-state green dot and its accessibility phrase.
- Don't present queued commands as executed work or expose their contents in the interface.
- Don't imply token activity is a quota balance.
- Don't animate continuously or use color as the only status signal.
