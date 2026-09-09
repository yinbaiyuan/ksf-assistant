# Feishu card interaction audit

## Flow and failure boundaries

The desktop task control calls Core `feishu/taskLink/create`. Core reserves a
stable link, resolves the configured direct recipient, and sends a card through
the bridge's governed message transport. Only the acknowledged root message ID
makes the public link active. Retrying a failed delivery reuses the reservation.

The bridge owns the official CLI message and card consumers. Core durably accepts
inbound events before transport ACK; event IDs and payload digests deduplicate
retries. Card actions bind the operator, message, task, link and relevant question
or plan revision before reaching Codex. Replies resolve the original message
binding and resume the stored task. Releasing/expiring a card disables controls.

| Boundary | Review result and validation |
| --- | --- |
| Listener lifetime | Owned consumer failure previously ended listening permanently. Bounded retries now join/clean only owned processes before reconnecting. A failed status probe is tolerated; foreign buses and event loss remain terminal. |
| Delivery acknowledgment | Failed send previously left an apparently active link. Public `pending` state now keeps explicit retry available, with unchanged idempotency identity. |
| Health and readiness | Old uncertain outcomes/card failures remain degraded warnings; they no longer disable unrelated new connections. Storage and worker failures still block readiness. |
| Desktop feedback | Task controls report why the service or recipient is unavailable when clicked, instead of silently doing nothing. Both hosts consume Core readiness. |
| Write policy and confirmation | Write-off and dry-run now block card readiness explicitly. The native task control retains structured authorization data, reviews the recipient/task, confirms only the frozen operation and reuses the delivery identity. Cancellation ends the unsent reservation. Lost reviews can receive a fresh challenge; expired/cancelled operations are re-prepared only with zero attempts, preserving prior audit records. |
| Diagnosis | Doctor now checks both message/card listeners and the running process; process existence alone is insufficient. |
| Callback and reply routing | Existing tests cover operator/message/revision validation, inactive cards, typed question IDs, message bindings and task continuation. |
| Replay and restart | Existing tests cover durable ACK, duplicate delivery, unknown outcomes, card restoration and patch reconciliation. Uncertain business effects are not automatically replayed. |

## Verification limits

Fake-port/process regressions validate the failure and recovery branches without
sending Feishu messages or creating real Codex turns. Production diagnosis found
stopped listeners, historical uncertain outcomes and failed old-card restoration.
The previously deployed build did not retain the original listener exit cause;
it cannot be reconstructed from its generic degraded snapshot. New consumer
failures record bounded diagnostic codes, without message contents or credentials.

A production send/callback/reply round trip still requires an authorized real
interaction. Unit tests and process health do not substitute for that acceptance.

The inspected local policy uses `confirm_each` for initial bot sends and `allowed`
for ordinary writes (card patches/replies), with no per-capability overrides.
`actionbox` was disabled despite enabled outbound delivery. No permission defaults,
feature switches or old unknown outcomes were changed by this repair. Enabling real
writes and production acceptance requires the user's explicit authorization.

## Regression and installation verification

The revised tree passed `go test ./...`, `go vet ./...`, race checks for Feishu,
integration and service, 88 Swift tests and 145 Windows Node tests. An additional
integration test connects the real governed transport to the task runtime: no send
before confirmation, one send after confirmation, and the acknowledged message
attached to the original pending link. All four Core targets and the universal
macOS package built successfully, including the 776 pinned CLI contract checks.

After local installation, the app/Core/bridge artifacts matched the build. Both
managed consumers connected and doctor included their health. The composed snapshot
reported ready transport with `taskCardWriteDisabled` as the new-task blocker;
historical reconciliation warnings remained visible. This validates startup and
configuration reporting, not production card actions or a real Codex continuation.

## Canonical task history ordering

A later live report showed one task card cycling through unrelated older answers
on successive polls. Desktop IPC decodes numeric values with `UseNumber`, but the
card normalizer did not recognize `json.Number` timestamps. All eight observed
turn timestamps therefore compared as zero; stable sorting retained Go map's
unspecified iteration order, changing the selected latest turn on each read.

Canonical history now follows `turnHistory.history.islands[].entries[].value`
into `entitiesByKey`, matching the installed Desktop history contract. The compact
`turns` cache and unindexed entities do not override canonical order. Direct arrays
retain their explicit order. Legacy unordered history uses recognized positive
timestamps (including `json.Number`), with deterministic ties; untimed entities
cannot establish a latest turn. A missing indexed entity yields no projection,
so it cannot replace the current card with an older partial result.

Regressions cover canonical order without timestamps, unindexed stale records,
incomplete indexes, direct-array order and the actual Desktop numeric decoder type.

The pre-install live probe selected an older completed turn while the current
Desktop turn was still running. After installing the corrected Core, verification
compares the persisted card turn with the canonical latest turn and checks that
`cardSyncPending` clears after delivery. No permissions or message targets are
changed by the ordering fix.

## 当前轮用户消息与回复配对（2026-09-07）

Desktop 观察链路此前只投影助手回复，卡片用户区仍读取飞书入站时保存的 `latestInput`，导致桌面新轮次显示旧提问。现在由同一次当前轮投影更新用户文字、轮次标识和助手回复；不跨轮回填用户文字。观察版本兼容标记使已有卡片在升级后重新投影。

用户文字从当前轮最后一个 `userMessage` 读取。附件传输包装移除后显示真实请求，纯附件显示“已发送附件”；Desktop 执行计划生成的完整指令显示为“执行此计划”。飞书执行计划入口也使用该显示文本。原始任务指令、执行语义和权限不变。

回归覆盖当前轮配对、旧字段迁移、缺失用户消息、追加输入、附件和执行计划显示。此变更位于 Go Core 投影层，平台宿主无需另存用户消息。

## 当前轮进展队列与快照防倒退（2026-09-09）

运行中卡片此前只读取当前轮最后一个公开 `commentary`，因此后续进展会覆盖较早内容。现在 Core 按当前 turn 保存公开进展段，并用完整快照重叠合并和稳定 item ID 去重；新 turn 重置队列，终态仍由最终回答替换过程内容。reasoning、命令和工具输出不进入卡片。

卡片请求继续遵守 30 KiB 上限并预留 512 bytes，按实际 UTF-8 嵌套请求体计数。超限时先按 FIFO 移除最早完整段；最新单段仍超限时保留其最大可容纳尾部并显示省略提示。被淘汰段不会因后续完整快照重新进入队列。

停止后立即开始新轮次会扩大 Desktop 快照切换窗口，但不是失败根因。观察缓存现在拒绝同一 owner 的倒序数字 revision；任务投影同时以已确认 turn 为锚，只接受同一 turn 或可由有序历史、较新时间戳证明的新 turn。不完整旧快照不能再把当前运行或已停止轮次改回较早失败轮次；真实停止仍保持 `interrupted`。

回归覆盖分段累加、重复与部分快照、FIFO、多字节及 JSON 转义预算、超长最新段、倒序 revision、旧失败快照和停止后新运行轮次。自动化不替代真实飞书客户端中的运行中卡片验收。

## macOS 配置确认窗口与导航生命周期

根视图此前监听应用内所有 `NSWindow.didBecomeKeyNotification` 并返回首页。授权和注销的确认窗口也会产生该事件，因而在提交前打断配置页。导航重置现仅跟随受管菜单栏面板真实打开的序号；确认窗口及返回焦点不触发重置。真正关闭后重开仍回到首页，旧确认意图同时清除。

离线 AppKit/SwiftUI 回归通过真实根视图注入其他窗口及原窗口焦点通知，对比配置页渲染：修改前前一个事件即失败，修改后均保持原页面。测试不运行 Core，不发起 OAuth 或注销。现有配置提交仍核验捕获的应用上下文、可交互状态和明确确认。
