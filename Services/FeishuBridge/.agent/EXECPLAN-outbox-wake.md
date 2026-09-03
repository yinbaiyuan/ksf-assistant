# ExecPlan: 出站 Outbox 与本地 Wake

## 目标

维护飞书桥的通用出站消息能力：本机其他项目先 append `logs/outbox.jsonl`，再调用本地 wake 接口触发飞书桥即时消费。2026-09-01 经用户明确确认，主动出站取消本地目标白名单，改为“任意显式目标 + 逐次用户授权”；飞书桥继续负责结构校验、去重、发送、日志和审计。

## 当前状态

- 主程序 `bot-bridge.js` 已具备入站事件、Codex 调用、outbox、docbox、wake、日志和审计。
- 统一客户端 `scripts/bridge-client.js` 支持目标别名和显式 `chat_id:` / `open_id:`。
- 入站权限继续由独立白名单控制；出站放开不改变谁可以操控机器人。
- 日志目录 `logs/` 已忽略，不纳入版本管理。

## 边界

飞书桥负责：

- 读取 outbox。
- 校验请求结构和显式目标。
- dry-run、去重、分段发送。
- 写 `outbox-results.jsonl`、`messages.jsonl` 和项目内审计页。
- 原样记录 `trace`。
- 入站 `[TRACE:<code>]` 识别和通用 key-value 解析。

飞书桥不负责：

- 识别 HAPOS 或任何上层业务系统。
- 维护任务、审查或业务对象状态。
- 将 key-value 内容解释为业务结论。
- 通过 wake 接口接收业务消息体。
- 按姓名搜索、猜测或自动更换目标。
- 突破机器人可用范围、群成员关系或飞书平台权限。

## 改动方案

- 删除 `FEISHU_OUTBOUND_ALLOWED_CHAT_IDS` / `FEISHU_OUTBOUND_ALLOWED_OPEN_IDS` 和客户端、worker 两层目标白名单判断。
- `FEISHU_OUTBOUND_ENABLED=true` 时接受任意结构合法、显式提供的 `chat_id` / `open_id`。
- 目标别名由用户通过本机安全配置显式建立，不再从环境白名单推导“我”。
- `status` / `cmd 状态` 显示 `any_explicit_id` 目标策略。
- 保持 outbox、wake、polling、dry-run、分段、去重、脱敏和审计链路不变。
- 同步 `.env.example`、统一客户端文档、技术规格、实现指南和 KSF trial Skill。

## 安全策略

- `FEISHU_OUTBOUND_ENABLED` 非 `true` 时不处理 outbox。
- 每次真实发送都必须由用户本轮明确提供动作、目标和正文；没有目标或正文不入队。
- 出站不设本地目标白名单，但不提供姓名搜索和目标猜测。
- `FEISHU_OUTBOUND_DRY_RUN=true` 时只写日志和审计，不实际发送。
- wake 接口只唤醒，不接收业务消息体，不表示发送成功。
- 文档和代码不写真实 token、secret、chat_id 或 open_id。
- 入站白名单继续独立生效；真实目标 ID 只进入本机安全配置、stdin 或 outbox。

## 验证步骤

1. `node --check bot-bridge.js`。
2. dry-run 写入一个未建立别名的假目标并调用隔离 worker。
3. 检查 `outbox-results.jsonl`、`messages.jsonl` 和项目内审计页。
4. 验证任意显式目标不再被本地白名单拒绝。
5. 验证重复 `id` 为 `duplicate`。
6. 验证坏 JSON 完整行 `invalid`、末尾半行下轮重试。
7. 验证 `cmd 状态`、`cmd 最近出站`、`cmd 出站 <OUT-id>`。
8. 真实发送只对用户另行明确给出的目标和正文执行。

## 已完成验证

- 原始 MVP 验证记录中的白名单 `dry_run` / `denied` 是 2026-06-01 的历史事实，不再代表当前策略。
- 2026-09-01 已完成客户端、worker、规则、配置样例、文档和 KSF trial Skill 更新。
- `node --check bot-bridge.js` 通过。
- Node 测试覆盖：任意显式 `chat_id` / `open_id` 可解析，代码中不再存在两项出站白名单环境变量或 `target_not_allowed` 分支，目标与正文仍脱敏。
- 已使用临时日志目录、临时审计目录、假 `lark-cli` 和 `FEISHU_OUTBOUND_DRY_RUN=true` 验证：
  - 未建立别名的假目标生成 `dry_run` 结果，不实际发送。
  - dry-run 期间没有调用飞书消息发送命令。
- 既有回归继续覆盖重复 ID、wake/polling fallback、完整坏 JSON、末尾半行与全部终态查询。
- 本轮不执行真实飞书发送；真实发送需要用户另行提供测试目标和正文。

## 回滚方式

- 设置 `FEISHU_OUTBOUND_ENABLED=false` 停止 outbox 消费。
- 设置 `FEISHU_OUTBOUND_WAKE_ENABLED=false` 停止 wake server。
- 恢复客户端和 worker 的目标白名单判断及两项出站白名单环境变量，然后重启 LaunchAgent。
