# 飞书桥 lark-cli 优化落地 ExecPlan

## 目标

把飞书桥从“可用的 lark-cli 迁移版”整理成稳定的本地协同编排层：

- outbox/docbox 仍以 JSONL 为事实队列。
- 飞书 API、消息、文档读取、文档更新和官方版本管理统一通过 lark-cli 执行。
- `update_document` 在正式编辑既有飞书文档前，必须先创建飞书官方文档版本；版本创建失败时停止，不写正文。
- 继续保留权限、幂等、审计、wake 和 polling fallback。

## 当前状态

- 主程序：`bot-bridge.js`
- lark-cli 执行层：`lib/lark-cli-runner.js`
- outbox 文件：`logs/outbox.jsonl`
- docbox 文件：`logs/docbox.jsonl`
- 结果文件：`logs/outbox-results.jsonl`、`logs/docbox-results.jsonl`
- 状态文件：`logs/outbox-state.json`、`logs/docbox-state.json`
- 单实例锁：`logs/bridge.pid`
- 文档规范：`docs/feishu-bridge-technical-spec.md`
- 复刻指南：`docs/feishu-bridge-implementation-guide.md`

## 边界

飞书桥负责：

- 消息和文档任务队列消费。
- 权限、白名单、source 校验。
- lark-cli 调用编排。
- JSONL 结果、运行日志和项目内审计。
- `trace` 透传。

飞书桥不负责：

- HAPOS 或其他业务系统状态流转。
- 审查结论采纳。
- 高影响业务语义判断。
- 绕过官方版本保护直接编辑既有飞书文档。

## 改动方案

1. 新增 `lib/lark-cli-runner.js`：
   - `runLarkCliJson()`
   - `larkApi()`
   - `larkImSend()`
   - `larkImReply()`
   - `larkDocFetch()`
   - `larkDocUpdate()`
   - `larkDocCreateVersion()`
   - `larkDocListVersions()`

2. `bot-bridge.js` 改为复用执行层：
   - 飞书消息发送、回复、群信息和成员信息查询走 runner。
   - event consumer 继续使用子进程，保持 stdin 打开。
   - 保留 lark-cli 扁平事件和旧 SDK 结构兼容。
   - 使用 `logs/bridge.pid` 防止多个桥实例并发消费队列或重复接收事件。

3. docbox `update_document` 改为桥内基础执行链路：
   - 解析 docx URL/token 或 wiki URL/token。
   - 读取原文。
   - 调用 raw API `/open-apis/drive/v1/files/:file_token/versions` 创建官方版本。
   - 版本 API 返回字段名可能是 `version`，需标准化为结果中的 `versionId`。
   - 版本创建成功后，通过 `lark-cli docs +update --api-version v2` 更新文档。
   - 更新后再次读取文档，写入 revision、version 和摘要。

4. docbox `create_document` 的历史方案曾暂时保留 Codex 任务执行；该方案已被 1.0.0 收敛：
   - 当前固定使用 `lark-cli docs +create → fetch`，正文只走 stdin，不再启动自由 Codex 文档任务。

## 安全策略

- 所有 lark-cli 调用使用数组参数，禁止 shell 拼接。
- `update_document` 默认 `versionPolicy=official_before_update`。
- 版本创建失败时返回 `failed`，不执行正文更新。
- `FEISHU_DOCBOX_DRY_RUN=true` 时不创建版本、不编辑文档。
- 版本化文件不得写入真实 App Secret、访问 token、open_id、chat_id 或真实飞书文档链接。

## 验证步骤

- `node --check bot-bridge.js`
- `node --check scripts/start-bridge.js`
- `node --check lib/lark-cli-runner.js`
- `git diff --check`
- 单聊 `cmd ping`、`cmd 状态`
- 群聊 `@机器人 cmd ping`、`@机器人 cmd 状态`
- outbox 白名单正式发送、未授权拒绝、重复 ID
- docbox dry-run、正式 `update_document` 版本保护更新、错误 token 不写正文
- 脱敏扫描

## 回滚方式

- 设置 `FEISHU_DOCBOX_ENABLED=false` 关闭 docbox。
- 设置 `FEISHU_DOCBOX_DRY_RUN=true` 禁止真实文档写入。
- 设置 `FEISHU_OUTBOUND_ENABLED=false` 关闭 outbox 主动发送。
- 如 runner 有问题，可回滚 `lib/lark-cli-runner.js` 和 `bot-bridge.js` 对 runner 的引用。
