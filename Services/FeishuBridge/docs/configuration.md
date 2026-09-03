# 飞书桥配置说明

更新日期：2026-09-02

本文是同事复刻飞书桥时的配置入口。真实配置统一写入项目根目录 `.env.local`，该文件已被 `.gitignore` 忽略，不应提交到仓库。

首次端到端安装先阅读 `docs/installation.md`；本文是环境变量和本机配置的详细参考。

## 1. 配置文件关系

```text
.env.example
可提交的示例配置。只允许写占位符和说明，不写真实 ID、secret、token、个人路径。

.env.local
本机真实配置。包含 lark-cli profile、入站白名单、审计目录等，不提交。

launchd/com.example.feishu-bot-bridge.plist
macOS LaunchAgent 模板。复制到本机后再替换路径和 Label，不直接写入真实业务 ID。

windows/*.ps1
Windows 用户级计划任务、DPAPI、ACL、休眠抑制和任务深链适配。完整流程见 `docs/windows-runtime.md`。
```

启动包装器 `scripts/start-bridge.js` 会先读取 `.env.local`，再叠加 shell、launchd 或 Windows 计划任务注入的环境变量。显式环境变量优先级高于 `.env.local`。

飞书 API 与主动出站只调用项目固定版本的本地 `lark-cli`。官方 SDK 单一入站长连接需要在内存中读取同一应用凭据：macOS 复用 lark-cli/Keychain，Windows 使用当前用户 DPAPI 文件；两端都不把明文写入 `.env.local`、命令行、日志或仓库。

统一客户端的目标别名和专用测试资产绑定另存于平台私有根的 `client.json`（schema v4），只读通讯录缓存另存于同目录的 `directory.json`。macOS 与 Windows 默认都使用 `~/.config/feishu-bridge`；macOS 使用 `0700/0600`，Windows 使用安装器设置的当前用户 ACL。该路径避开 Windows 打包应用的 `%LOCALAPPDATA%` 文件视图隔离，并且不进入项目仓库。

## 2. 最小可运行配置

复制示例文件：

```bash
cd /path/to/feishu-bot-bridge
cp .env.example .env.local
```

最小必填项：

```text
# 留空则由平台层选择 macOS shim 或 Windows 官方 run.js
LARK_CLI_BIN=
LARK_CLI_PROFILE=default
LARK_CLI_AS=bot
FEISHU_EVENT_CONSUMER_ENABLED=true
FEISHU_EVENT_TRANSPORT=official-sdk
FEISHU_EVENT_PROFILE_POLL_MS=1000
FEISHU_EVENT_KEYS=<copy the frozen 23-key non-Approval catalog from .env.example>
FEISHU_INBOUND_MAX_BYTES=26214400
FEISHU_BRIDGE_LOG_DIR=./logs
FEISHU_DIRECT_ALLOWED_OPEN_IDS=ou_xxx
```

含义：

- `LARK_CLI_BIN`：可选。留空时由平台层选择项目本地入口；Windows 不经 shell 执行 `.cmd`。
- `LARK_CLI_PROFILE`：`lark-cli` 配置 profile 名称。
- `LARK_CLI_AS`：调用身份，默认 `bot`。
- `FEISHU_EVENT_CONSUMER_ENABLED`：是否启动入站事件消费。
- `FEISHU_EVENT_TRANSPORT`：固定为 `official-sdk`。桥只建立一个官方 SDK 长连接，不并行启动 `lark-cli event consume`。
- `FEISHU_EVENT_PROFILE_POLL_MS`：运行时档位文件轮询间隔，默认 1000 ms，最小 250 ms；供 CLI 和后续 UI 无重启切换 `primary` / `manual-only`。
- `FEISHU_EVENT_KEYS`：必须是 `lark-cli 1.0.92` 冻结目录内的非 Approval 子集；生产默认使用 `.env.example` 中完整的 23 项目录，未知键和 Approval 键均 fail-closed。
- `FEISHU_EVENT_KEYS`：受控事件注册表，默认包含消息与卡片回调；不接受任意事件名。

运行时档位不写入 `.env.local`，而是写入平台私有数据根的 `event-consumer-profile.json`。缺省为 `primary`；`manual-only` 关闭本机入站长连接但不关闭本地与出站能力。由于飞书对同一应用的多个长连接执行竞争消费，系统不支持把默认消息和卡片回调拆到不同电脑；共享同一应用时必须只保留一个 `primary`。
- `FEISHU_INBOUND_MAX_BYTES`：单条授权单聊的附件总暂存上限，默认 25 MiB。临时文件权限为 `0600`，Codex 处理结束后清理。
- `FEISHU_BRIDGE_LOG_DIR`：本地 JSONL 日志目录。
- `FEISHU_DIRECT_ALLOWED_OPEN_IDS`：允许单聊访问机器人的用户 open_id 列表，多个值用英文逗号分隔。
- Usage Bar 的任务连接目标必须同时是 `open_id` 消息目标别名和 `FEISHU_DIRECT_ALLOWED_OPEN_IDS` 成员；`targets list` 只把满足两者的别名标为任务连接可用。桥不会因为主动出站配置而扩大入站操控权限。连接账本固定使用 `~/.config/feishu-bridge/task-links-v1.json`，schema v2 将连接/轮次状态分开并使用 24 小时闲置租约，无需新增环境变量。

默认不启用群聊和主动出站，避免 clone 后误发消息。

初始化 `lark-cli`：

```bash
npm install
npm run bridge -- auth configure-existing --payload-file -
npx lark-cli auth status
```

项目把 `@larksuite/cli` 精确固定在 `1.0.92`，把 `@larksuiteoapi/node-sdk` 精确固定在 `1.73.0`。`lark-cli` 是唯一飞书 API/出站客户端；官方 SDK 只负责单一入站长连接。桥只在内存中读取平台安全存储中的凭据，不把凭据复制到 `.env.local`、日志或命令行。`auth start-config --create-new` 只在用户明确要求新建飞书 CLI 应用时使用。升级任一依赖前必须重新验证事件帧分发、快捷命令参数、身份语义和审计脱敏。

## 3. 路径配置

```text
FEISHU_BRIDGE_PROJECT_ROOT=/path/to/feishu-bot-bridge
FEISHU_BRIDGE_LOG_DIR=./logs
KMS_ROOT=/path/to/knowledge-workspace
KSF_PROJECT_ROOT=/path/to/KSF
FEISHU_AUDIT_DIR=./logs/audit
FEISHU_BRIDGE_LAUNCHD_LABEL=com.example.feishu-bot-bridge
FEISHU_BRIDGE_WINDOWS_TASK_NAME=FeishuBotBridge
```

说明：

- `FEISHU_BRIDGE_PROJECT_ROOT`：项目根目录。手动 `npm run start` 时通常可以不填；常驻运行时可显式配置。
- `FEISHU_BRIDGE_LAUNCHD_LABEL`：macOS 服务标识，必须与安装后的 plist Label 一致。
- `FEISHU_BRIDGE_WINDOWS_TASK_NAME`：Windows 计划任务名，必须与安装器的 `-TaskName` 一致。
- `FEISHU_BRIDGE_DATA_DIR`：可选私有数据根。macOS 与 Windows 默认均为 `~/.config/feishu-bridge`。
- `FEISHU_BRIDGE_LOG_DIR`：可选运行日志目录。macOS 默认项目内 `./logs`，Windows 默认私有数据根内 `logs`。
- `KMS_ROOT`：个人知识管理系统根目录。没有固定知识管理系统目录时可以指向其他本地目录。
- `KSF_PROJECT_ROOT`：可选。用于“默认对话升级项目”的 KSF 根目录；未配置时依次尝试 `KMS_ROOT` 和当前用户的 `Documents/KSF`。桥只调用 KSF 的项目目录与任务投影协议，不直接解析或修改项目记忆卡。
- `FEISHU_AUDIT_DIR`：人类可读审计 Markdown 输出目录。需要关闭或替换审计时，修改这里即可。

不要把个人真实路径写入版本化文档、代码或 plist 模板。

## 4. Codex 配置

```text
CODEX_BIN=codex
CODEX_TRANSPORT=auto
CODEX_AUTO_START_DAEMON=true
CODEX_FEISHU_DEFAULT_SESSION=feishu-default-kms
CODEX_FEISHU_DEFAULT_THREAD_TITLE=飞书默认对话
CODEX_TIMEOUT_MS=600000
CODEX_TASK_TIMEOUT_MS=1800000
CODEX_BYPASS_APPROVALS=true
CODEX_APP_SERVER_REQUEST_TIMEOUT_MS=60000
CODEX_DESKTOP_IPC_PATH=/Users/<user>/.codex/ipc/ipc.sock
CODEX_DESKTOP_REQUEST_TIMEOUT_MS=20000
CODEX_CLIENT_NAME=codex_vscode
CODEX_CLIENT_TITLE=Codex
```

`CODEX_FEISHU_DEFAULT_SESSION` 只用于启动时识别并清理旧版全局默认会话指针，不再参与新消息路由。现在每条未引用卡片的默认输入创建独立 Thread；卡内追问和引用回复通过私有卡片绑定续接各自 Thread。`CODEX_FEISHU_DEFAULT_THREAD_TITLE` 仍控制新建默认 Thread 的标题。

建议保持：

- `CODEX_TRANSPORT=auto`：优先使用 app-server proxy，不可用时自动回退。
- `CODEX_AUTO_START_DAEMON=true`：proxy 不可用时尝试自动启动本机 daemon。
- `CODEX_DESKTOP_IPC_PATH`：可选。任务连接的写操作通过当前用户私有的 Codex Desktop IPC 交给任务所有者；默认使用 `~/.codex/ipc/ipc.sock`。桥会校验路径确为当前用户所有且权限不向组或其他用户开放。
- `CODEX_DESKTOP_REQUEST_TIMEOUT_MS`：Desktop 所有者请求的单次超时；默认 20 秒。目标任务未被窗口持有时，桥先打开准确任务并等待 Desktop 接管。
- `CODEX_CLIENT_NAME=codex_vscode`：已验证更容易让新 thread 出现在 Codex 本地 UI。
- `CODEX_BIN`：应指向支持当前 `~/.codex/config.toml` 模型的 Codex CLI。若服务端返回 `requires a newer version of Codex`，更新本机 `.env.local` 中的路径并重启 daemon 与飞书桥；不要在版本化配置中写入个人绝对路径，也不要静默降级模型。
- 如果 `codex app-server daemon version` 显示 managed daemon 仍旧于 `CODEX_BIN`，可暂时设置 `CODEX_TRANSPORT=app-server` 走独立 app-server；managed daemon 升级后恢复 `auto`。

## 5. 入站权限配置

```text
FEISHU_DIRECT_ALLOWED_OPEN_IDS=ou_xxx

FEISHU_GROUP_ENABLED=false
FEISHU_GROUP_ALLOWED_CHAT_IDS=oc_xxx
FEISHU_GROUP_ALLOWED_OPEN_IDS=ou_xxx
```

规则：

- 单聊必须命中 `FEISHU_DIRECT_ALLOWED_OPEN_IDS` 才响应。
- `FEISHU_GROUP_ENABLED=false` 时不处理群聊消息。
- 群聊上线前建议同时配置 `FEISHU_GROUP_ALLOWED_CHAT_IDS` 和 `FEISHU_GROUP_ALLOWED_OPEN_IDS`。
- 群聊白名单和用户白名单都为空时，容易扩大访问面，不建议用于共享版本默认配置。

## 6. 出站 outbox 配置

```text
FEISHU_OUTBOUND_ENABLED=false
FEISHU_OUTBOX_PATH=./logs/outbox.jsonl
FEISHU_OUTBOX_POLL_MS=3000
FEISHU_OUTBOUND_DRY_RUN=true
```

规则：

- `FEISHU_OUTBOUND_ENABLED=true` 才消费 outbox。
- 出站目标不设本地白名单；任意显式 `chat_id` / `open_id` 都可以提交，最终可达性仍由飞书平台权限决定。
- 入站用户和群聊白名单继续独立生效，不因出站放开而允许更多人操控机器人。
- 首次配置必须保持 `FEISHU_OUTBOUND_DRY_RUN=true`，确认日志和审计无误后再改成 `false`。

发送链路：

```text
append logs/outbox.jsonl
POST http://127.0.0.1:<wake-port>/internal/outbox/wake
read logs/outbox-results.jsonl
```

`outbox.jsonl` 是唯一出站事实队列。wake 接口只负责唤醒 worker，不接收业务消息体。

## 7. 只读通讯录缓存

```text
FEISHU_DIRECTORY_ENABLED=false
FEISHU_DIRECTORY_CACHE_PATH=/Users/<user>/.config/feishu-bridge/directory.json
FEISHU_DIRECTORY_STATE_PATH=/Users/<user>/.config/feishu-bridge/directory-state.json
FEISHU_DIRECTORY_REFRESH_MS=21600000
FEISHU_DIRECTORY_MAX_AGE_MS=86400000
FEISHU_DIRECTORY_PAGE_SIZE=50
FEISHU_DIRECTORY_MIN_USER_COUNT=1
```

规则：

- 默认关闭。启用前必须把应用通讯录可见范围设为全组织，并授予只读通讯录权限。
- 推荐授权 `contact:contact:readonly_as_app`；使用细分权限时，至少覆盖用户基础信息、用户部门关系、部门基础信息和用户 ID。
- 不申请通讯录写权限，不缓存手机号、邮箱、性别等与发送无关的字段。
- 桥启动时同步一次，之后默认每 6 小时刷新；缓存超过 24 小时后，按姓名发送会先同步一次，失败则不入 outbox。
- `FEISHU_DIRECTORY_MIN_USER_COUNT` 防止权限或可见范围异常时用空结果覆盖最后一次成功缓存。个人组织可保持为 `1`。
- 唯一精确姓名可直接发送；同名必须首次选择并写入本机姓名绑定。模糊匹配不会自动发送。
- 关闭 `FEISHU_DIRECTORY_ENABLED` 会停止后台同步和按姓名发送，不影响显式 ID、普通别名、入站权限或 docbox。

只读检查与手动同步：

```bash
npm run bridge -- targets directory status
npm run bridge -- targets directory sync
```

## 7.1 只读群目录缓存

```text
FEISHU_GROUP_DIRECTORY_ENABLED=false
FEISHU_GROUP_DIRECTORY_CACHE_PATH=/Users/<user>/.config/feishu-bridge/group-directory.json
FEISHU_GROUP_DIRECTORY_STATE_PATH=/Users/<user>/.config/feishu-bridge/group-directory-state.json
FEISHU_GROUP_DIRECTORY_REFRESH_MS=1800000
FEISHU_GROUP_DIRECTORY_MAX_AGE_MS=7200000
FEISHU_GROUP_DIRECTORY_PAGE_SIZE=100
```

规则：

- 只缓存机器人已经加入的群，不搜索未加入的公开群，也不自动入群。
- 缓存只保留群名称、`chat_id` 和必要区分字段，不保存群成员或消息内容；目录权限 `0700`、文件权限 `0600`。
- 唯一精确群名可直接发送；同名必须先用脱敏候选建立本机绑定，模糊群名不会自动发送。
- 缓存默认最多使用 1 小时；过期后发送前先刷新，刷新失败则不入 outbox。
- 群改名、机器人退群或绑定目标消失时返回 `binding_stale`，不会迁移到另一个同名群。
- 桥启动时立即同步群目录，之后默认每 30 分钟刷新一次；群改名、群解散、机器人入群或退群事件会触发防抖刷新。
- 最近一次完整缓存会在刷新失败时保留；只有连续失败并超过默认 2 小时有效期时，`doctor` 才显示 `degraded`。
- 该能力使用既有 `im:chat:readonly` 和机器人发送权限，不改变任何入站白名单。

只读检查、同步与搜索：

```bash
npm run bridge -- targets group-directory status
npm run bridge -- targets group-directory sync
npm run bridge -- targets group-directory search --query '项目群'
```

## 8. Wake 接口配置

```text
FEISHU_OUTBOUND_WAKE_ENABLED=false
FEISHU_OUTBOUND_WAKE_HOST=127.0.0.1
FEISHU_OUTBOUND_WAKE_PORT=0
```

规则：

- wake server 只能绑定 `127.0.0.1`。
- `FEISHU_OUTBOUND_WAKE_PORT=0` 表示系统自动分配可用端口。
- 实际端口会写入启动日志、`cmd 状态` 和 `logs/outbox-state.json`。
- wake 调用成功只表示“已触发扫描”，不表示消息已发送成功。

## 9. 文档任务 docbox 配置

```text
FEISHU_DOCBOX_ENABLED=false
FEISHU_DOCBOX_PATH=./logs/docbox.jsonl
FEISHU_DOCBOX_POLL_MS=3000
FEISHU_DOCBOX_DRY_RUN=true
FEISHU_DOCBOX_WAKE_ENABLED=false
FEISHU_DOCBOX_WAKE_HOST=127.0.0.1
FEISHU_DOCBOX_WAKE_PORT=0
FEISHU_DOCBOX_ALLOWED_SOURCES=local,codex
```

规则：

- docbox 用于“创建或编辑飞书文档”的任务编排，不是飞书消息发送通道。
- 飞书桥只负责编排、权限、日志、审计和唤醒；文档相关能力统一通过 `lark-cli` 执行。
- `create_document` 使用固定 `lark-cli docs +create → fetch` 链路，正文只走 stdin。
- `update_document` 由飞书桥执行基础版本保护更新：读取目标、创建官方版本、更新正文、再次读取验证。
- `FEISHU_DOCBOX_DRY_RUN=true` 时只记录任务意图，不创建版本、不创建或编辑文档。
- 第一版只支持 `type: "document_task"`，动作支持 `create_document` 和 `update_document`。
- `update_document` 默认使用 `versionPolicy: "official_before_update"`，编辑前必须先创建飞书官方文档版本。版本 API 固定走 `/open-apis/drive/v1/files/:file_token/versions`，需要 `drive:drive:version` 相关权限。
- `FEISHU_DOCBOX_ALLOWED_SOURCES` 控制允许写入 docbox 的调用方 source，多个值用英文逗号分隔。

docbox 请求链路：

```text
append logs/docbox.jsonl
POST http://127.0.0.1:<docbox-wake-port>/internal/docbox/wake
read logs/docbox-results.jsonl
```

常用命令：

```text
cmd 最近文档
cmd 文档 <DOC-id>
```

## 10. 飞书动作 actionbox 配置

```text
FEISHU_ACTIONBOX_ENABLED=false
FEISHU_ACTIONBOX_PATH=./logs/actionbox.jsonl
FEISHU_ACTIONBOX_POLL_MS=3000
FEISHU_ACTIONBOX_DRY_RUN=true
FEISHU_ACTIONBOX_ALLOWED_SOURCES=local,codex
FEISHU_ACTIONBOX_TIMEOUT_MS=120000
FEISHU_ACTIONBOX_WAKE_ENABLED=true
FEISHU_ACTIONBOX_WAKE_HOST=127.0.0.1
FEISHU_ACTIONBOX_WAKE_PORT=0
```

规则：

- actionbox 是精确动作注册表，不是任意 OpenAPI 转发器；只接受 1.0.0 注册表内、已声明身份与风险的受控写动作。
- outbox、docbox 和 actionbox 的每个写请求都必须携带 `explicitAuthorization: true`；统一客户端只在用户本轮明确要求动作、目标和内容时生成该字段。
- 评论正文、妙记总结/待办/关键词和结构化写 payload 从 stdin/文件进入客户端；大型结构化 payload 通过权限受限临时文件交给 lark-cli，执行后清理。
- Sheets 写入先检查目标区域和 revision；Base 写入先检查字段，记录更新还会写前、写后读取目标记录。
- 删除、清空、权限/角色/成员、实时会议控制、Base/Apps 自动化、Wiki 移动和任意 OpenAPI 仍不进入 actionbox。
- actionbox wake 只监听 `127.0.0.1` 的固定 `/internal/actionbox/wake`；wake 失败不改变请求终态判断，客户端继续 polling fallback，并保留原请求 ID。
- 首次启用保持 `FEISHU_ACTIONBOX_DRY_RUN=true`；读取知识库不要求启用 actionbox。
- 文档写入按目标位置选定身份：个人云空间固定使用 `user`，Wiki URL/节点及其中的内容固定使用 `bot`；身份不匹配时请求在入队或执行前失败，权限不足时不切换身份重试。
- 用户身份由 `lark-cli` profile 管理，需要相应个人 OAuth 和云文档权限；知识库 bot 写入还要求目标空间显式授予机器人编辑权限。飞书桥不复制或保存 OAuth token。

专用测试资产复用规则：

- 创建型注册能力只有显式声明了固定结果 ID 字段时才允许 `--save-as`，别名必须以“Codex桥测试”开头。
- 正式成功结果中的原始 ID 只写入 `client.json.testAssets`；公开结果只显示类型和不可逆指纹。
- 后续 capability payload 用 `asset:<别名>` 引用，且参数类型必须与绑定类型一致；dry-run、缺少固定结果字段或类型不匹配都不会创建或迁移绑定。
- 本机队列请求 ID 保留原值供 `result` 查询；飞书远端 session、release、task 等标识继续脱敏。

读取相关命令不进入 actionbox：消息列表/搜索使用机器人身份；Drive/Wiki/Docs、Calendar、Task、Sheets、Base、VC、Note 和 Minutes 读取使用个人身份，并分别强制时间窗、单页、区域、字段投影或逐字稿字符上限。

## 10.1 版本、队列和卡片回调

```text
FEISHU_QUEUE_MAX_PROCESSED_IDS=5000
FEISHU_QUEUE_WARN_BYTES=104857600
FEISHU_EVENT_KEYS=<lark-cli 1.0.92 固定的 23 个非 Approval EventKey>
FEISHU_EVENT_INBOX_ENABLED=true
FEISHU_EVENT_INBOX_DIR=~/.config/feishu-bridge/events
FEISHU_INBOUND_MAX_BYTES=26214400
```

- `status` 同时显示桥 1.0.0、能力 1.0.0、npm 1.2.0、Skill 兼容 `1.0.x`、0.6.1 稳定性基线和 queue schema v2；`doctor` 用 `healthy/degraded/failed` 区分正常、警告和硬故障。
- 事件收件箱目录必须为 `0700`，状态和 JSONL 文件必须为 `0600`。桥按日分卷、按事件 ID 去重，不保存逐字稿正文。
- 队列状态采用 schema v2 原子写入和有界去重表；达到文件阈值只报警，不自动压缩或删除审计历史。
- 图片、文件、音频、视频和富文本附件只在通过入站权限的单聊中下载，资源键不进入 Codex 提示或审计。
- 启用 `card.action.trigger` 前，还要在飞书开放平台“应用 → 事件与回调 → 回调配置”打开卡片回调；并授予机器人 `im:message:readonly`。

## 11. 单实例运行锁

飞书桥启动后会写入：

```text
logs/bridge.pid
```

该文件记录当前 `bot-bridge.js` 进程 PID 和启动时间，用于防止多个桥实例同时消费 outbox/docbox 或重复接收事件。正常退出时会自动清理；如果进程被强制杀死，下一次启动会检查旧 PID 是否仍存活，死进程的锁会被自动替换。

## 12. LaunchAgent 配置

复制模板：

```bash
mkdir -p ~/Library/LaunchAgents ~/Library/Logs/feishu-bot-bridge
cp launchd/com.example.feishu-bot-bridge.plist ~/Library/LaunchAgents/com.<your-name>.feishu-bot-bridge.plist
```

修改复制后的 plist：

- `Label`：改成自己的唯一 label，例如 `com.<your-name>.feishu-bot-bridge`。
- `ProgramArguments`：改成本机 Node 路径和项目路径。
- `WorkingDirectory`：改成本机项目路径。
- `HOME`：改成本机用户目录。
- `FEISHU_BRIDGE_PROJECT_ROOT`：改成本机项目路径。
- `StandardOutPath` / `StandardErrorPath`：改成本机日志路径。

加载：

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.<your-name>.feishu-bot-bridge.plist
launchctl kickstart -k gui/$(id -u)/com.<your-name>.feishu-bot-bridge
```

卸载：

```bash
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.<your-name>.feishu-bot-bridge.plist
```

## 13. 配置检查清单

提交仓库前确认：

- `.env.local` 未被 Git 跟踪。
- `logs/` 未被 Git 跟踪。
- 文档、代码、plist 模板中没有真实 `cli_` secret、`ou_`、`oc_`、`om_`、飞书文档 token 或个人业务链接。
- `.env.example` 只包含占位符。
- `launchd/` 中只保留模板 plist。

同事首次启动前确认：

- 飞书应用已开启机器人能力。
- 飞书应用已订阅 `im.message.receive_v1` 长连接事件。
- 飞书应用已开通发送消息、读取单聊和群聊消息所需权限。
- lark-cli profile 已安全初始化并可解析凭据；`.env.local` 已填写单聊白名单。
- 群聊入站先以最小白名单验证；主动出站先用 dry-run，再对用户明确授权的目标做真实验证。

## 14. AI 接手提示

AI 进入项目后应先读：

1. `AGENTS.md`
2. `.agent/RULE.md`
3. `docs/configuration.md`
4. `docs/feishu-bridge-technical-spec.md`
5. `docs/feishu-bridge-implementation-guide.md`
6. `docs/bridge-client.md`

涉及发送飞书消息时，只能写入 `outbox.jsonl` 并调用 wake，不能绕过飞书桥直接调用飞书 API。
