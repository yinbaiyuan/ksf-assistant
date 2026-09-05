# 飞书桥系统实现与复刻指南

> 迁移期维护者参考：本文用于理解和回退 Node 兼容实现，不是
> KSFAssistant 普通用户的安装或配置入口。

更新日期：2026-09-03
适用项目：`feishu-bot-bridge`
目标读者：需要搭建同类系统的同事、后续接手维护的 AI Agent

本文件解释架构和复刻原理。实际安装统一从 `docs/installation.md` 开始，避免沿用本文件中的历史示例片段作为当前安装命令。

## 1. 系统目标

飞书桥是一个运行在个人电脑上的本地消息桥，用来连接：

- 飞书自建机器人。
- 飞书长连接事件。
- 本机 Codex app-server。
- 本地项目或其他 Agent 的出站消息请求。
- 本地 JSONL 日志与项目内审计页。

它的核心目标是把“飞书协同界面”和“本机 Codex 执行能力”连接起来，同时保留清晰权限边界、审计记录和可追踪消息链路。

飞书桥不是业务系统。它不维护业务对象生命周期，不裁决审批结果，不理解 HAPOS 或其他上层系统的业务语义。

## 2. 能力范围

当前 1.0.0 系统在原有能力上增加四类核心机制：

1. 入站消息处理：接收飞书单聊或群聊消息，做去重、权限判断和路由。
2. Codex 调用：通过 `codex app-server proxy` 或独立 `codex app-server` 创建或恢复 Codex thread。
3. 主动出站消息：其他本机项目写入 `logs/outbox.jsonl`，飞书桥校验权限后发送飞书消息。
4. 日志与审计：写入 JSONL 机器日志，并追加项目内 Markdown 审计页。
5. 只读通讯录缓存：通过机器人应用可见范围同步成员，用唯一精确姓名解析出站目标。
6. 只读群目录缓存：分页同步机器人已加入的群，用唯一精确群名解析出站目标。
7. 富消息发送：纯文本、Markdown、卡片、图片和文件统一进入 outbox。
8. 有边界的读取：群消息列表/搜索/thread 以及 Docs/Wiki/Drive 检索和分范围读取。
9. 精确动作队列：actionbox 只允许以个人用户身份执行注册表中的已审查写动作。
10. 日程与任务：有边界地读取日程/任务，通过 actionbox 创建、修改、完成、分配、提醒或 RSVP。
11. 结构化数据：有边界地读取 Sheets/Base，并通过 actionbox 写明确区域、追加表格或新增/更新 Base 记录。
12. 固定能力注册表：固定 `lark-cli 1.0.92` 命令与参数快照，按 read/write/high-impact-write/remote-operation 分级。
13. 事件收件箱：官方 SDK 单一连接接入 23 个非 Approval EventKey，私有分卷、去重、脱敏且不自动触发业务写入。
14. 协作与内容补全：Whiteboard、Mindnotes、Markdown、Wiki/评论以及消息、日历、任务、Sheets、Base、会议和妙记的安全子集。
15. 妙搭 Apps 与组合工作流：固定 Apps 开发主链路，以及默认不发布的站会与会议素材包。
16. Codex 任务控制：由本机 KSFAssistant 显式绑定一个顶层 thread 与唯一授权单聊，观察/steer/interrupt 当前轮，并以全权限继续后续轮次；任务卡用单行“快速回复”处理普通文字，多行文字和各类附件通过飞书原生“回复卡片”精确路由；24 小时闲置租约和非敏感问题均由桥统一管理。
17. 独立默认对话与项目升级：授权单聊的每条根消息创建独立 Codex Thread，卡内追问和引用回复只续接对应卡片；默认卡与任务卡使用同一视觉外壳，未绑定时显示“Codex 对话”。明确项目延续语句通过 KSF 项目目录协议做精确匹配；普通对话每轮完成后也回读该 Thread 的 KSF 投影。只有 active 项目归属可验证时，才把原卡标题改为项目名并升级为任务控制卡。桥不从正文猜项目、不创建项目，也不允许已升级任务静默改绑。

明确不做：

- 不直接承载业务工作流。
- 不把飞书桥改造成任务系统、审批系统、知识库系统。
- 不绕过 outbox、逐次用户授权和审计主动发消息。
- 不把真实 App Secret、token、open_id、chat_id 写入版本化文档或代码。
- 不提供任意 OpenAPI、权限修改、删除、Wiki 移动或后台全量消息抓取。
- 不接 Approval、Base/Apps 自动化、Apps 秘密/数据库、实时会议控制或妙记原始媒体下载。

## 3. 代码结构

推荐目录结构：

```text
feishu-bot-bridge/
  AGENTS.md
  .agent/
    RULE.md
    PLANS.md
  bot-bridge.js
  package.json
  .env.example
  .env.local
  .gitignore
  lib/
    lark-cli-runner.js
    queue-worker-core.js
    action-registry.js
    capability-registry.js
    capability-executor.js
    lark-cli-flag-snapshot.js
    event-inbox.js
    event-subscriptions.js
  docs/
    configuration.md
    feishu-bridge-technical-spec.md
    feishu-codex-integration.md
    feishu-bridge-implementation-guide.md
  launchd/
    com.example.feishu-bot-bridge.plist
  scripts/
    start-bridge.js
  logs/
    messages.jsonl
    tasks.jsonl
    sessions.json
    outbox.jsonl
    outbox-results.jsonl
    outbox-state.json
    docbox.jsonl
    docbox-results.jsonl
    docbox-state.json
    actionbox.jsonl
    actionbox-results.jsonl
    actionbox-state.json
    bridge.pid
    task-runs/
```

关键文件职责：

- `bot-bridge.js`：主程序，包含事件消费、权限、路由、Codex 调用、outbox/docbox worker、wake server、日志和审计。
- `lib/official-event-adapter.js`：官方 SDK 单一长连接入口，负责事件传输。
- `lib/official-message-client.js`：常驻官方 SDK 卡片快路径，只负责状态卡回复与更新；其余 219 项能力仍由 `lark-cli` 承担。
- `lib/lark-cli-credentials.js`：在内存中复用 lark-cli 安全 profile 凭据，不写明文 secret。
- `lib/lark-cli-runner.js`：统一的 `lark-cli` 执行层，封装飞书 API、消息发送、消息回复、文档读取、文档更新和官方版本管理调用。
- `lib/contact-directory.js`：通讯录分页、去重、字段裁剪、安全缓存、姓名解析和同名绑定。
- `scripts/start-bridge.js`：跨平台启动包装器，读取 `.env.local` 和环境变量，补齐私有目录与 lark-cli 入口，并使用 macOS `caffeinate` 或 Windows `SetThreadExecutionState` 抑制休眠。
- `.env.example`：可提交的配置样例。
- `.env.local`：本机私有配置，不提交。
- `launchd/com.example.feishu-bot-bridge.plist`：macOS LaunchAgent 常驻配置。
- `windows/*.ps1`：Windows 当前用户计划任务、DPAPI、ACL、休眠抑制和 Codex 深链适配；完整步骤见 `docs/windows-runtime.md`。
- `docs/configuration.md`：配置文件说明和同事复刻检查清单。
- `docs/feishu-bridge-technical-spec.md`：当前技术规格。
- `docs/feishu-codex-integration.md`：飞书 x Codex 接入背景和实测记录。

## 4. 依赖与运行环境

基础要求：

- macOS 或 Windows 11。
- Node.js。
- 已安装 Codex CLI。
- 飞书企业自建应用，启用机器人能力。
- 飞书应用开启长连接事件订阅。

Node 依赖：

```json
{
  "dependencies": {
    "@larksuite/cli": "1.0.92",
    "@larksuiteoapi/node-sdk": "1.73.0"
  }
}
```

安装：

```bash
cd /path/to/feishu-bot-bridge
npm install
npm run bridge -- auth configure-existing --payload-file -
npx lark-cli auth status
```

语法检查：

```bash
node --check bot-bridge.js
node --check scripts/start-bridge.js
```

## 5. 飞书应用配置

应用类型：

```text
企业自建应用
```

应用能力：

```text
机器人
```

事件订阅：

```text
事件：im.message.receive_v1、card.action.trigger
订阅方式：长连接
```

消息、群聊和基础读取所需权限：

```text
im:message:send_as_bot
im:message.p2p_msg:readonly
im:message.group_at_msg:readonly
im:chat:readonly
contact:user.base:readonly
contact:user.id:readonly
```

Drive/Wiki/Docs 检索、读取使用 `lark-cli` 的个人用户身份。写入按位置固定分流：个人云空间使用个人用户身份，知识库空间使用机器人身份；目标空间必须显式授予机器人编辑权限。两种身份分开执行，不复制 token 到飞书桥配置，也不在权限失败后自动换身份。

启用全量通讯录缓存还需要应用全组织可见范围和只读通讯录权限。推荐 `contact:contact:readonly_as_app`；细分授权至少覆盖用户基础信息、用户部门关系、部门基础信息和用户 ID。不要申请通讯录写权限、手机号或邮箱读取权限。

飞书文档能力统一通过固定 lark-cli 适配器执行。创建文档使用 `docs +create → fetch`，正文只走 stdin；更新既有文档由飞书桥执行基础版本保护更新。需要文档能力时，参考 `docs/feishu-codex-integration.md`。

## 6. 配置文件

完整配置说明见 `docs/configuration.md`。本节只保留关键字段摘要。

`.env.example` 只放示例，不放真实值：

```text
LARK_CLI_BIN=./node_modules/.bin/lark-cli
LARK_CLI_PROFILE=default
LARK_CLI_AS=bot
FEISHU_EVENT_CONSUMER_ENABLED=true
FEISHU_EVENT_TRANSPORT=official-sdk
FEISHU_EVENT_KEYS=<copy the frozen 23-key non-Approval catalog from .env.example>
FEISHU_BRIDGE_LOG_DIR=./logs
KMS_ROOT=/Users/<user>/Documents/KMS
KSF_PROJECT_ROOT=/Users/<user>/Documents/KSF
FEISHU_AUDIT_DIR=./logs/audit

CODEX_BIN=codex
CODEX_TRANSPORT=auto
CODEX_AUTO_START_DAEMON=true
CODEX_FEISHU_DEFAULT_SESSION=feishu-default-kms
CODEX_FEISHU_DEFAULT_THREAD_TITLE=飞书默认对话
CODEX_TIMEOUT_MS=600000
CODEX_TASK_TIMEOUT_MS=1800000
CODEX_BYPASS_APPROVALS=true
CODEX_APP_SERVER_INITIALIZE_TIMEOUT_MS=5000
CODEX_APP_SERVER_REQUEST_TIMEOUT_MS=60000
CODEX_CLIENT_NAME=codex_vscode
CODEX_CLIENT_TITLE=Codex

FEISHU_DIRECT_ALLOWED_OPEN_IDS=ou_xxx
FEISHU_GROUP_ENABLED=true
FEISHU_GROUP_ALLOWED_CHAT_IDS=
FEISHU_GROUP_ALLOWED_OPEN_IDS=

FEISHU_OUTBOUND_ENABLED=false
FEISHU_OUTBOX_PATH=./logs/outbox.jsonl
FEISHU_OUTBOX_POLL_MS=3000
FEISHU_OUTBOUND_DRY_RUN=true
FEISHU_OUTBOUND_WAKE_ENABLED=false
FEISHU_OUTBOUND_WAKE_HOST=127.0.0.1
FEISHU_OUTBOUND_WAKE_PORT=0

FEISHU_DIRECTORY_ENABLED=false
FEISHU_DIRECTORY_CACHE_PATH=/Users/<user>/.config/feishu-bridge/directory.json
FEISHU_DIRECTORY_STATE_PATH=/Users/<user>/.config/feishu-bridge/directory-state.json
FEISHU_DIRECTORY_REFRESH_MS=21600000
FEISHU_DIRECTORY_MAX_AGE_MS=86400000
FEISHU_DIRECTORY_PAGE_SIZE=50
FEISHU_DIRECTORY_MIN_USER_COUNT=1

FEISHU_GROUP_DIRECTORY_ENABLED=false
FEISHU_GROUP_DIRECTORY_CACHE_PATH=/Users/<user>/.config/feishu-bridge/group-directory.json
FEISHU_GROUP_DIRECTORY_STATE_PATH=/Users/<user>/.config/feishu-bridge/group-directory-state.json
FEISHU_GROUP_DIRECTORY_REFRESH_MS=1800000
FEISHU_GROUP_DIRECTORY_MAX_AGE_MS=7200000
FEISHU_GROUP_DIRECTORY_PAGE_SIZE=100

FEISHU_DOCBOX_ENABLED=false
FEISHU_DOCBOX_PATH=./logs/docbox.jsonl
FEISHU_DOCBOX_POLL_MS=3000
FEISHU_DOCBOX_DRY_RUN=true
FEISHU_DOCBOX_WAKE_ENABLED=false
FEISHU_DOCBOX_WAKE_HOST=127.0.0.1
FEISHU_DOCBOX_WAKE_PORT=0
FEISHU_DOCBOX_ALLOWED_SOURCES=local,codex
```

`.env.local` 放本机真实配置，并加入 `.gitignore`：

```text
.env.local
logs
node_modules
```

启动包装器读取顺序：

1. 读取项目根目录 `.env.local`。
2. 叠加 shell 或 launchd 注入的环境变量。
3. 补齐默认 `LARK_CLI_BIN`、`LARK_CLI_AS` 和事件消费配置。

环境变量优先级高于 `.env.local`。

## 7. 启动方式

开发启动：

```bash
cd /path/to/feishu-bot-bridge
npm run start
```

推荐常驻启动：

```bash
npm run start:awake
```

`start:awake` 会通过 `scripts/start-bridge.js` 使用 `caffeinate` 防止系统休眠导致桥中断。

macOS LaunchAgent 安装示例：

```bash
launchctl bootstrap gui/$(id -u) /path/to/feishu-bot-bridge/launchd/com.example.feishu-bot-bridge.plist
launchctl kickstart -k gui/$(id -u)/com.example.feishu-bot-bridge
```

查看状态：

```bash
launchctl print gui/$(id -u)/com.example.feishu-bot-bridge
```

查看日志：

```bash
tail -f ~/Library/Logs/feishu-bot-bridge/launchd.out.log
tail -f ~/Library/Logs/feishu-bot-bridge/launchd.err.log
```

## 8. 入站消息链路

入站主流程：

```text
飞书用户或群聊
  -> 飞书机器人
  -> 官方 SDK 单一长连接
     -> im.message.receive_v1 / card.action.trigger
  -> bot-bridge.js
     -> message_id 去重
     -> 读取文本
     -> 识别 trace 和 key-value
     -> 权限判断
     -> 路由到命令、任务、默认对话或默认对话项目升级
     -> 回复飞书
     -> 写 messages.jsonl 和审计
```

单聊权限：

- 只响应 `FEISHU_DIRECT_ALLOWED_OPEN_IDS` 中的用户。
- 未授权单聊用户会收到拒绝提示。

群聊权限：

- `FEISHU_GROUP_ENABLED=false` 时不响应群聊。
- `FEISHU_GROUP_ALLOWED_CHAT_IDS` 为空时允许所有群。
- `FEISHU_GROUP_ALLOWED_OPEN_IDS` 为空时允许所有群内用户。
- 群聊默认只开放 help 和 cmd 类低风险命令。

命令模式：

```text
help
cmd
cmd <命令>
cmd命令
```

常用命令：

```text
cmd ping
cmd id
cmd whoami
cmd 群信息
cmd 成员列表
cmd 状态
cmd 最近消息
cmd 最近任务
cmd 任务 <task_id>
cmd 最近出站
cmd 出站 <OUT-id>
cmd 审计 今天
```

任务模式：

```text
task <任务描述>
```

行为：

- 立即回复“等待Codex响应中”。
- 创建本地任务 ID。
- 创建独立 Codex thread。
- 后台运行 Codex。
- 完成后回复飞书。
- 写入 `tasks.jsonl` 和 `task-runs/<task_id>.log`。

默认说话模式：

- 每条未引用卡片的普通文本创建独立 Codex Thread。
- 卡内追问和引用回复按私有消息绑定恢复对应 Thread，不会串到其他默认卡片。
- 同卡片轮次串行、不同卡片可并发；新 Thread 标题由 `CODEX_FEISHU_DEFAULT_THREAD_TITLE` 控制。
- `CODEX_FEISHU_DEFAULT_SESSION` 只用于清理旧版全局默认会话指针。

## 9. Codex 调用链路

以下传输开关只用于独立开发模式；KSFAssistant 托管模式会忽略它们并固定直连：

```text
CODEX_TRANSPORT=auto
CODEX_AUTO_START_DAEMON=true
CODEX_CLIENT_NAME=codex_vscode
CODEX_CLIENT_TITLE=Codex
```

KSFAssistant 托管调用顺序：

```text
短生命周期 codex app-server
  -> initialize（最多 5 秒）
  -> thread/start 或 thread/resume
  -> 新 Thread 同连接命名（最多 1 秒，失败不阻塞）
  -> turn/start / turn/completed
  -> thread/unsubscribe / 关闭连接
```

超时建议：

- 托管模式初始化固定最多 5000 ms，可由 `CODEX_APP_SERVER_INITIALIZE_TIMEOUT_MS` 单独约束；常规 RPC 继续使用 `CODEX_APP_SERVER_REQUEST_TIMEOUT_MS`，不得把初始化限时复用于 `thread/start`。
- `CODEX_TIMEOUT_MS=600000`
- `CODEX_TASK_TIMEOUT_MS=1800000`

如果任务运行时间长，优先使用任务模式，不要让飞书同步等待整段输出。

## 10. 出站消息链路

出站消息用于让本机其他项目或 Codex 主动给飞书用户或群发送消息。

设计原则：

- `logs/outbox.jsonl` 是唯一事实队列。
- wake 接口只负责唤醒，不接收业务消息体。
- polling 兜底，避免 wake 丢失。
- 发送结果以 `logs/outbox-results.jsonl` 为准。
- 出站目标不设本地白名单，任意显式 `chat_id` / `open_id` 都可以提交。
- 入站权限仍由独立白名单控制；出站放开不改变谁可以操控机器人。
- 启用通讯录后，唯一精确姓名可在入队前解析；同名必须首次选择，模糊匹配不得自动发送。

出站流程：

```text
调用方
  -> append 完整单行 JSON 到 logs/outbox.jsonl
  -> POST http://127.0.0.1:<wake-port>/internal/outbox/wake
  -> 查询 logs/outbox-results.jsonl
```

outbox 请求结构：

```json
{
  "id": "OUT-20260601120000-ABCD",
  "type": "text",
  "target": {
    "type": "chat_id",
    "id": "oc_xxx"
  },
  "text": "要发送的消息\n[TRACE:REQ-20260601-000001]",
  "source": "calling-project-name",
  "reason": "发送原因",
  "trace": {
    "system": "calling-system-name",
    "code": "REQ-20260601-000001",
    "objectType": "collaboration_request",
    "objectId": "xxx"
  },
  "createdAt": "2026-06-01T12:00:00.000+08:00"
}
```

字段规则：

- `id` 必填，全局唯一。
- `type` 第一版只支持 `text`。
- `target.type` 第一版只支持 `chat_id` 和 `open_id`。
- 单聊和群聊都使用 `chat_id`。
- 直接按用户 open_id 发送时使用 `open_id`。
- `source` 必填，用于审计调用方。
- `reason` 建议填写。
- `trace` 可选，飞书桥只原样记录和透传。

发送示例：

```bash
cd /path/to/feishu-bot-bridge

node -e 'const fs=require("fs"); const id=`OUT-${Date.now()}`; fs.appendFileSync("logs/outbox.jsonl", JSON.stringify({
  id,
  type: "text",
  target: { type: "chat_id", id: "oc_xxx" },
  text: `要发送的消息\n[TRACE:${id}]`,
  source: "manual-test",
  reason: "手动验证出站消息",
  trace: { system: "manual-test", code: id },
  createdAt: new Date().toISOString()
}) + "\n"); console.log(id);'
```

获取 wake 端口：

```bash
port=$(node -e 'const fs=require("fs"); const s=JSON.parse(fs.readFileSync("logs/outbox-state.json","utf8")); console.log(s.wake.actualPort)')
```

唤醒消费：

```bash
curl -X POST "http://127.0.0.1:${port}/internal/outbox/wake"
```

查看结果：

```bash
tail -n 10 logs/outbox-results.jsonl
tail -n 10 logs/messages.jsonl
```

结果状态：

```text
dry_run
sent
partial_sent
denied
failed
duplicate
invalid
```

状态含义：

- `dry_run`：只记录，不实际发送。
- `sent`：发送成功。
- `partial_sent`：长消息分段后部分成功。
- `denied`：兼容历史结果；当前目标策略不再因本地白名单产生该状态。
- `failed`：飞书 API 或内部错误。
- `duplicate`：重复 `id`，不重复发送。
- `invalid`：请求结构或 JSON 行不合法。

## 11. 文档任务 docbox 链路

docbox 用于让本机项目或人类请求创建或编辑飞书文档。它与 outbox 的边界不同：outbox 负责发飞书消息，docbox 负责编排文档任务。

- `create_document` 固定执行 `lark-cli docs +create`，并通过 `docs +fetch` 复读；不启动自由 Codex 任务。
- `update_document` 由飞书桥通过 `lib/lark-cli-runner.js` 执行基础版本保护更新：解析目标、读取原文、创建官方版本、更新正文、再次读取验证。

第一版支持结构化文档任务：

```text
调用方
  -> append 完整单行 JSON 到 logs/docbox.jsonl
  -> POST http://127.0.0.1:<docbox-wake-port>/internal/docbox/wake
  -> 查询 logs/docbox-results.jsonl
```

创建文档请求结构：

```json
{
  "id": "DOC-20260601123000-ABCD",
  "type": "document_task",
  "action": "create_document",
  "target": {
    "kind": "wiki_url",
    "value": "<FEISHU_WIKI_NODE_URL>"
  },
  "content": {
    "format": "markdown",
    "text": "# 标题\n\n正文内容"
  },
  "instruction": "请使用 lark-cli 创建一篇飞书新版文档，并挂载到目标位置；完成后返回文档标题和链接。",
  "source": "calling-project-name",
  "reason": "创建项目说明文档",
  "trace": {
    "code": "REQ-20260601-000001"
  },
  "createdAt": "2026-06-01T12:30:00.000+08:00"
}
```

编辑文档请求结构：

```json
{
  "id": "DOC-20260601124000-EFGH",
  "type": "document_task",
  "action": "update_document",
  "versionPolicy": "official_before_update",
  "target": {
    "kind": "url",
    "value": "<FEISHU_DOCX_URL>"
  },
  "content": {
    "format": "markdown",
    "text": "需要写入或追加的内容"
  },
  "updateMode": "append",
  "instruction": "请先读取目标文档，创建飞书官方文档版本，然后将以上内容追加到文档末尾，并返回版本信息和改动摘要。",
  "source": "calling-project-name",
  "reason": "补充会议纪要",
  "createdAt": "2026-06-01T12:40:00.000+08:00"
}
```

`update_document` 的 `versionPolicy` 可省略，默认值为 `official_before_update`。第一版不提供默认跳过版本的路径；编辑既有文档前必须先创建飞书官方文档版本。

`updateMode` 可省略，默认 `append`。第一版支持 `append`、`overwrite`、`str_replace`。日常测试和协同写入优先使用 `append`；`overwrite` 属于高影响操作，只在明确授权时使用；`str_replace` 容易误匹配，只用于测试文档或明确授权的局部替换，并通过 `--pattern-file` 提供唯一精确旧文本。

docx 官方版本管理固定使用 raw API：

```text
POST /open-apis/drive/v1/files/:file_token/versions
GET  /open-apis/drive/v1/files/:file_token/versions
```

请求体或参数包含 `obj_type: "docx"`。不要使用 `drive +version-history` shortcut 作为当前 docx 文档版本查询依据。

命令：

```text
cmd 最近文档
cmd 文档 <DOC-id>
```

状态：

```text
accepted
completed
dry_run
denied
failed
duplicate
invalid
```

安全规则：

- `FEISHU_DOCBOX_DRY_RUN=true` 时只记录任务意图，不创建版本、不创建或编辑文档。
- 飞书桥不维护桥内预览/确认状态机。
- `update_document` 必须先创建飞书官方文档版本；版本创建失败时不得继续写入。
- 高影响编辑应在请求中明确目标、更新模式、内容和授权边界；飞书桥只执行基础更新动作，不判断业务内容是否应被采纳。
- docbox 不移动 Wiki 节点，不修改协作者权限，不直接调用文档块编辑 API。

## 12. Trace 与轻量结构化解析

出站消息可以携带 `trace`：

```json
{
  "trace": {
    "system": "calling-system-name",
    "code": "REQ-20260601-000001",
    "objectType": "collaboration_request",
    "objectId": "xxx"
  }
}
```

飞书桥行为：

- 原样写入 `outbox-results.jsonl`。
- 原样写入 `messages.jsonl`。
- 原样写入项目内审计页。
- 不理解 `trace` 的业务含义。

入站消息如果包含：

```text
[TRACE:REQ-20260601-000001]
```

会在 `messages.jsonl` 中记录：

```json
{
  "trace": {
    "code": "REQ-20260601-000001"
  }
}
```

入站消息如果是明确 key-value 格式：

```text
结论：通过
问题：这里是问题描述
建议：这里是修改建议
```

会记录：

```json
{
  "parsed": {
    "fields": {
      "结论": "通过",
      "问题": "这里是问题描述",
      "建议": "这里是修改建议"
    }
  }
}
```

飞书桥不把 `结论：通过` 翻译为任何上层业务状态。上层系统需要自行读取并解释。

## 13. 日志与审计

`logs/messages.jsonl`：

- 入站消息。
- 授权结果。
- 本地命令处理。
- 飞书回复。
- Codex transport 状态。
- outbox 处理结果。
- 错误记录。

`logs/tasks.jsonl`：

- 任务创建。
- 任务完成。
- 任务失败。
- 继续任务关系。

`logs/outbox.jsonl`：

- 出站事实队列。
- 调用方只 append，不修改历史行。

`logs/outbox-results.jsonl`：

- 出站处理结果。
- 发送成功时包含飞书 `messageIds`。

`logs/outbox-state.json`：

- 已处理行数。
- 已处理 ID。
- 最近处理时间。
- 最近错误。
- wake server 实际端口。

项目内审计页：

```text
logs/audit/YYYY-MM-DD.md
```

JSONL 是机器事实，Markdown 审计页是人类可读摘要，两者都保留。

## 14. 从零复刻步骤

1. 创建飞书企业自建应用，启用机器人。
2. 开通消息、群聊和联系人基础权限。
3. 配置事件订阅为长连接，订阅 `im.message.receive_v1` 与 `card.action.trigger`。
4. 创建本地项目并精确安装 `@larksuite/cli` 与 `@larksuiteoapi/node-sdk`。
5. 编写 `bot-bridge.js`：
   - 初始化 lark-cli profile，由官方 SDK 在内存中复用其安全凭据建立唯一长连接。
   - 注册 `im.message.receive_v1` 与 `card.action.trigger` handler；卡片回调立即应答、业务异步执行。
   - 实现文本解析、去重、权限、路由、回复。
   - 接入 Codex app-server。
   - 写 JSONL 日志和审计。
   - 实现 outbox worker 和 wake server。
   - 保持 `lark-cli` 为唯一飞书 API/出站客户端，不启动第二个事件消费者。
6. 编写 `scripts/start-bridge.js`：
   - 读取 `.env.local`。
   - 读取 lark-cli profile 和 auth 状态。
   - 使用 `caffeinate` 拉起主程序。
7. 编写 `.env.example`，把真实配置写入 `.env.local`。
8. 配置 LaunchAgent。
9. 先 dry-run 测 outbox，再对用户明确授权的目标真实发送。
10. 写 `AGENTS.md`、`.agent/RULE.md` 和技术文档，方便 AI 接手。

## 15. 验证清单

基础检查：

```bash
node --check bot-bridge.js
node --check scripts/start-bridge.js
```

入站验证：

- 单聊 `cmd ping` 返回 `pong`。
- 单聊 `cmd id` 返回 `chat_id` 和 sender 信息。
- 群聊 @机器人 `cmd 状态` 返回桥状态。
- 未授权用户被拒绝。
- 非文本消息不进入 Codex。

Codex 验证：

- 两条默认根消息分别创建不同 Thread；卡内追问和引用回复只恢复各自 Thread。
- 某张默认卡升级项目后，其他默认卡与下一条根消息不受影响。
- 默认卡标题为“Codex 对话”；同一 Thread 形成可验证项目投影后，原卡标题改为项目名，任务原名保留为次级身份。
- 普通对话没有项目投影时不得仅凭正文项目词自动升级；已升级任务不得切换到另一个项目。
- `task <任务>` 能创建独立后台任务。
- `task任务` 或 `cmd 最近任务` 能查询任务。
- 回复任务消息能继续任务。

出站验证：

- `FEISHU_OUTBOUND_DRY_RUN=true` 时只写日志，不发消息。
- 任意显式目标在 dry-run 中被正常接受并留下脱敏审计。
- 用户明确授权的可达目标真实发送返回 `sent` 和 `messageIds`。
- 机器人不可达、未入群或平台权限不足时返回 `failed`，并保留飞书错误。
- 重复 `id` 返回 `duplicate`。
- 完整坏 JSON 行返回 `invalid`。
- 末尾半行不处理，等待补全后下轮处理。
- wake 失败时 polling fallback 仍处理。

docbox 验证：

- `create_document` 请求由固定创建与复读链路返回完成记录。
- `update_document` 请求先创建官方版本，再更新正文。
- 版本创建失败时返回 `failed`，不得继续更新正文。
- `cmd 文档 <DOC-id>` 能查看任务状态。
- `FEISHU_DOCBOX_DRY_RUN=true` 时返回 `dry_run`，不创建版本、不编辑文档。
- 未授权 source 返回 `denied`。
- lark-cli、版本 API、文档创建、复读或更新失败时返回 `failed`，并记录脱敏错误摘要。

审计验证：

- `messages.jsonl` 有入站、出站和错误记录。
- `outbox-results.jsonl` 有出站结果。
- `docbox-results.jsonl` 有文档任务结果。
- 项目内审计页有可读摘要。

## 16. 安全与运维原则

- 真实飞书 App Secret、访问 token 和 lark-cli 本地认证材料不入库。
- 真实 `chat_id`、`open_id` 不写入版本化文档。
- 出站目标不设本地白名单；依靠全局开关、逐次用户授权、dry-run、队列、去重、脱敏和审计控制风险。
- 群聊上线前先限定 chat_id。
- 高影响能力先 dry-run。
- 不用 wake 接口承载业务消息体。
- 不直接从其他项目调用飞书 OpenAPI 发消息。
- 不直接从其他项目调用飞书 OpenAPI 创建或编辑文档，先走 docbox。
- 改飞书桥运行逻辑前先读 `AGENTS.md`、`.agent/RULE.md`、`.agent/PLANS.md`。

## 17. AI 接手提示

AI 进入项目后应先读：

```text
AGENTS.md
.agent/RULE.md
.agent/PLANS.md
docs/feishu-bridge-technical-spec.md
docs/feishu-bridge-implementation-guide.md
```

涉及以下改动必须先写或更新 ExecPlan：

- 主动出站能力。
- 文档任务 docbox 能力。
- 飞书权限、入站白名单或发送边界。
- Codex app-server 调用链路。
- JSONL 日志或 项目内审计格式。
- 启动方式、launchd 或关键环境变量。
- HTTP API、队列、数据库或跨项目集成。

AI 调试时默认不读取或修改 `logs/` 历史运行数据，除非任务明确要求排查日志。

AI 需要从其他项目发送飞书消息时，应只写 outbox 并调用 wake，不能绕过飞书桥直接发消息。

AI 需要从其他项目创建或编辑飞书文档时，应写 docbox `document_task` 请求并调用 wake。创建文档由飞书桥执行固定 `lark-cli docs +create → fetch` 链路；更新既有文档由飞书桥执行版本保护更新。
