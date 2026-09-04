# 飞书 x Codex 对接说明（AI 接手版）

> 迁移期维护者参考：本文描述 Node 兼容实现，不是 CodexAssistant
> 普通用户配置说明；用户配置只在软件内完成。

## 目标

把 Codex 接入飞书，使其具备三类能力：

1. Codex 主动读取飞书文档、Wiki、文档结构和图片素材。
2. Codex 主动向飞书用户或群发送消息。
3. 飞书用户通过私聊机器人或群里 @ 机器人，把消息转给本地 bridge 处理。

当前已完成最小闭环，并验证通过。

## 当前架构

```text
Codex Desktop
  └─ 本地飞书机器人 bridge：feishu-bot-bridge
      ├─ 使用 lark-cli
      ├─ 使用官方 Node SDK 单一长连接接收事件
      ├─ 处理 im.message.receive_v1 与 card.action.trigger
      ├─ 执行命令解析
      ├─ 做响应权限判断
      ├─ 消费 outbox 主动发送飞书消息
      ├─ 消费 docbox 编排飞书文档任务
      ├─ 通过 codex app-server 创建/恢复 Codex thread
      ├─ 将任务写入 Codex Desktop 可见的本地会话系统
      └─ 回复飞书消息
```

## 本地文件

```text
MCP 配置：
${HOME}/.codex/config.toml

bridge 项目：
/path/to/feishu-bot-bridge

bridge 主程序：
/path/to/feishu-bot-bridge/bot-bridge.js

bridge 日志：
/path/to/feishu-bot-bridge/logs/messages.jsonl

bridge 出站队列：
/path/to/feishu-bot-bridge/logs/outbox.jsonl

bridge 文档任务队列：
/path/to/feishu-bot-bridge/logs/docbox.jsonl

bridge 本机私有配置：
/path/to/feishu-bot-bridge/.env.local

实现与复刻指南：
/path/to/feishu-bot-bridge/docs/feishu-bridge-implementation-guide.md
```

## 主动发送飞书消息

其他本机项目或 Codex 需要主动发送飞书消息时，应通过飞书桥 outbox，不直接调用飞书 OpenAPI。

调用顺序：

```text
1. append 完整单行 JSON 到 logs/outbox.jsonl
2. POST http://127.0.0.1:<wake-port>/internal/outbox/wake
3. 从 logs/outbox-results.jsonl 查看发送结果
```

wake 接口只负责唤醒消费，不接收业务消息体。wake 调用失败时，飞书桥仍会通过 polling fallback 消费 outbox。

出站请求由显式类型注册表约束。下面是最小文本消息示例；图片、文件、语音、媒体、卡片、回复、转发等已审查类型仍通过同一 outbox，正文和附件不进入命令行：

```json
{
  "id": "OUT-20260530123000-ABCD",
  "type": "text",
  "target": {
    "type": "chat_id",
    "id": "oc_xxx"
  },
  "text": "要发送的消息\n[TRACE:REQ-20260530-000001]",
  "source": "calling-project-name",
  "reason": "发送原因",
  "trace": {
    "code": "REQ-20260530-000001"
  },
  "createdAt": "2026-05-30T12:30:00.000+08:00"
}
```

安全边界：

- 出站目标不设本地白名单，任意显式 `chat_id` / `open_id` 都可提交。
- 启用全组织只读通讯录缓存后，统一客户端可把唯一精确姓名解析为 `open_id`；同名首次选择后保存本机绑定。
- 启用只读群目录缓存后，统一客户端可把机器人已加入群中的唯一精确群名解析为 `chat_id`；同名首次选择后保存独立本机绑定。
- 入站白名单继续独立生效；出站放开不改变谁可以操控机器人。
- dry-run 可只写日志和审计，不实际发送。
- 真实目标 ID 只进入本机安全配置、stdin 或 outbox，不写入版本化文档或代码。
- 飞书桥只透传 `trace`，不理解上层业务含义。
- 审查、任务、状态流转等业务语义由调用方系统负责。

手动发送示例：

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

port=$(node -e 'const fs=require("fs"); const s=JSON.parse(fs.readFileSync("logs/outbox-state.json","utf8")); console.log(s.wake.actualPort)')
curl -X POST "http://127.0.0.1:${port}/internal/outbox/wake"
tail -n 10 logs/outbox-results.jsonl
```

发送到单聊或群聊都使用 `chat_id`；发送到用户 open_id 时把 `target` 改为 `{ "type": "open_id", "id": "ou_xxx" }`。

## 创建或编辑飞书文档

其他本机项目或 Codex 需要创建或编辑飞书文档时，应通过飞书桥 docbox，不直接调用飞书文档 OpenAPI。

调用顺序：

```text
1. append document_task 请求到 logs/docbox.jsonl
2. POST http://127.0.0.1:<docbox-wake-port>/internal/docbox/wake
3. 飞书桥消费 docbox；创建文档时执行固定 `docs +create → fetch`，更新文档时执行版本保护更新
4. 从 logs/docbox-results.jsonl 查看 accepted / completed / dry_run / failed
```

支持 `create_document` 和 `update_document`。`create_document` 由飞书桥使用固定 `lark-cli docs +create` 创建，再以 `docs +fetch` 复读；正文只从 stdin 传入，不启动自由 Codex 任务。`update_document` 由飞书桥通过 `lark-cli` 执行基础版本保护更新。飞书桥只负责编排、状态、日志和审计，不判断业务内容是否应被采纳。`update_document` 默认采用 `versionPolicy: "official_before_update"`，编辑前必须先通过 raw API `/open-apis/drive/v1/files/:file_token/versions` 创建飞书官方文档版本；版本创建失败时不得继续写入。

## 飞书应用

```text
应用类型：企业自建应用
应用能力：机器人
事件订阅：长连接
事件：im.message.receive_v1、card.action.trigger
```

敏感信息处理原则：

- App Secret 不写入群聊文档。
- 官方 SDK 在内存中复用 lark-cli 安全 profile；不在启动脚本或 `.env.local` 重复保存。
- 公开说明里只保留配置项名称，不暴露 secret。

## 已验证权限

文档读取：

```text
offline_access
wiki:wiki:readonly
docx:document:readonly
drive:drive:readonly
```

消息与群：

```text
im:message:send_as_bot
im:message.p2p_msg:readonly
im:message.group_at_msg:readonly
im:chat:readonly
contact:user.base:readonly
contact:user.id:readonly
```

全量通讯录缓存额外要求应用可见范围覆盖全组织，并授予 `contact:contact:readonly_as_app` 或等价的用户基础信息、用户部门关系、部门基础信息和用户 ID 细分只读权限。不要为该能力授予通讯录写权限，也不需要手机号和邮箱读取权限。

## 历史 Codex MCP 配置要点

本节保留早期接入记录，方便排查旧环境。当前飞书桥主链路已迁移到 `lark-cli`，不再依赖该 MCP 配置。

`${HOME}/.codex/config.toml` 中新增 `lark_mcp`：

```toml
[mcp_servers.lark_mcp]
command = "npx"
args = [
  "-y",
  "@larksuiteoapi/lark-mcp",
  "mcp",
  "-a",
  "cli_xxx",
  "-s",
  "APP_SECRET_FROM_ENV_OR_LOCAL_CONFIG",
  "--oauth",
  "--token-mode",
  "auto",
  "--tools",
  "preset.default,preset.task.default,preset.calendar.default,docx.builtin.import,docx.v1.documentBlock.list,docx.v1.documentBlock.get,docx.v1.documentBlockChildren.get,drive.v1.media.batchGetTmpDownloadUrl,drive.v1.meta.batchQuery,drive.v1.permissionMember.create,wiki.v1.node.search,wiki.v2.space.getNode,wiki.v2.spaceNode.list,wiki.v2.spaceNode.create,wiki.v2.spaceNode.moveDocsToWiki",
  "--language",
  "zh"
]
startup_timeout_sec = 120
```

关键点：

- `--token-mode auto` 很重要。
- 读取用户文档通常需要用户授权 token。
- 读取 Wiki 子文件需要显式开启 `wiki.v2.spaceNode.list`；否则只能解析当前 Wiki 节点，无法列出下级页面。
- 写入知识库需要显式开启 `docx.builtin.import`、`wiki.v2.spaceNode.moveDocsToWiki`，必要时开启 `wiki.v2.spaceNode.create` 和 `drive.v1.permissionMember.create`。

### 写入飞书知识库

推荐写入流程：

```text
Markdown 内容
  -> docx.builtin.import 创建新版文档
  -> wiki.v2.space.getNode 解析目标父节点
  -> wiki.v2.spaceNode.moveDocsToWiki 挂载到知识库
  -> drive.v1.meta.batchQuery 获取最终链接
```

写入前必须确认：

```text
- 目标父节点链接或 node_token
- 文档标题
- 文档正文来源
- 是否允许挂载到该知识库位置
- 是否需要新增协作者权限
```

权限边界：

```text
- 创建、移动、改权限属于高影响操作
- 单聊可测试；群聊默认不开放写入
- 未明确目标时，只给方案，不直接创建
```

真实写入测试记录：

```text
目标父节点：
<FEISHU_WIKI_NODE_URL>

测试 1：Markdown -> Docx 导入
结果：失败
原因：应用身份缺上传/云空间写入权限
飞书错误码：99991672
缺失范围示例：docs:document.media:upload 或 drive:drive

测试 2：wiki.v2.spaceNode.create 创建空 Docx 节点
结果：失败
原因：应用身份对目标父节点没有编辑权限
飞书错误码：131006
原文：permission denied: node permission denied, tenant needs edit permission.
```

下一步授权：

```text
1. 在开发者后台补开应用身份写入权限：
   - docs:document.media:upload
   - drive:drive

2. 在目标知识库或目标父节点上给应用/机器人编辑权限：
   - 至少需要目标父节点容器编辑权限
   - 如果用 Markdown 导入再挂载，还需要应用对导入文档和目标知识库都有移动/挂载权限
```

注意：实际测试中未找到 `wiki:wiki:write` 这个权限项。Wiki 节点创建接口返回的关键限制不是缺 scope，而是目标节点编辑权限：

```text
错误码：131006
原文：permission denied: node permission denied, tenant needs edit permission.
```

群权限载体验证成功：

```text
方式：把机器人加入专用授权群，再把该群设为目标知识库/父节点可编辑协作者
目标父节点：AI办公最佳实践
父节点链接：<FEISHU_WIKI_NODE_URL>

成功 1：wiki.v2.spaceNode.create 创建空 Docx
标题：Codex空文档测试20260527-0943
链接：<FEISHU_WIKI_NODE_URL>

成功 2：Markdown -> Docx 导入 -> moveDocsToWiki 挂载
标题：Codex写入测试20260527-0944
链接：<FEISHU_WIKI_NODE_URL>
原始 Docx 链接：<FEISHU_DOCX_URL>
```

- 发机器人消息通常需要应用身份 token。
- `auto` 可以让官方 MCP 自动选择合适 token。

## OAuth 登录

登录命令：

```bash
npx -y @larksuiteoapi/lark-mcp login \
  -a cli_xxx \
  -s APP_SECRET \
  --scope offline_access,docx:document:readonly,wiki:wiki:readonly,drive:drive:readonly,im:message:send_as_bot,im:message.p2p_msg:readonly,im:message.group_at_msg:readonly,im:chat:readonly,contact:user.base:readonly,contact:user.id:readonly
```

开发者后台需要配置 OAuth 回调：

```text
http://localhost:3000/callback
```

验证登录：

```bash
npx -y @larksuiteoapi/lark-mcp whoami
```

## 文档读取验证

已验证能力：

```text
Wiki 链接 -> Wiki node -> docx token
docx token -> rawContent
docx token -> documentBlock.list
image token -> tmp_download_url
```

已验证样例：

```text
Wiki 标题：小瑞音箱需求文档
Wiki token：WIKI_NODE_TOKEN
Docx token：<FEISHU_DOCX_ID>
```

注意：

- `rawContent` 适合摘要、搜索、快速读。
- `documentBlock.list` 适合保留结构、表格、图片、块级信息。
- 图片需要用 `drive.v1.media.batchGetTmpDownloadUrl` 把素材 token 换成临时链接。

## 消息发送验证

已验证机器人可以向飞书群发送文本消息。

测试群：

```text
codex测试群
```

测试消息：

```text
Codex 飞书发消息能力测试：如果你看到这条消息，说明机器人通道已打通。
```

发送结果中确认：

```text
sender_type: app
sender_id: cli_xxx
msg_type: text
```

## 机器人事件消费 bridge

bridge 使用 `lark-cli`：

```bash
npm install @larksuite/cli
npm run bridge -- auth configure-existing --payload-file -
npx lark-cli auth status
```

启动方式：

```bash
cd /path/to/feishu-bot-bridge
FEISHU_DIRECT_ALLOWED_OPEN_IDS=ou_xxx \
npm start
```

启动成功日志应包含：

```text
ws client ready
```

### Codex 调用方式

当前 bridge 不再通过 `codex exec` 执行任务。普通任务按轮次启动短生命周期 `codex app-server`，并用 JSON-RPC 调用原生 Codex 会话能力。

默认传输策略为 `CODEX_TRANSPORT=auto`：

```text
- CodexAssistant 托管的普通根消息和后台任务固定使用短生命周期独立 `codex app-server`，不经过 proxy，也不自动启动 daemon。
- 初始化最多等待 5 秒；失败立即更新失败卡，禁止在不确定状态下重复创建 Thread 或 Turn。
- 新 Thread 命名复用同一短连接并限制为 1 秒，失败仅记录安全诊断，不阻塞轮次结果。
```

核心链路：

```text
飞书消息
  -> bridge 路由
  -> codex app-server
  -> thread/start 或 thread/resume
  -> turn/start
  -> 监听 item/agentMessage/delta、item/completed、turn/completed
  -> 写入 messages.jsonl / tasks.jsonl / task-runs / 项目内审计页
  -> 回复飞书
```

CodexAssistant 显式连接的既有 Desktop 任务采用独立链路：权威快照读取以及 `start / steer / interrupt` 均通过当前用户私有 Desktop IPC 定向交给目标任务的 Desktop 所有者。桥以标准本地 rollout 中的 `task_started / task_complete / turn_aborted` 作为轮次生命周期证据。这样不会由第二个 app-server `thread/resume` 已加载任务，也不会创建替代 thread。目标任务暂时没有所有者时，桥打开准确任务并等待 Desktop 接管后再提交。

效果：

```text
- 说话模式中每条根消息创建独立 Feishu/Codex Thread。
- 卡内追问和引用回复只恢复对应卡片绑定的 Thread；不同卡片可以并发。
- 任务模式创建独立 Codex thread。
- 飞书创建的 Codex 任务会进入标准 ~/.codex/sessions 与 session_index.jsonl。
- Codex Desktop UI 可以看到这些标准本地 thread，并可继续后续工作。
- 新根消息 Thread 使用 `飞书 · <首条消息摘要>` 标题；卡片与 Thread 的私有绑定是后续轮次的唯一恢复入口。
- 默认对话与传统后台任务的每个飞书轮次使用短生命周期 App Server 客户端；轮次结束后先 `thread/unsubscribe` 再关闭传输，让持久 Thread 可以立即由 Codex Desktop 接管输入。项目升级后的飞书控制改走 Desktop IPC。
- KSF 工作区只来自 Core 私有宿主上下文：就绪时以 KSF 根创建任务，未配置时使用托管通用工作区，失效时阻止创建并引导回软件设置；无权威项目投影的 KSF 根任务显示在“无项目”，不获得项目属性。
- 启动时只清理旧版全局默认 session 指针，不删除历史 Codex Thread。
- 审计日志仍保留，但只作为辅助索引，不再是主会话系统。
```

认证边界：

```text
- bridge 只通过 app-server 的 account/read 检查认证状态。
- bridge 不直接读取或写入 auth.json。
- bridge 不主动执行 Codex 登录流程，避免破坏 Desktop UI / CLI 共享认证。
```

相关环境变量：

```text
CODEX_BIN=codex
CODEX_TRANSPORT=auto
CODEX_AUTO_START_DAEMON=true
CODEX_CLIENT_NAME=codex_vscode
CODEX_CLIENT_TITLE=Codex
CODEX_APP_SERVER_INITIALIZE_TIMEOUT_MS=5000
CODEX_APP_SERVER_REQUEST_TIMEOUT_MS=60000
CODEX_DESKTOP_IPC_PATH=/Users/<user>/.codex/ipc/ipc.sock
CODEX_DESKTOP_REQUEST_TIMEOUT_MS=20000
CODEX_FEISHU_DEFAULT_SESSION=feishu-default-kms
CODEX_FEISHU_DEFAULT_THREAD_TITLE=飞书默认对话
CODEX_TIMEOUT_MS=600000
CODEX_TASK_TIMEOUT_MS=1800000
CODEX_BYPASS_APPROVALS=true
```

### 锁屏与睡眠

Mac 锁屏本身不会导致 bridge 失效；真正会断开的是系统睡眠。系统睡眠后网络断开，飞书长连接会进入反复 `reconnect` 状态。

本项目提供防睡眠启动方式：

```bash
cd /path/to/feishu-bot-bridge
npm run start:awake
```

该命令通过 `scripts/start-bridge.js` 启动：

```text
caffeinate -dimsu <node绝对路径> /path/to/feishu-bot-bridge/bot-bridge.js
```

效果：

```text
- bridge 运行期间阻止系统睡眠
- 允许屏幕关闭或锁屏
- 不修改系统全局 pmset 配置
- App Secret 从本机 lark-cli 安全 profile 在内存中读取，不写入启动脚本或日志
```

如果需要登录后自动启动并异常退出自动拉起，可使用模板：

```text
/path/to/feishu-bot-bridge/launchd/com.example.feishu-bot-bridge.plist
```

安装示例：

```bash
mkdir -p /Users/<user>/Library/Logs/feishu-bot-bridge
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.example.feishu-bot-bridge.plist 2>/dev/null || true
cp /path/to/feishu-bot-bridge/launchd/com.example.feishu-bot-bridge.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.feishu-bot-bridge.plist
launchctl enable gui/$(id -u)/com.example.feishu-bot-bridge
launchctl kickstart -k gui/$(id -u)/com.example.feishu-bot-bridge
```

检查示例：

```bash
launchctl print gui/$(id -u)/com.example.feishu-bot-bridge
pmset -g assertions | grep caffeinate
tail -f /Users/<user>/Library/Logs/feishu-bot-bridge/launchd.out.log
```

注意：LaunchAgent 的 stdout/stderr 放在 `~/Library/Logs/feishu-bot-bridge/`，避免 macOS 对 `Documents` 目录的后台进程访问限制。bridge 自己的业务日志仍在项目 `logs/` 目录。

## 当前 bridge 版本

当前版本：`v0.7.0-lark-cli`

已支持命令：

```text
help / 帮助
ping
id
whoami / 我是谁
群信息
成员列表
状态 / status
最近消息
最近任务
任务 <task_id 或序号>
审计 今天
echo <文本>
```

已暂时移除业务型命令：

```text
读文档
裸飞书文档链接自动读取
```

原因：当前阶段先规范机器人交互和权限边界，暂不接业务能力。

## 业务分流术语

后续讨论和实现飞书 bridge 路由时，统一使用以下三个术语：

```text
命令模式：
  入口：help、cmd、cmd <命令>
  语义：由本地 bridge 直接处理，不进入 Codex 默认对话，不创建后台任务。
  典型用途：help、ping、id、whoami、群信息、状态、审计查询。

任务模式：
  入口：task <任务描述>
  语义：创建独立 Codex thread 后台任务，每个任务独立执行，不污染默认说话上下文，并可在 Codex Desktop UI 中继续。
  典型用途：较长时间运行、需要读写项目文件、需要形成结果日志的工作。
  辅助入口：task任务 / task 任务，用于查看最近任务列表。
  续跑入口：task继续1 <追加要求> / task继续 TASK-xxx <追加要求>，用于基于已有任务继续执行。

说话模式：
  入口：不加前缀的自然语言消息
  语义：进入知识管理系统默认 Feishu Codex thread，保持连续上下文，用于日常协作问答。
  典型用途：解释、讨论、澄清、轻量分析。
```

当前策略：

```text
单聊：
  开放命令模式、任务模式、说话模式。

群聊：
  只开放命令模式里的基础命令。
  暂不开放任务模式。
  暂不开放说话模式。
```

任务引用规则：

```text
完整 ID：
  TASK-20260526-XXXX

短序号：
  #1 或 1，表示 task任务 返回的最近任务列表里的第 1 个任务。

示例：
  task任务
  cmd 任务 1
  task继续1 把刚才的结果整理成可发群里的版本
```

回复续跑规则：

```text
单聊中，如果用户直接回复某条任务相关的机器人消息：
  - 被回复消息属于任务创建、任务完成、任务失败或任务续跑提示
  - bridge 会通过 parent_id / root_id 反查对应任务
  - 用户回复内容会自动等价为 task继续 <该任务> <回复内容>

群聊中暂不启用回复续跑，避免其他人沿用单聊任务上下文继续执行。
```

## 测试与放开流程

后续新增或调整飞书 bridge 能力时，默认遵守以下流程：

```text
1. 所有新能力先在单聊里测试。
2. 单聊确认稳定、边界清楚、日志可查后，再考虑群聊放开。
3. 群聊按能力逐步放开，不一次性开放命令模式、任务模式、说话模式。
4. 每次放开群聊能力前，必须先检查权限边界和风险。
```

群聊放开前的权限检查至少包括：

```text
- 谁能触发：是否限制群、限制用户、限制 @ 机器人。
- 能触发什么：只读、写入、发消息、创建任务、调用 Codex、读取飞书文档分别检查。
- 结果发到哪里：是否会把单聊内容、私有文档、任务日志泄露到群里。
- 是否需要确认：写入、发送、创建任务、跨系统操作默认需要更高谨慎级别。
- 是否可审计：messages.jsonl、tasks.jsonl、task-runs、项目内审计页是否能追踪。
```

## 响应权限策略

当前规则：

```text
单聊：
只响应授权用户。

群聊：
响应群里 @ 机器人的消息。
默认允许群成员使用。
```

环境变量：

```text
FEISHU_DIRECT_ALLOWED_OPEN_IDS=ou_xxx
FEISHU_GROUP_ALLOWED_CHAT_IDS=
FEISHU_GROUP_ALLOWED_OPEN_IDS=
```

含义：

```text
FEISHU_DIRECT_ALLOWED_OPEN_IDS
  单聊允许名单。必须显式配置。

FEISHU_GROUP_ALLOWED_CHAT_IDS
  群聊允许群。空值表示所有群。

FEISHU_GROUP_ALLOWED_OPEN_IDS
  群聊允许用户。空值表示所有群成员。
```

日志会记录授权结果：

```json
{
  "direction": "authz",
  "kind": "direct",
  "allowed": true,
  "reason": "direct_allowed"
}
```

常见 reason：

```text
direct_allowed
direct_sender_not_allowed
group_allowed
group_chat_not_allowed
group_sender_not_allowed
unknown_chat_kind
```

## 飞书后台配置要求

必须开启：

```text
机器人能力
事件订阅：im.message.receive_v1
订阅方式：长连接
```

单聊输入框不可用时，优先检查：

```text
im:message.p2p_msg:readonly
im.message.receive_v1
应用是否重新发布
用户是否在应用可用范围内
```

群里 @ 没反应时，优先检查：

```text
im:message.group_at_msg:readonly
机器人是否在群里
是否真的 @ 了机器人，而不是只输入名字
长连接进程是否在线
状态中的事件传输是否为 official-sdk 且 connection=connected
```

## 当前已验证闭环

```text
1. Codex 读取飞书 Wiki / Docx：已通
2. Codex 读取文档 block 和图片素材：已通
3. Codex 主动发送飞书群消息：已通
4. 飞书群 @ 机器人 -> 本地 bridge 收到：已通
5. bridge 回复飞书消息：已通
6. 单聊与群聊响应权限：已实现
```

## 后续建议

下一阶段不要直接堆业务命令，先做能力编排层：

```text
轻量命令：
  help / ping / id / 状态 / echo

需要确认的命令：
  发消息给某人
  群公告
  创建任务
  写文档

业务型命令：
  摘要文档
  提取会议纪要
  建任务
  同步知识库

高风险命令：
  删除、移动、改权限、群管理、通讯录写入
```

推荐原则：

```text
先识别身份和场景。
再判断权限。
再判断命令风险等级。
最后执行或要求确认。
```

## 给后续 AI 的接手提示

如果你是后续接手的 AI：

1. 不要把 App Secret 发到群里。
2. 不要默认开放写入能力。
3. 先看 `/path/to/feishu-bot-bridge/bot-bridge.js`。
4. 先看 `/path/to/feishu-bot-bridge/logs/messages.jsonl`。
5. 如果要改 MCP，先备份 `${HOME}/.codex/config.toml`。
6. 如果要新增业务命令，先接入权限层，不要绕过 `authorizeMessage`。
7. 群聊默认只处理被 @ 的事件；不要主动监听全部群消息，除非明确开通并说明风险。
