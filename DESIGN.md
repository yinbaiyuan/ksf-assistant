---
version: 0.3
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

The app should feel like a macOS system instrument: compact, quiet, and precise. Its audience is one person switching among KSF projects while monitoring capacity and whether Codex needs attention. The reference is Activity Monitor's factual density combined with the restraint of native Control Center panels. Exclude marketing surfaces, custom brand illustration, decorative gradients, and dashboard-like chrome.

## Colors

Use native semantic backgrounds and labels wherever SwiftUI provides them. `{colors.primary}` marks interactive or current state, `{colors.success}` healthy capacity, `{colors.warning}` low capacity, and `{colors.error}` exhausted capacity or blocking failures. Color is always paired with text or an SF Symbol.

## Typography

Use the system font in implementation; the token families describe the macOS roles. The remaining percentage uses `{typography.metric}` with monospaced digits. Section labels use `{typography.headline}`, values use `{typography.body}`, and timestamps use `{typography.caption}`. Never present token activity with greater emphasis than remaining capacity.

## Layout

The popover is fixed at `{components.popover.width}` with `{spacing.md}` outer padding and content-driven height. The home page uses one compact vertical flow: general quota, Token activity, then a vertical KSF project workset containing only pinned projects and projects bound to running or waiting tasks. Pinned rows precede unpinned current-task rows; within those groups, first-seen order remains stable and live activity does not move rows. Each project is a container for its real task rows, not a summary of one representative task. The first four projects are included by default; if their full task content exceeds 360pt, only the project region scrolls. `更多` includes the remaining workset projects in that same bounded region while quota and Token stay fixed. The complete project catalog uses one bounded continuous scroll region; project detail and settings do not scroll. Use `{spacing.xs}` inside dense groups, `{spacing.sm}` between related groups, and `{spacing.md}` only between major sections. Do not use tabs or a sidebar.

## Elevation & Depth

Rely on native popover material, grouped tonal surfaces, and dividers. Do not add custom drop shadows inside the panel.

## Shapes

Cards use `{rounded.md}`. Progress tracks and status chips use `{rounded.full}`. Buttons retain native macOS shapes and hit areas.

## Components

- **Menu label:** Use the selected 24×24 Codex mark (OpenAI blossom with `>_`) at 16pt, then render `N%`, this Mac's current-date Token total as `N.NM`, `play.fill N`, and `person.fill.questionmark N` in that order. The Token value always uses one-decimal million units: it never switches to K or B, uses `…M` before the first current-date scan, and uses `—M` only when the local source is unavailable. Compose the complete line into one exact-intrinsic-width 18pt-high macOS template `NSImage` and assign it directly to an AppKit `NSStatusItem` button; SwiftUI remains responsible for the popover content, not status-item layout. Use 12pt rounded monospaced digits, a 9pt `play.fill`, and a 13pt `person.fill.questionmark` whose extra intrinsic whitespace requires the larger nominal size; keep 2–3pt icon-to-number spacing and 5–6pt between groups. Counts always remain visible, including zero; use `…` while task state loads and `—` when desktop IPC is unavailable. Keep the label monochrome except for a 5pt solid `{colors.success}` dot over the Codex icon's lower-right corner while WeChat is confirmed connected; hide it for every other connection state and include `微信已连接` in the accessibility sentence. Fall back to `terminal.fill` only when the bundled resource cannot load. Runtime acceptance requires the local Token and both trailing task groups to contain visible pixels and the installed status item's measured width to match the composed image rather than a quota-only width. Opening the popover must preserve the last confirmed usage and task snapshots while refresh is in flight, and identical presentations must not rewrite the image or status-item length; clicking the item therefore causes neither a transient `0` count nor neighboring menu-bar reflow.
- **Popover header:** Label the home surface `Codex 用量`, keep it left-aligned, and place only the icon-only settings button at the top right. Opening the popover remains the refresh action; do not duplicate it with a manual refresh control.
- **Project section header:** Separate the lower project region with one native divider and a compact `KSF 项目` heading. Put the project-sync indicator, current workset count, and the icon-only complete-project-list control on this row.
- **Project detail list:** Use compact vertical project containers rather than a horizontal switcher or a representative-task summary. Include only pinned projects and projects bound to running or waiting tasks. Keep pinned projects first and current-task projects below; preserve first-seen relative order across task counts, waiting/running transitions, Token changes, and latest-activity updates. A deliberate pin-state change may move a project between groups. The title row always shows the `play.fill` running count, including `0`; show a waiting count only when nonzero, and keep pin state in its adjacent control. Below it, render every real active task as a two-line full-width button: status symbol, original Codex task name, explicit status and chevron on line one; that task's `工作类别 · 主岗位 · N 基本功 · N Skill` on line two. Use `未命名任务` and `未绑定 KSF 路由` as factual fallbacks. Waiting statuses are `待批准`, `待确认计划`, `待回复`, and `待处理`; color supplements rather than replaces the text. Finish the container with lifetime/today Token values and icon-only controls for new task, KSF folder, engineering root, launch actions, and complete details. The new-task control is the first shortcut after the metrics and uses `plus.bubble`; it is always a direct action because every created task belongs to the Codex Desktop KSF project. Replace the control with a fixed-size mini progress indicator from the click until the visible Codex window accepts the bootstrap turn. Open the explicitly named task before submission so Codex itself presents the prompt and native active-reply state. Keep failures inside that project container. Default to the first four projects, but never truncate tasks inside an included project. Above about 360pt, the project region gains a native vertical scroll indicator. Reserve the same trailing gutter with or without scrolling so content width remains stable. Reopening the popover restores the first-four-project state.
- **Project catalog:** The restored `square.grid.2x2` control opens the complete active KSF catalog plus unavailable pinned placeholders as one continuous list. Pinned rows remain at the top; all other rows retain their first-seen order without recency-driven movement. Short lists use their natural height; long lists stop at a bounded viewport and expose a native vertical scroll indicator. Force overlay scrollers and keep a permanent narrow trailing safe area so the indicator appearing or disappearing never changes row width. Each row exposes only project name, aggregate activity or availability, navigation, and pin state; task names, task routes, Token values, and project actions remain out of this catalog.
- **Project detail:** Expose the complete route names, Token coverage statement, engineering mappings, and all declared actions with bounded text and native menus. It is a drill-down, not a second dashboard.
- **Main quota card:** A compact horizontal metric row, linear progress track, window rows, reset time, plan, and freshness.
- **Token metrics:** Six equal cells share one three-column, two-row grouped surface. The first row uses the model's perspective for this Mac's current date: `模型普通输入`, `模型缓存输入`, and `模型输出`. The second row shows the account's latest published dated bucket, `本机昨日`, and `本机今日`. Model input covers user text, instructions, prior context, and tool results sent to the model; model output covers model-generated responses, reasoning, and tool calls. Do not show lifetime or peak-daily summaries. Label the account metric `账号最新 · 今日` or `账号最新 · 昨日` when applicable so the delay needs no mental date conversion; use `账号最新 · M月d日` for older buckets and `未同步` only when no daily bucket exists. The account bucket may have a broader scope than this Mac, so never derive or display an account/local ratio. Derive all local metrics only from cumulative local Codex `token_count` fields and natural local-day boundaries. Ordinary input is the positive input delta minus its cached-input delta, cached input is the cached-input delta, and output is the output delta. If any contributing counter lacks a complete split, preserve local day totals but show the three split values as unavailable. While a popover-open refresh is in flight, place a native mini progress indicator immediately after the `本机今日` label without changing the metric grid or value. No nested metric cards and no chart.
- **Error state:** One concise explanation, one recovery action, and the last successful timestamp when available.
- **Settings page:** A back control returns to the main page. Show KSF root status and a reselect control, compact rows for login launch and general-quota reset notifications, then a full-width `微信连接` navigation row with factual connection state, followed by a separated red quit action containing only the power icon and `退出`. Closing the popover discards secondary-page state, so every later status-item opening starts on the main page. Do not expose the external usage-dashboard action.
- **WeChat connection:** Use a compact secondary page, not a modal or dashboard. Lead with one native status row, center the scannable QR code only while authorization is active, place test-send and disconnect actions together, and finish with a plain-language trust-boundary note. Never show account identifiers, tokens, command contents, or message history. Pair every state color with an SF Symbol and explicit text.

## Do's and Don'ts

- Do show remaining percentage and reset time together.
- Do keep the global task count in the menu label; project Cells may list real task identity and route, but never conversation content or generated summaries.
- Do keep running and waiting mutually exclusive and pair every count with both an icon and an accessibility phrase.
- Do make each task row one direct target for opening its exact Codex task; never infer a task summary or silently open a different task.
- Do make new-task creation explicit and project-scoped: name it `项目名 · 新任务`, open the exact task first, and keep the fixed-size progress indicator until that visible Codex window accepts the initial turn; do not wait for completion.
- Do keep task identity, original names, status, and first-seen row order in memory only.
- Do show the account's latest published daily bucket with its source date instead of treating a missing current-date bucket as zero.
- Do keep stale values visible with an explicit stale marker.
- Do show the full Chinese interface and keep scrolling confined to an over-height project region or the complete project catalog; quota and Token content never scroll.
- Do keep the Codex mark monochrome, unmodified, and subordinate to the percentage; do not imply OpenAI endorsement.
- Do keep model-specific quota buckets out of the popover; they do not belong to the primary capacity check.
- Do keep WeChat recovery inside settings; the menu-bar surface exposes only the connected-state green dot and its accessibility phrase.
- Don't present queued commands as executed work or expose their contents in the interface.
- Don't imply token activity is a quota balance.
- Don't animate continuously or use color as the only status signal.
