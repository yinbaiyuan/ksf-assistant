# 飞书桥统一客户端

`scripts/bridge-client.js` 是飞书桥 1.0.0 的本机稳定入口。消息写入只进 outbox，Docx 正文写入只进 docbox，其他注册写动作只进 actionbox；它不绕过显式授权、官方版本保护、固定能力注册表或审计链路。

## 认证与初始化

桥客户端提供跨平台认证入口，二维码 PNG 和临时授权状态只写入平台私有根 `~/.config/feishu-bridge/auth`。真实 App Secret、OAuth token 和飞书 ID 仍由 `lark-cli` 或平台安全存储管理，不进入队列、Skill、审计或版本化文件。

```bash
npm run bridge -- auth configure-existing --payload-file -
npm run bridge -- auth start-user
npm run bridge -- auth finish-user
```

`auth configure-existing` 复用已有飞书应用，从 stdin 或私有 JSON 文件读取 `{ "appId": "...", "appSecret": "...", "brand": "feishu" }`，Secret 不出现在命令行或公开结果中；Windows 会同步写入当前用户 DPAPI，供官方 SDK 长连接使用。`auth start-user` 默认请求桥当前固定注册表需要的完整 user scope，也可用 `--scope recommend` 做轻量验证；扫码完成后运行 `auth finish-user` 轮询并写入 `lark-cli` 的用户 OAuth 安全存储。

`auth start-config` 默认不创建新应用；没有既有 profile、Agent 绑定或安全导入时会 fail-closed。只有用户明确要创建新飞书 CLI 应用时，才使用 `auth start-config --create-new`，并扫描它返回的配置二维码。

## 初始化目标别名

```bash
cd /path/to/feishu-bot-bridge
npm run bridge -- targets init
```

客户端配置默认写入 `~/.config/feishu-bridge/client.json`。当前 schema 为 v4；macOS 使用 `0700/0600`，Windows 使用当前用户私有 ACL。统一的用户目录路径避免 Codex Desktop 打包进程与 Windows 计划任务落入不同的 `%LOCALAPPDATA%` 文件视图。初始化只创建安全配置文件，不从环境变量推导目标；消息目标必须由用户提供 ID 后显式建立别名。该文件也保存专用测试资产的本机绑定，真实飞书 ID 不进入仓库、Skill 或公开输出。

新增或修改别名时，真实目标 ID 必须从标准输入或文件读取，不能放入命令行参数：

```bash
printf '%s' '<open_id>' | npm run bridge -- targets set message 同事 --type open_id --value-file -
npm run bridge -- targets set message 项目群 --type chat_id --value-file /secure/path/chat-id.txt
npm run bridge -- targets list
npm run bridge -- targets remove message 项目群
```

## 通讯录缓存与按姓名发送

通讯录功能默认关闭。启用前需要把飞书应用可见范围设为全组织，并授予机器人应用只读通讯录权限；不需要通讯录写权限，也不读取手机号和邮箱。缓存默认位于平台私有根的 `directory.json`，不会进入仓库、JSONL 业务日志或 Markdown 审计。

```bash
npm run bridge -- targets directory status
npm run bridge -- targets directory sync
npm run bridge -- targets directory search --query '张三'
```

按姓名发送只接受精确匹配。唯一姓名会直接解析为 `open_id` 并继续走 outbox；模糊结果不会自动发送。同名时命令返回姓名、部门路径和脱敏指纹，用户选择后建立本机绑定：

```bash
npm run bridge -- targets directory bind --name '张三' --candidate 'sha256:...'
npm run bridge -- targets directory unbind --name '张三'
```

姓名绑定只保存于 `client.json`。如果被绑定成员离职或不再可见，桥会停止而不是把同一姓名自动迁移给另一人。

## 群目录缓存与按群名发送

群目录只读取机器人已经加入的群，默认缓存于平台私有根的 `group-directory.json`。它不会搜索未加入的公开群、读取群成员或修改群资料。

桥启动时会立即同步群目录，之后默认每 30 分钟刷新；群改名、群解散、机器人入群或退群事件会触发防抖刷新。刷新失败会保留最后一次完整缓存，缓存超过默认 2 小时后 `doctor` 才报告降级。按群名读取或发送仍保留过期前的兜底刷新。

```bash
npm run bridge -- targets group-directory status
npm run bridge -- targets group-directory sync
npm run bridge -- targets group-directory search --query 'codex测试群'
```

唯一精确群名会解析为 `chat_id` 后继续走 outbox。同名群返回群名、必要描述、是否外部群和脱敏指纹，用户选择后建立独立群名绑定：

```bash
npm run bridge -- targets group-directory bind --name '项目群' --candidate 'sha256:...'
npm run bridge -- targets group-directory unbind --name '项目群'
```

群改名、机器人退群或绑定失效时停止发送，不会自动迁移到另一个同名群。

## 状态与运维

```bash
npm run bridge -- status
npm run bridge -- doctor
npm run bridge -- profile catalog
npm run bridge -- profile show
npm run bridge -- profile set primary
npm run bridge -- profile set manual-only
npm run bridge -- capabilities
npm run bridge -- permissions
npm run bridge -- auth configure-existing --payload-file -
npm run bridge -- auth start-user
npm run bridge -- auth finish-user
npm run bridge -- start
npm run bridge -- restart
```

`status` 和 `doctor` 只读。它们检查当前平台服务管理器（macOS LaunchAgent 或 Windows 用户级计划任务）、PID、私有数据根权限、`lark-cli 1.0.92` 固定参数快照、官方 SDK 单一入站长连接、私有事件收件箱、Codex daemon、三类队列及 wake、dry-run、配置权限和最近错误，不会自动修复。`doctor` 在 `primary` 档位确认 23 个非 Approval EventKey 与长连接，在 `manual-only` 档位确认连接已关闭；不会输出 App ID、App Secret 或业务目标 ID。`degraded` 表示仍可运行但有缓存过期、仅 polling fallback 或队列体积警告；只有 `failed` 才是硬故障。

事件档位写入平台私有根的 `event-consumer-profile.json`，macOS 和 Windows 使用同一 schema。运行中的桥默认每秒读取一次，因此 CLI 或后续 UI 修改档位不需要重启：`primary` 保留消息、卡片和固定事件全部入站逻辑；`manual-only` 只断开官方 SDK 入站长连接，outbox、docbox、actionbox、目录和本地读取能力继续工作。同一飞书应用的多条长连接属于竞争消费，无法可靠按机器拆分消息事件与卡片事件，因此不提供 `cards-only` 档位；每个共享应用应始终只有一台机器处于 `primary`。

## 固定能力注册表

1.0.0 新能力通过统一入口调用，payload 必须从 stdin 或私有文件读取：

```bash
npm run bridge -- capability catalog --domain sheets
npm run bridge -- capability get sheets.chart.create
printf '%s' '{"spreadsheet-token":"...","sheet-id":"..."}' \
  | npm run bridge -- capability read sheets.chart.list --payload-file -
printf '%s' '{"file-token":"...","pattern":"旧值","content":"新值"}' \
  | npm run bridge -- capability write markdown.patch --payload-file - --confirm-high-impact
printf '%s' '{"name":"Codex桥测试-Sheets-1.0.0"}' \
  | npm run bridge -- capability write sheets.workbook.create --payload-file - \
      --save-as 'Codex桥测试-Sheets-1.0.0'
printf '%s' '{"spreadsheet-token":"asset:Codex桥测试-Sheets-1.0.0"}' \
  | npm run bridge -- capability read sheets.workbook.get --payload-file -
```

`read` 直接执行，但每个能力都有固定范围和数量上限。`write`、`high-impact-write` 与 `remote-operation` 必须从固定注册表进入相应队列；未知字段、身份切换、删除/清空、权限/角色/成员、任意 OpenAPI、任意 shortcut 和未确认高影响操作在入队前失败。完整矩阵见 `docs/capability-matrix-1.0.0.md`。

`--save-as` 只对注册表明确列出的创建能力开放，且别名必须以“Codex桥测试”开头。正式执行成功后，桥只从该能力固定声明的结果字段提取 ID，将原值写入 `client.json` 的 `testAssets`；公开结果仅显示别名、类型和指纹。后续 payload 可在类型匹配的标识字段使用 `asset:<别名>`。dry-run 不保存资产，类型不匹配或结果中缺少约定 ID 时 fail-closed，不猜测、不重试写入。

## 固定事件收件箱

```bash
npm run bridge -- events catalog
npm run bridge -- events status
npm run bridge -- events recent --limit 20
npm run bridge -- events get 'sha256:...'
npm run bridge -- events watch list
npm run bridge -- events watch add meeting
printf '%s' '<whiteboard_token>' | npm run bridge -- events watch add whiteboard --target-file -
```

`lark-cli 1.0.92` 实际有 25 个 EventKey；桥固定排除 2 个 Approval 事件，因此收件箱固定接入 23 个。事件按日写入平台私有根的 `events`，按事件 ID 去重，只保存事件类型、时间、长度和不可逆指纹；逐字稿事件不保存正文。事件本身不会自动发送消息或修改飞书对象。

## 只读组合工作流

```bash
npm run bridge -- workflow standup-report \
  --start '2026-09-02T08:00:00+08:00' --end '2026-09-02T18:00:00+08:00'
npm run bridge -- workflow meeting-summary --meeting-ids '<meeting_id>'
npm run bridge -- workflow meeting-summary --minutes-url 'https://tenant.feishu.cn/minutes/<token>'
```

`standup-report` 组合限定时间窗的日程和未完成任务，返回排序、冲突和空闲时段。`meeting-summary` 可从内部 meeting ID 或精确妙记链接开始，组合可获得的会议、Note、Minutes 及可选的有界逐字稿；两种目标必须二选一。两者都返回 `publish:false`，不会自行写文档；只有用户明确要求发布时，整理结果才另行进入 docbox。

## 入站附件与进度卡

授权单聊支持图片、文件、音频、视频和富文本。桥只通过 `lark-cli` 下载消息自带资源，暂存到项目内权限受限目录，单条消息默认最多 25 MiB；资源键不写入 Codex 提示，处理完成或失败后清理。群聊附件仍不交给 Codex。

Codex 的处理中、后台任务和终态使用同一张消息卡片展示。卡片创建与更新成功时，结果、错误和任务状态只写入原卡片，不再额外补发一条文字消息。卡片不再使用固定 7200 字符阈值，而是按最终消息更新请求体的 UTF-8 字节数动态计算：以飞书 30 KiB 上限为边界并预留 512 bytes 运输余量，先尝试完整回复；超限时在保留现有状态、表单和操作的前提下求得可容纳的最大回复前缀，并提示完整回复见后续文字消息。只有动态适配无法完整承载或卡片更新失败时才使用文字兜底，避免结果丢失。正文仍按每段最多 2400 字符拆为多个组件，这只是阅读与组件粒度，不是总内容上限。任务连接的持久化状态只保存摘要；终态卡片重绘、重启恢复和卡片操作会从 Codex 权威轮次重新取回完整回答，禁止用持久化摘要覆盖已经交付的完整正文。

普通对话卡与项目任务卡使用同一套 JSON 2.0 外壳：标题栏统一以语义色标签显示状态，正文按“辅助信息 → 你 → Codex → 后续操作”排列，原生细分隔线只负责划分内容。尚未形成项目归属时，标题固定为“Codex 对话”，不根据正文猜项目；KSF 为该 Thread 发布可验证项目投影后，原卡原位升级为项目任务卡，标题改为项目名，任务完整原名降为正文中的次级身份，不剥离项目名前缀。显式“继续某项目”仍先做目录精确匹配和归属验真；普通对话也会在每轮完成后回读同一权威投影，因此可以在讨论逐步落到项目时升级。已经升级的任务连接不会因后续对话静默改绑其他项目。

每条未引用的根消息创建独立 Codex Thread，名称固定为 `飞书 · <首条消息摘要>`；卡内追问和引用回复只续接该卡片绑定的 Thread。托管运行时从 Core 写入的 `codexassistant-host-context-v1` 读取软件内验证过的 KSF 状态：`ready` 使用 KSF 根目录并在尚无权威项目投影时自然进入 CodexAssistant 的“无项目”，`not_configured` 使用应用托管的一般工作区，`invalid` 则停止创建线程并引导用户回到软件设置修复。旧绑定保留原 Thread 与 cwd，不迁移。每个飞书轮次使用短生命周期独立 App Server，结束后取消订阅并关闭连接；托管模式固定直连 `codex app-server`，不尝试 proxy 或 daemon。项目升级后的飞书后续轮次统一交给 Desktop IPC。默认对话与任务卡都使用最长 1000 字的单行快速输入，并把“发送”放在输入框右侧；默认卡首行右侧直接显示与项目卡一致的红色描边“断开”按钮，复用原 `dismiss` 回调，只关闭飞书对话连接，不删除或停止 Codex 任务。进入 JSON 2.0 生命周期后，处理中、完成、失败和关闭状态不再切回旧 schema，也不输出旧卡专用根字段。终态辅助信息分开显示 `总耗时` 与 `Codex`，前者覆盖桥收到事件到取得结果，后者只表示真实轮次执行时间。

CodexAssistant 任务连接卡使用两种明确输入方式。日常短消息使用卡内单行输入；多行文字、图片、文件、音频、视频或富文本直接使用飞书的“回复卡片”发送，引用关系会把内容精确路由到对应任务，不再提供“长文本 / 附件”的选中与等待按钮。标题栏右侧先使用飞书原生语义色标签显示“运行中 / 已完成 / 等待回答 / 失败”等主状态，再显示一个与“已完成”样式一致的绿色模式标签；模式标签只使用“默认”或“计划”。标题下首行以加粗主文字显示不重复项目名的任务名，右侧使用红色描边“断开”；长任务名使用自适应列自然换行，按钮保持右上对齐。权限与模式不进入辅助信息，控制方、具体阶段、耗时和租约合并为下一行灰色辅助信息；“运行中”“当前进展”等通用阶段不重复显示。任务名与项目名相同时不再重复，辅助信息与“断开”共用首行。运行中与等待回答的表单严格按“停止 → 输入框 → 发送”排列：“停止”使用红色描边并保持独立回调，不提交输入内容；排队和等待 Desktop 没有输入框时，停止按钮位于正文下方。运行中不显示冗余字段标题，输入框占位文案为“补充或修正”；等待普通问题时仍标为“回答 Codex”。终态表单在左侧把“开始新一轮”和默认/Plan 下拉选择紧邻排列，模式和值随输入一次提交，并携带安全的 `intent=new_turn` 意图标记；模式选择只作用于这一轮，启动后以及完成、失败、停止或重启恢复时都会回到“默认模式”。桥会执行终态复核，状态已变化时拒绝整次提交，不会误当作当前轮补充。新卡不再预先显示独立模式按钮。Plan 轮次生成的完整计划会在原卡片的独立“计划”区按段持续同步；桥侧 Plan 轮次结束后会在串行锁内按 0、250、750 ms 读取 Desktop 权威快照，首次终态直接进入 `plan_ready`，不同的新轮次不会被旧完成态覆盖。待执行计划把内容宽度的“开始执行”居中放在完整计划正文后，并在下方保留“修改计划”输入与紧邻的次级“提交修改”；超过飞书单卡大小限制时，完整计划会在同一消息线程自动分段补发。排队、等待 Desktop、过期和连接已断开状态不显示输入入口。任务连接卡的按钮回调与后台进度不得分别直写飞书：两者统一进入同一任务的串行消息更新队列，常规更新固定优先使用消息 Patch，回调 Token 只作为该次写入失败后的队内兜底，防止旧完成态覆盖新运行态。旧版模式、附件等待和刷新回调继续兼容，但不再由新卡片生成。飞书移动端点击卡片输入组件是否进入独立编辑页属于客户端行为，卡片布局不承诺改变该交互。

运行时由桥监听 App Server 公开 `commentary`、计划事件和 Desktop rollout 的本轮用户消息与公开进展，在本机合并后主动更新原卡片；原始 reasoning、命令和工具参数不会展示。连续变化合并后推送，内容没有变化时不重建卡片；2 秒状态读取只负责漏事件、重启、等待窗口过期和终态恢复。所有动作均经过固定 action 注册、授权单聊、操作者校验和消息绑定，不接受任意飞书写动作。启用回调需要在飞书开放平台打开卡片回调配置，并授予机器人 `im:message:readonly`。桥会在飞书回调窗口内立即返回轻量 toast，再异步执行原有卡片动作，避免慢任务触发平台 `200671`。

CodexAssistant 集成使用 `task-link protocol|create|list|status|interrupt|release`。协议名保持 `codex-feishu-task-link-v1`，response version 为 2。`create` 只允许通过 `--payload-file -` 从 stdin 提交 thread ID、展示名称、项目名称和目标别名；客户端通过 Codex app-server 读取权威工作目录和轮次状态，拒绝子 thread、缺失本机目录和非授权单聊目标。`protocol` 同时报告主动出站、单聊入站、卡片回调、桥进程和 Codex daemon 就绪度。公开响应只返回不可逆 task key、展示状态、能力布尔值和 24 小时租约，不返回 thread、turn、飞书或消息 ID。

## 发送消息

文本、Markdown 和卡片正文必须从标准输入或文件读取，不能拼入 shell 命令：

```bash
printf '%s' '测试消息' | npm run bridge -- send --target 我 --text-file -
npm run bridge -- send --target 我 --text-file /path/to/message.txt
npm run bridge -- send --target-name '唯一姓名' --text-file /path/to/message.txt
npm run bridge -- send --target-group-name '唯一群名' --text-file /path/to/message.txt
printf '%s' '**进度已更新**' | npm run bridge -- send --target-group-name '唯一群名' --format markdown --content-file -
printf '%s' '{"elements":[]}' | npm run bridge -- send --target 我 --format card --content-file -
```

图片和文件先复制到项目内权限受限的临时目录，再由 `lark-cli` 上传；终态后清理临时副本，源文件不删除：

```bash
npm run bridge -- send --target-group-name '唯一群名' --format image --media-file /path/to/image.png
npm run bridge -- send --target 我 --format file --media-file /path/to/report.pdf
```

默认等待终态。需要异步提交时增加 `--async`，再用原请求 ID 查询：

```bash
npm run bridge -- result outbox '<request_id>'
```

如果 wake 失败，客户端会继续等待 polling fallback；超时只返回原请求 ID，不自动生成新请求重试。桥未运行时，已授权的发送任务最多尝试启动一次。

## 有边界的消息读取

群消息读取只接受精确群目标、带时区的起止时间和单页数量上限，不启用自动翻页，也不后台抓取消息。时间窗最多 31 天：

```bash
npm run bridge -- message list --target-group-name '唯一群名' \
  --start '2026-09-01T00:00:00+08:00' --end '2026-09-02T00:00:00+08:00' --limit 50
npm run bridge -- message search --target-group-name '唯一群名' --query '项目进度' \
  --start '2026-09-01T00:00:00+08:00' --end '2026-09-02T00:00:00+08:00' --limit 20
printf '%s' '<om_or_omt_id>' | npm run bridge -- message thread --message-id-file - --limit 50
```

读取默认使用机器人身份，返回内容只保留受限预览；`chat_id`、`open_id`、消息 ID 和 thread ID 统一显示为脱敏指纹。

## 飞书文档

更新既有文档前先读取当前摘要与 revision：

```bash
npm run bridge -- doc inspect --target '<feishu-wiki-or-docx-url>'
```

创建文档：

```bash
printf '%s' '# 标题\n正文' | npm run bridge -- doc create --content-file -
```

更新文档默认采用 `append`，并强制使用 `official_before_update` 版本策略：

```bash
printf '%s' '新增内容' | npm run bridge -- doc update --target '<url>' --mode append --content-file -
```

`overwrite` 和 `str_replace` 只有在调用方明确选择高影响操作时才允许提交：

```bash
printf '%s' '完整新正文' | npm run bridge -- doc update --target '<url>' --mode overwrite --confirm-high-impact --content-file -
printf '%s' '替换后的文本' | npm run bridge -- doc update --target '<url>' --mode str_replace --pattern-file /secure/path/exact-old-text.txt --confirm-high-impact --content-file -
```

`str_replace` 必须用受控文件提供唯一精确旧文本；`lark-cli 1.0.92` 不再支持旧的 title/ellipsis 选择参数。官方版本创建失败时，飞书桥不会执行正文写入。

文档写入身份由目标位置确定：个人云空间的 Docx/Markdown 使用 `user`；Wiki URL、Wiki token、Wiki 节点及其中的 Mindnote 使用 `bot`。队列会复核 identity，不允许调用方自由切换，也不会因权限失败自动回退。若知识库返回 `131006`，需要先在该知识库的成员管理中为机器人授予编辑权限。

## 知识检索、读取与评论

Drive/Wiki/Docs 检索和读取使用 `lark-cli` 的个人用户身份，依赖本机 profile 的用户授权，不复制 OAuth token：

```bash
npm run bridge -- knowledge search --query '季度规划' --types docx,wiki --limit 20
npm run bridge -- knowledge read --target '<feishu-url-or-alias>' --scope outline
npm run bridge -- knowledge read --target '<feishu-url-or-alias>' --scope keyword --keyword '风险'
npm run bridge -- knowledge read --target '<feishu-url-or-alias>' --scope full
npm run bridge -- knowledge comments list --target '<feishu-url-or-alias>' --limit 50
```

`outline` 是默认读取范围；只有用户明确要求全文时才用 `full`。评论新增继续使用受控 actionbox；1.0.0 还通过固定能力注册表提供回复、回复更新、解决/恢复和回复表情。所有写动作都要求明确目标与正文，正文从 stdin/文件进入队列：

```bash
printf '%s' '请核对这一段。' | npm run bridge -- knowledge comments add \
  --target '<feishu-url-or-alias>' --content-file -
npm run bridge -- result actionbox '<request_id>'
```

actionbox 默认关闭且 dry-run；启用后只接受精确注册表中的动作，不提供任意 OpenAPI、权限修改、删除或移动动作。

所有队列写命令都支持单次 `--dry-run`，并由客户端显式写入 `explicitAuthorization: true`。请求上的 `dryRun: true` 优先于进程级开关，因此桥保持运行且全局允许真实写入时，仍可按“同一输入先演练、再以新请求 ID 正式执行”的方式验收。请求级 dry-run 会进入结果与审计，但不会调用发送、文档或动作执行器，也不会建立测试资产绑定。

## 日程与任务

日程和任务固定使用个人用户身份。读取命令不自动全量翻页；日程搜索必须提供不超过 40 天的范围：

```bash
npm run bridge -- calendar agenda --start '2026-09-01' --end '2026-09-08'
npm run bridge -- calendar search --query '评审' --start '2026-09-01' --end '2026-09-30' --limit 20
npm run bridge -- calendar get --event-id '<event_id>'
npm run bridge -- calendar freebusy --start '2026-09-02T09:00:00+08:00' --end '2026-09-02T18:00:00+08:00'

npm run bridge -- task mine
npm run bridge -- task related --created-by-me
npm run bridge -- task search --query '上线' --completed false
npm run bridge -- task get --task-id '<task_guid>'
npm run bridge -- task tasklists --limit 50
```

写动作 payload 必须从 stdin 或文件读取。事件时间必须是带时区的 ISO 8601；修改已有日程会先读取目标事件。重复日程必须由调用方先明确“仅此次/全部/此次及后续”并提供对应 `eventId`：

```bash
npm run bridge -- calendar create --payload-file /secure/event-create.json
npm run bridge -- calendar update --payload-file /secure/event-update.json
npm run bridge -- calendar rsvp --payload-file /secure/event-rsvp.json

npm run bridge -- task create --payload-file /secure/task-create.json
npm run bridge -- task update --payload-file /secure/task-update.json
npm run bridge -- task complete --payload-file /secure/task-complete.json
npm run bridge -- task reopen --payload-file /secure/task-reopen.json
npm run bridge -- task assign --payload-file /secure/task-assign.json
npm run bridge -- task reminder --payload-file /secure/task-reminder.json
```

日程创建 payload 使用 `calendarId`、`summary`、`start`、`end`、可选 `description`、`attendeeIds` 和 `rrule`。更新和 RSVP 增加必填 `eventId`。任务创建使用 `summary` 及可选 `description`、`due`、`assignee`、`follower`、`tasklistId`；其余任务写动作都需要 `taskId`。

## 电子表格与多维表格

电子表格读取必须给出明确工作表和有界 A1 区域；整列、整行和全工作簿正文读取会被拒绝：

```bash
npm run bridge -- sheets inspect --target '<sheets_url>'
npm run bridge -- sheets cells --target '<sheets_url>' --sheet-name 'Sheet1' --range 'A1:F100'
npm run bridge -- sheets table --target '<sheets_url>' --sheet-name 'Sheet1' --range 'A1:F100'
npm run bridge -- sheets search --target '<sheets_url>' --sheet-name 'Sheet1' --range 'A1:F100' --find '阻塞'
npm run bridge -- sheets revision --target '<sheets_url>'
```

Sheets 写入会先读取 revision。`set-cells` 还会先读目标区域、写后复读；默认拒绝覆盖非空单元格，只有 payload 中显式写入 `allowOverwrite: true` 且命令增加 `--confirm-high-impact` 才允许覆盖：

```bash
npm run bridge -- sheets create-sheet --target '<sheets_url>' --payload-file /secure/sheet-create.json
npm run bridge -- sheets set-cells --target '<sheets_url>' --payload-file /secure/cells.json
npm run bridge -- sheets append-table --target '<sheets_url>' --payload-file /secure/table-append.json
```

多维表格读取先解析明确 URL，再使用真实 Base token；记录读取最多 200 条并支持字段投影：

```bash
npm run bridge -- base inspect --target '<base_url>' --limit 50
npm run bridge -- base schema --target '<base_url>' --table-id '任务' --limit 100
npm run bridge -- base records --target '<base_url>' --table-id '任务' --fields '标题,状态' --limit 100
npm run bridge -- base search --target '<base_url>' --table-id '任务' --query '上线' --search-fields '标题' --fields '标题,状态' --limit 20
npm run bridge -- base get --target '<base_url>' --table-id '任务' --record-ids '<record_id>' --fields '标题,状态'
```

Base 新增或更新前都会读取真实字段列表并拒绝未知字段；更新还会在写前、写后读取指定记录。单批最多 200 条，payload 从 stdin/文件进入，行数据不会进入 lark-cli 的进程参数：

```bash
npm run bridge -- base create-records --target '<base_url>' --payload-file /secure/base-create.json
npm run bridge -- base update-records --target '<base_url>' --payload-file /secure/base-update.json
```

P2/P3 用户授权使用 `lark-cli auth login --domain calendar,task,sheets,base --no-wait --json` 发起；应用后台和个人 OAuth 两层都必须具备相应只读/写入权限。

## 会议、智能纪要与妙记

会议、智能纪要（Note）和妙记（Minutes）固定使用个人用户身份。会议与妙记搜索至少要提供关键词、时间、组织者/所有者或参与人之一；时间窗最多 90 天，单页最多 30 条，不自动全量翻页：

```bash
npm run bridge -- meeting search --query '周会' --start '2026-08-01' --end '2026-08-31' --limit 15
npm run bridge -- meeting active
npm run bridge -- meeting get --meeting-id '<meeting_id>' --with-participants
npm run bridge -- meeting detail --meeting-ids '<meeting_id>'
npm run bridge -- meeting events --meeting-id '<meeting_id>' --limit 50
npm run bridge -- meeting recording --meeting-ids '<meeting_id>'

npm run bridge -- note detail --note-id '<note_id>'
npm run bridge -- note transcript --note-id '<note_id>' --max-chars 20000

npm run bridge -- minutes search --query '项目复盘' --start '2026-08-01' --end '2026-08-31' --limit 15
npm run bridge -- minutes get --minute-token '<minute_token>'
npm run bridge -- minutes detail --minute-token '<minute_token>' --summary --todo --chapter --keyword
npm run bridge -- minutes transcript --minute-token '<minute_token>' --max-chars 20000
```

逐字稿先写入权限受限的系统临时目录，读取后立即清理。默认返回最多 20,000 字符，调用方可在 `1..100000` 范围内明确调整；审计只记录目标指纹、上限和实际返回字符数。会议、Note、Minutes 标识及会议/妙记 URL 默认脱敏。

妙记写动作必须从 stdin 或文件读取 JSON payload，并统一进入 actionbox：

```bash
npm run bridge -- minutes upload --payload-file /secure/minutes-upload.json
npm run bridge -- minutes update-title --payload-file /secure/minutes-title.json
npm run bridge -- minutes replace-summary --confirm-high-impact --payload-file /secure/minutes-summary.json
npm run bridge -- minutes todos --payload-file /secure/minutes-todos.json
npm run bridge -- minutes replace-words --confirm-high-impact --payload-file /secure/minutes-words.json
npm run bridge -- minutes replace-speaker --confirm-high-impact --payload-file /secure/minutes-speaker.json
```

payload 契约：

- `upload`：`fileToken`。
- `update-title`：`minuteToken`、`topic`。
- `replace-summary`：`minuteToken`、`summary`；这是全文覆盖，必须传高影响确认。
- `todos`：`minuteToken`、`todos[]`；只允许 `add` / `update`，不允许 `delete`。
- `replace-words`：`minuteToken`、`replacements[]`，每项为 `source_word` / `target_word`；必须传高影响确认。
- `replace-speaker`：`minuteToken`、`fromSpeakerId`、`toUserId`；必须传高影响确认。

修改标题、总结和待办会先读取目标并在写后复读；关键词替换使用飞书返回的逐项命中结果作为验证；说话人替换前后读取 speaker list。上传生成妙记是异步飞书任务，actionbox 的 `completed` 只表示创建请求已提交，产物是否就绪仍需随后执行 `minutes detail`。

本能力需要用户 OAuth 覆盖：`vc:meeting.search:read`、`vc:meeting`、`vc:meeting.meetingevent:read`、`vc:record:readonly`、`vc:note:read`、`minutes:minutes.search:read`、`minutes:minutes.basic:read`、`minutes:minutes:readonly`、`minutes:minutes:update`、`minutes:minutes.upload:write`。`vc:meeting` 是当前飞书控制台提供的会议基础权限，旧权限码 `vc:meeting:readonly` 已不可用。应用后台也必须发布包含相同能力的版本；`permissions` 会分别检查应用权限和用户 OAuth，两层任一缺失都不会报告完整。

本版本不提供机器人入会/离会/结束会议、会中邀请/消息/截图/倒计时、原始媒体下载、妙记权限修改或删除动作。

## 结果与审计

```bash
npm run bridge -- result outbox '<request_id>'
npm run bridge -- recent outbox --limit 20
npm run bridge -- recent docbox --limit 20
npm run bridge -- recent actionbox --limit 20
npm run bridge -- recent tasks --limit 20
npm run bridge -- recent audit --limit 20
```

默认 JSON 输出隐藏真实目标和远端对象 ID，只显示别名、目标类型和脱敏指纹。本机队列请求 ID 保留原值，保证 `--async` 后可以用同一 ID 查询终态；远端 session、release、task 等 ID 仍只输出指纹。

## 能力边界

- 消息支持纯文本、Markdown、卡片、图片和文件；目标支持任意显式 `open_id` / `chat_id` 或本机已配置别名。
- 启用只读通讯录后，目标也支持唯一精确姓名或用户确认过的同名绑定；姓名解析发生在 outbox 入队前。
- 启用只读群目录后，目标也支持机器人已加入群中的唯一精确群名或已确认同名绑定；不会搜索未加入的公开群。
- 不会对模糊人员或群名自动选择第一项。
- Docx 基础入口支持摘要检查、创建和 `append` / `overwrite` / `str_replace`；1.0.0 注册表另外提供历史版本、媒体、资源、窄类型 Whiteboard、Markdown 和 Mindnotes 安全子集，复杂任意块编辑仍排除。
- 支持受限群消息列表、关键词搜索和 thread 读取；必须给出时间/数量边界，不支持全量历史抓取或持续监听。
- 支持 Docs/Wiki/Drive 检索、分范围读取，以及 1.0.0 注册表中的 Wiki 创建/复制和评论回复、解决/恢复等受控动作。
- 支持日程/任务有界读取，以及注册表中的日历、会议室、智能时间、任务清单、父子任务、附件、分组与自定义字段安全子集。
- 支持 Sheets 和 Base 的 1.0.0 全量安全子集；删除、清空、权限、公开设置、自动化和未审查的删除型批处理仍排除。
- 支持会议、智能纪要和妙记的有界查询、逐字稿临时读取，以及 actionbox 内受控的妙记生成与编辑。
- 支持妙搭 Apps 开发主链路的固定读取、应用/会话/Chat/Release 受控写入与远端终态轮询；不接秘密、数据库、自动化、成员角色或删除能力。
- 不支持权限修改、Wiki 节点移动、删除、复杂块编辑、通讯录写入、Sheets/Base 结构删除、Base 工作流或任意 OpenAPI。
- 不支持实时会议控制、妙记原始媒体下载或妙记权限变更。
- 取消出站目标白名单不代表能突破飞书平台权限；机器人不可达目标、未加入群或应用权限不足时仍会发送失败。
- 每次发送仍需用户明确提供动作、目标和正文，并保留全局开关、dry-run、队列、去重、脱敏和审计。
