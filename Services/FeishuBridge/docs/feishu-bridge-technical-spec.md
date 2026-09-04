# 飞书桥技术规格

> 迁移期维护者参考：本文主体记录 Node 兼容实现。CodexAssistant 的 Go
> 迁移目标与当前切换边界以仓库 `docs/ARCHITECTURE.md` 和 roadmap 为准。

首次部署入口为 `docs/installation.md`；本文件描述当前运行契约。

更新日期：2026-09-03
当前桥 / capability 版本：v1.0.0；npm 包版本：v1.2.0；Skill 兼容：`1.0.x`（保留 v0.6.1 稳定性基线）

## 跨平台宿主

业务核心在 macOS 与 Windows 共用。平台适配统一收敛在 `lib/platform-runtime.js`：macOS 使用 LaunchAgent、Keychain/POSIX 私有目录、`caffeinate` 和 Unix Socket；Windows 使用当前用户计划任务、DPAPI/ACL 私有目录、`SetThreadExecutionState` 和可选命名管道。Windows 安装、运维与真机验收见 `docs/windows-runtime.md`。平台差异不得改变入站权限、显式发送授权、队列 schema、能力注册、版本保护或审计语义。

## Codex 任务远程控制连接

`codex-feishu-task-link-v1` response v2 允许本机 CodexAssistant 将一个已存在的顶层 Codex thread 主动连接到单聊白名单中的唯一目标。初始任务卡必须经过 outbox；回复只在命中该卡或后续结果消息且操作者与连接目标完全一致时路由到原 thread。CodexAssistant 只提交 thread ID、展示名称、项目名称和目标别名；桥通过 `thread/read` 获取权威 `cwd`、当前轮次和任务状态，并拒绝子 thread。连接状态保存在 `~/.config/feishu-bridge/task-links-v1.json`，目录权限 `0700`、文件权限 `0600`，拒绝符号链接、使用同目录原子替换，并把旧的扁平状态迁移为连接状态、轮次状态、轮次所有者和待操作方。

授权单聊中的每条默认根消息创建独立 Codex Thread，卡内追问和引用回复只续接该卡片的 Thread。普通对话卡与项目任务卡共用同一套 JSON 2.0 外壳，但不提前共享项目语义：无权威项目归属时标题固定为“Codex 对话”；任务形成 KSF 项目投影后，原卡标题才改为项目名，任务完整原名作为次级身份保留，不删除与项目名相同的前缀。显式“我需要/想/要继续完成、推进或处理某项目”仍从 `ksf-panel-catalog-v1` 的 active 项目中精确匹配，只兼容名称末尾可选的“项目”二字，不做模糊猜测；桥在当前卡片绑定的 Thread 中触发 KSF 上下文加载，再用 `ksf-task-project-resolution-v1` 验证项目 ID。普通对话每轮结束后也会回读该 Thread 的同一权威投影，发现 active 项目后原位升级，不分析回复正文猜项目。匹配失败、绑定未形成或卡片交付失败时保留默认对话绑定；已升级的任务连接不允许静默切换项目。桥不直接解析项目卡、不新建项目、不修改 KSF 协议，也不另建项目归属来源。

同一 thread 仅运行一个 turn。App Server daemon 只负责 `thread/read`、历史和内容补读；它不得 `thread/resume` 一个已由 Codex Desktop 加载的连接任务。由于独立只读 App Server 会把尚未写入完成事件的 Desktop 轮次暂时投影为 `interrupted`，桥同时增量读取并监听该 thread 的标准本地 rollout：`task_started` 表示仍在运行，`agent_message` 的 `commentary` 表示可公开进展，`request_user_input` 表示等待普通用户回答，`function_call_output` 表示回答已进入轮次，`task_complete` 与最终回答表示成功，`turn_aborted` 表示真实中断。瞬时投影不得触发失败回传。所有连接任务写操作都通过当前用户私有的 Desktop IPC 路由到该任务的唯一所有者：新轮次使用 `thread-follower-start-turn`，运行中修正使用 `thread-follower-steer-turn`，普通问题回答使用 `thread-follower-submit-user-input`，停止使用 `thread-follower-interrupt-turn`。没有所有者时，桥打开准确的 `codex://threads/<id>` 任务并等待 Desktop 接管，禁止新建或回退到其他 thread。不可 steer 时最多保留一条下一轮消息。

飞书发起的轮次固定使用 `dangerFullAccess` 与 `approvalPolicy: never`，继承原 thread 的模型和推理强度。新轮次表单通过 `select_static(name=turnMode)` 将 `default/plan` 与 `followup` 一次提交，并在既有 `task_link_followup` 回调中附加 `intent=new_turn`；即使客户端省略未改动的 `turnMode`，桥仍使用已保存模式进行同样的终态复核。桥在发送前重新读取 Desktop 权威快照，只有存储状态与观察状态都仍属于 `idle/completed/failed/interrupted` 才应用所选模式。状态竞争、伪造模式或不支持的模式均拒绝整次提交，不得把原本的新轮次内容降级为当前轮 steer。缺少 `turnMode` 的新表单继续沿用已保存的 `nextTurnMode`，旧 `task_link_mode` 回调仅作兼容。Desktop 快照出现未完成 `planImplementation` 时，桥将连接投影为 `plan_ready`，计划正文仅驻留内存，私有连接只保存计划 turn ID 和 SHA-256 截断指纹。卡片在计划正文后显示满宽“开始执行”，随后显示“修改计划”输入和次级“提交修改”，不显示普通模式选择。`task_link_implement_plan` 回调只携带 `taskKey` 与计划指纹；桥重新校验任务、操作者、卡片绑定、权威计划轮次和指纹后，才在原 thread 以默认模式和固定前缀 `PLEASE IMPLEMENT THIS PLAN:` 启动一次新轮次。陈旧、伪造、重复或 Desktop 不可用的请求均不得执行旧计划。

桥重启后若恢复到同一个 `activeTurnId`，必须保留私有连接状态中已经记录的轮次控制来源；只有发现不同的新运行轮次时，才按 Desktop 观察结果切换来源。普通非敏感 `requestUserInput` 从 rollout 恢复问题与工具调用关联；用户提交答案时，桥必须先订阅 Desktop 的当前任务快照，按 turn 和问题 ID 映射到仍然有效的 App Server 请求 ID，再调用 `thread-follower-submit-user-input`。rollout 的 `call_id` 不能替代该请求 ID，Desktop IPC 的传输级成功也不能作为过期请求已回答的证据。每个普通问题最多显示三个预设项；选项按移动端优先的纵向信息行展示，左侧为加粗标题及可选灰色说明，右侧为“选择”按钮。说明只参与展示和卡片预算，回调仍只提交原始 `label`。秘密输入和无法可靠分类的 MCP elicitation 转回 Codex Desktop，秘密问题正文不得进入卡片、日志或连接存储。任务卡自动同步，不再显示手动刷新；公开 commentary、Plan、普通待答问题和终态由桥合并后主动更新原卡片。原始 reasoning、命令、工具参数和秘密不进入卡片。连续事件在 900 ms 窗口内合并，内容指纹相同则不更新；2 秒周期读取只负责丢事件、重启、输入等待过期和终态恢复。运行中输入修正当前轮，终态输入开启新轮次，等待普通问题时输入作为回答。首行右侧只保留红色描边“断开连接”；运行中或等待回答时，`canInterrupt` 的红色描边“停止”位于输入框左侧，和输入框、发送按钮构成同一行，且停止回调不提交表单内容。排队或等待 Desktop 时把“停止”独立放在正文下方；“断开连接”不打断当前本机轮次，终态事件不得复活连接。

默认对话与传统后台任务使用按轮次创建的短生命周期 App Server 客户端。每轮结束后客户端先执行 `thread/unsubscribe`，再关闭本轮传输连接；因此 Thread 会持久保存，但飞书桥不会继续占用它的写入者。用户随后可在 Codex Desktop 中打开并直接输入。项目升级后的后续飞书写操作改走 Desktop IPC，不会重新用独立 App Server 抢占写入权。卡片“断开连接”只释放飞书业务连接，不承担写入者清理；写入者必须在每个飞书轮次的 `finally` 阶段独立释放。

桥只向飞书发送本轮用户消息、阶段性脱敏摘要、结构化普通问题和最终结果，不发送思维链、完整命令、秘密或真实 thread/turn/message/Feishu ID。Desktop 发起的本轮用户消息从对应 rollout 的用户记录读取，剥离附件路径包装，并对疑似密码、Token、密钥内容改为 Desktop 查看提示；只驻留桥进程内存，不写入任务连接存储。连接在 24 小时无交互后过期；运行中、等待飞书普通输入或存在唯一排队消息时不在中途失效，用户消息和卡片操作续期。有效连接或观察中的活动轮次持有 `caffeinate -i`。授权单聊中的图片使用 Codex 本地图片输入，文件、音频和视频使用权限受限的只读暂存路径，单条消息总量最多 25 MiB；终态、失败、解除、过期或重启恢复时清理。群聊控制保持关闭。
项目目录：`/path/to/feishu-bot-bridge`

## 1. 定位

飞书桥是一个本机常驻 Node.js 服务，用来连接飞书自建机器人和本机 Codex。

它负责：

- 通过官方 SDK 单一长连接消费 `lark-cli 1.0.92` 中固定的 23 个非 Approval EventKey，并写入私有事件收件箱。
- 做用户、群聊和路由权限判断。
- 把飞书消息分流到命令模式、任务模式、说话模式。
- 通过 `codex app-server` / `codex app-server proxy` 调用 Codex。
- 把 Codex 的回复、任务状态和错误回传飞书。
- 在授权单聊中安全暂存图片、文件、音频、视频和富文本附件，交给本机 Codex 处理后清理。
- Codex 处理中与终态统一更新原消息卡片；任务卡按“本轮用户消息 → 最新动态回复”的顺序只保留一个权威内容区，以“你 / Codex”角色标签和原生细分隔线区分双方内容。完成、失败和停止状态使用 app-server 轮次时间或 Desktop rollout 起止事件计算真实耗时，并写入加粗首行；桥重启后可重新投影，不依赖卡片进程内存。文件改动数按当前轮 `fileChange.changes[].path` 去重统计，新轮次从零开始；无法取得可靠路径时隐藏该项，不以事件次数或工作树存量猜测。最终回复不会再作为正文和进展重复渲染。卡片成功时不重复发送文字结果，卡片更新失败或回复超过卡片安全预算时才降级为文字回复。
- 普通 Codex 对话卡与任务连接卡共用“标题状态 → 辅助信息 → 你 → Codex → 后续操作”的 JSON 2.0 阅读结构：状态使用标题栏语义色标签，用户消息和 Codex 结果以简短角色标签及原生细分隔线区分。默认卡标题为“Codex 对话”；项目任务卡标题来自已验证的 `projectName`，任务原名降为次级身份。最终卡片按消息更新请求体的 UTF-8 字节数动态适配飞书 30 KiB 上限，并保留 512 bytes 运输余量；完整回复优先，超限时卡片展示可容纳的最大回复前缀并通过分段文字消息交付完整内容，不再用固定字符数静默截断。连接状态只持久化结果摘要；任何终态卡片重绘都必须从 Codex 权威轮次恢复完整回答，摘要不得反向覆盖已经交付的正文。普通对话完成卡使用最长 1000 字的单行快速输入，发送按钮位于输入框右侧，关闭卡片收进顶部原生更多菜单；该表单只接受授权单聊操作者，拒绝空值、超长输入和同卡并发提交。
- CodexAssistant 任务连接卡提供两种明确输入。标题栏 `text_tag_list` 使用蓝、绿、橙、红、灰等语义色显示主进度状态；正文首行不再重复该状态，只保留控制方、Plan、权限、阶段和耗时，终态租约时间位于第二行，右侧以红色描边按钮显示 `task_link_release` 的“断开连接”。卡内短文本是最长 1000 字的单行表单；运行中和等待回答严格按红色描边“停止”→自适应输入框→蓝色“发送”排列，其中停止保持独立 `task_link_interrupt` 回调，不带表单输入。排队与等待 Desktop 没有输入框，停止按钮左对齐放在正文下方。运行中输入不设置 `label`，占位文案为“补充或修正”；普通等待问题仍标为“回答 Codex”。`idle/completed/failed/interrupted` 的表单在左侧紧邻显示“开始新一轮”和 `select_static(name=turnMode)`，`followup` 与 `default/plan` 选择由现有 `task_link_followup` 一次回传；桥重新读取权威状态，仅在存储状态和 Desktop 状态仍为可启动终态时应用所选模式，否则拒绝旧表单且不把内容误送为 steer。`plan_ready` 不显示模式选择，在计划正文后显示唯一的满宽主按钮“开始执行”，再显示“修改计划”输入及次级“提交修改”。Plan 模式的桥侧轮次返回时，在同一任务锁内按 0、250、750 ms 复核 Desktop 权威快照；待执行计划直接投影为 `plan_ready`，不同的新轮次禁止被旧完成态覆盖，暂时不可读时保留安全完成态并交给后台同步。多行文字、图片、文件、音频、视频和富文本使用飞书原生“回复卡片”发送，桥以被引用卡片的消息 ID 精确恢复任务。飞书移动端是否为卡片输入组件打开独立编辑页属于客户端行为，卡片布局不承诺改变该交互。按钮回调卡与任务状态/进度卡共用按任务串行的更新协调器和内容指纹：每次写入先使用原消息 Patch，只有该次 Patch 失败时才在同一队列内使用回调 Token 兜底；禁止两条传输路径并发直写同一卡片。旧版 `task_link_mode`、`task_link_capture` 和刷新动作继续兼容，但不再由新卡片生成。
- 用原位更新的进度卡展示 Codex 处理状态；旧卡片的刷新动作仅保留回调兼容，卡片回调继续执行入站权限判断和审计。
- 通过 outbox 主动发送飞书消息。
- 通过统一 outbox 发送纯文本、Markdown、卡片、图片和文件。
- 在明确群目标、31 天以内时间窗和单页上限内读取或搜索群消息，并读取指定 thread。
- 通过只读通讯录缓存把唯一精确姓名解析为 `open_id`，再提交既有 outbox。
- 通过只读群目录缓存把唯一精确群名解析为 `chat_id`。
- 通过 docbox 编排文档创建任务，并执行既有文档的版本保护更新。
- 通过个人用户身份检索和分范围读取 Docs/Wiki/Drive、日程、任务、电子表格、多维表格、会议、智能纪要和妙记。
- 通过固定能力注册表补齐消息/群聊、通讯录、Docs/Wiki、Whiteboard、Mindnotes、Markdown、评论、日历、任务、Sheets、Base、会议/妙记和妙搭 Apps 的安全子集。
- 通过 actionbox 执行非消息、非 Docx 正文的注册写动作，并对远端 Apps 操作轮询原请求终态。
- 提供 `standup-report` 与 `meeting-summary` 两个只读组合工作流，默认不发布。
- 写入 JSONL 日志和项目内 Markdown 审计页。

它不负责：

- 直接保存 App Secret 到代码。
- 绕过 outbox、逐次用户授权或审计链路主动发消息。
- 绕过 docbox 版本保护更新既有飞书文档。
- 提供任意 OpenAPI 转发、权限修改、删除、Wiki 移动或后台全量消息抓取。
- 控制机器人入会/离会/结束会议、会中邀请/消息/截图/倒计时，或下载妙记原始媒体。
- 作为上层业务系统、任务系统或审批系统。

## 2. 关键文件

```text
bot-bridge.js
主程序。包含官方 SDK 入站事件编排、权限判断、路由、Codex app-server 调用、日志和审计。

lib/official-event-adapter.js
唯一入站长连接适配层。复用官方 SDK 的握手、重连、分片和事件分发，并为 SDK 尚未分发的 card 数据帧提供隔离兼容覆盖。

lib/lark-cli-credentials.js
只在内存中解析平台安全凭据，供官方 SDK 建立长连接；macOS 复用 lark-cli/Keychain，Windows 使用当前用户 DPAPI 文件，不写明文凭据。

lib/lark-cli-runner.js
lark-cli 执行层。统一封装消息发送与受限读取、文档/Drive/Wiki 读取、私有 payload 文件、评论、文档更新和官方版本管理调用。

lib/queue-worker-core.js
outbox、docbox、actionbox 共享的 JSONL 队列状态、完整行读取和结果存储核心；状态采用 schema v2、原子私有写入和有界去重保留。

lib/runtime-manifest.js / lib/capability-policy.js
统一版本、能力、事件、动作和最小权限矩阵；`bridge-client capabilities|permissions` 从这里输出。

lib/inbound-media.js / lib/inbound-events.js / lib/progress-card.js
入站附件安全暂存、统一事件归一化、受控卡片动作和进度卡构造。

lib/task-link-store.js / lib/codex-task-control.js / lib/codex-desktop-ipc.js / lib/codex-desktop-turn-journal.js
Codex 任务连接 v2 私有状态、24 小时租约、v1 迁移、公开能力与进展投影、权威 thread/turn 解析、rollout 增量监听、终态结果提取、附件输入构造，以及把连接任务写操作定向交给 Codex Desktop 任务所有者的私有 IPC 适配。

lib/action-registry.js
actionbox 兼容注册表，并把 1.0.0 的 `feishu_capability` 请求交给固定能力注册表验证。

lib/capability-registry.js / lib/lark-cli-flag-snapshot.js
1.0.0 固定能力定义与 `lark-cli 1.0.92` 参数快照。每项能力声明身份、风险、输入、范围、预检、复读和脱敏；依赖升级不会自动扩张能力。

lib/capability-executor.js
执行固定 shortcut 或固定原始端点；正文通过 stdin/私有文件传递，高影响写入执行预检与复读，远端操作使用固定读能力轮询。

lib/event-inbox.js / lib/event-subscriptions.js
23 个非 Approval EventKey、私有按日 JSONL 收件箱、去重与类型化 Task/Whiteboard/VC/Minutes 订阅。

lib/work-actions.js
工作数据写动作执行层。负责读取前检查、固定 lark-cli shortcut、结构化 payload 隔离和必要复读。

lib/contact-directory.js
只读通讯录同步、分页去重、安全缓存、精确姓名解析和同名绑定规则。

scripts/start-bridge.js
跨平台启动包装器。读取 `.env.local` 和环境变量，补齐平台私有目录与 lark-cli 入口，并持有对应平台的休眠抑制。

scripts/bridge-client.js
统一客户端。封装桥状态、LaunchAgent/Windows 计划任务管理、目标别名、富消息发送、有边界的消息/知识/会议/智能纪要/妙记读取、docbox、actionbox、结果查询和审计读取。

launchd/com.example.feishu-bot-bridge.plist
macOS LaunchAgent 配置。用于开机或登录后常驻运行。

windows/*.ps1
Windows 用户级计划任务、DPAPI、ACL、休眠抑制和 Codex 深链固定适配器。

.env.example
环境变量样例，不包含真实密钥。

.env.local
本机私有环境变量文件。启动包装器会读取该文件；该文件被 `.gitignore` 忽略，不进入版本管理。

docs/feishu-codex-integration.md
历史接入说明和飞书权限、文档读写测试记录。

docs/configuration.md
配置文件说明、LaunchAgent 模板使用方式和同事复刻检查清单。

docs/bridge-client.md
统一客户端命令、授权边界、同步与异步结果处理以及安全约束。

docs/feishu-bridge-technical-spec.md
当前文档，供其他项目集成和 AI 接手使用。

docs/feishu-bridge-implementation-guide.md
系统实现与复刻指南，供同事创建同类系统和 AI 快速理解实现全貌。
```

运行日志目录：

```text
logs/messages.jsonl
logs/tasks.jsonl
logs/sessions.json
logs/outbox.jsonl
logs/outbox-results.jsonl
logs/outbox-state.json
logs/docbox.jsonl
logs/docbox-results.jsonl
logs/docbox-state.json
logs/actionbox.jsonl
logs/actionbox-results.jsonl
logs/actionbox-state.json
logs/bridge.pid

~/.config/feishu-bridge/directory.json
应用可见通讯录的安全缓存，不进入项目日志或仓库。
logs/task-runs/<task_id>.log
```

默认项目内审计页：

```text
logs/audit/YYYY-MM-DD.md
```

## 3. 运行架构

```text
飞书用户/群
  -> 飞书自建机器人
  -> 官方 SDK 单一长连接
     -> im.message.receive_v1 / card.action.trigger
  -> feishu-bot-bridge
     -> 去重
     -> 权限判断
     -> 路由分流
     -> Codex app-server / proxy
     -> 日志与审计
  -> 飞书消息回复
```

出站消息链路：

```text
本机调用方
  -> append logs/outbox.jsonl
  -> POST 127.0.0.1:<wake-port>/internal/outbox/wake
  -> feishu-bot-bridge
     -> 读取 outbox
     -> 校验请求结构与显式目标
     -> dry-run 或飞书发送
     -> 写 outbox-results / messages / 项目内审计

如果 wake 未调用或失败：
  -> polling fallback 定时消费 outbox
```

消息、知识、日程、任务、结构化数据、会议、智能纪要和妙记读取不经过写队列，但必须经过统一客户端的目标、时间窗、数量、区域、字段投影或逐字稿字符上限和输出脱敏策略。消息读取固定机器人身份，其余读取固定个人用户身份，不使用无界 `--page-all`。

文档类写入采用位置型身份路由：个人云空间目标固定为 `user`；`wiki_url`、`wiki_token`、Wiki 节点创建/复制及桥管理的 Wiki Mindnote 固定为 `bot`。客户端写入请求显式携带计算后的 identity，队列再次校验目标位置与 identity，执行器只选择对应 runner；任何权限错误都失败关闭，不回退到另一身份。历史资源不因规则升级自动变更所有者。

飞书受控动作写入链路：

```text
本机调用方
  -> append logs/actionbox.jsonl
  -> polling fallback
  -> exact registry / user identity
  -> lark-cli typed shortcuts
     -> 日程更新先读目标
     -> Sheets 写入先读区域与 revision，写后复读
     -> Base 写入先验字段；更新记录写前、写后均读取目标
     -> 妙记标题、总结和待办修改先读后写再复读；关键词和说话人使用受控结果验证
     -> 大型结构化 payload 走权限受限临时文件并在终态前清理
  -> actionbox-results / messages / Markdown 审计
```

Codex 调用链路：

```text
feishu-bot-bridge
  -> codex app-server proxy
     -> 本机 app-server daemon
     -> 只读 thread/turn 权威状态与普通飞书会话

CodexAssistant 已连接任务的写操作
  -> 当前用户私有 Codex Desktop IPC
  -> 目标任务的唯一 Desktop 所有者
  -> start / steer / interrupt 原任务

如果 proxy 不可用：
  -> 自动尝试 codex app-server daemon start
  -> 再试 proxy
  -> 仍失败则回落到独立 codex app-server
```

推荐保持：

```text
CODEX_TRANSPORT=auto
CODEX_AUTO_START_DAEMON=true
CODEX_CLIENT_NAME=codex_vscode
CODEX_CLIENT_TITLE=Codex
```

原因：

- `proxy` 最接近 Codex Desktop / VSCode 插件的官方本地会话链路。
- 已加载任务存在单写入者约束；因此连接任务只经 daemon 读取，经 Desktop IPC 写入，禁止让独立 daemon 与 Desktop 争抢同一任务。
- 回落独立 `app-server` 时飞书仍可用，但 Desktop UI 的实时完成状态可能只靠本地状态补读，不如 proxy 稳定。
- `codex_vscode` 已验证能让新建 thread 更容易出现在 Desktop UI。
- `CODEX_BIN` 指向的 CLI 必须支持当前 Codex 配置选择的模型；出现 `requires a newer version of Codex` 时，应更新该本机路径，而不是静默降级模型。
- 如果本机 managed daemon 仍固定在旧版本，允许临时设置 `CODEX_TRANSPORT=app-server`，用 `CODEX_BIN` 直接启动独立 app-server；daemon 升级后再恢复 `auto`。

## 4. 环境变量

必填：

```text
LARK_CLI_BIN=./node_modules/.bin/lark-cli
LARK_CLI_PROFILE=default
LARK_CLI_AS=bot
FEISHU_EVENT_CONSUMER_ENABLED=true
FEISHU_EVENT_TRANSPORT=official-sdk
FEISHU_EVENT_PROFILE_POLL_MS=1000
FEISHU_EVENT_KEYS=<copy the frozen 23-key non-Approval catalog from .env.example>
FEISHU_INBOUND_MAX_BYTES=26214400
```

常用：

```text
FEISHU_BRIDGE_LOG_DIR=./logs
FEISHU_QUEUE_MAX_PROCESSED_IDS=5000
FEISHU_QUEUE_WARN_BYTES=104857600
KMS_ROOT=<KNOWLEDGE_WORKSPACE_ROOT>
KSF_PROJECT_ROOT=<KSF_ROOT>
FEISHU_AUDIT_DIR=./logs/audit

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

FEISHU_ACTIONBOX_ENABLED=false
FEISHU_ACTIONBOX_PATH=./logs/actionbox.jsonl
FEISHU_ACTIONBOX_POLL_MS=3000
FEISHU_ACTIONBOX_DRY_RUN=true
FEISHU_ACTIONBOX_ALLOWED_SOURCES=local,codex
FEISHU_ACTIONBOX_TIMEOUT_MS=120000

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

启动包装器 `scripts/start-bridge.js` 会先读取项目根目录 `.env.local`，再叠加 launchd 或 shell 注入的环境变量；显式环境变量优先级高于 `.env.local`。真实 `chat_id`、`open_id`、secret 等本机配置应写在 `.env.local`，不要写入 plist、文档或代码。

权限变量说明：

- `FEISHU_EVENT_KEYS`：只允许已审查的消息与卡片回调事件；其他事件不会启动消费者。
- `FEISHU_EVENT_PROFILE_POLL_MS`：轮询平台私有 `event-consumer-profile.json` 的间隔。档位只允许 `primary` 和 `manual-only`，修改后无需重启桥。
- `FEISHU_INBOUND_MAX_BYTES`：单条入站消息附件的总暂存上限，默认 25 MiB；只处理授权单聊，处理完成或失败即清理。
- `FEISHU_QUEUE_MAX_PROCESSED_IDS`：每个队列状态文件保留的最近去重 ID 上限，默认 5000。
- `FEISHU_QUEUE_WARN_BYTES`：队列或结果 JSONL 达到该体积时由 doctor 发出 warning，不自动删改历史。
- `FEISHU_DIRECT_ALLOWED_OPEN_IDS`：单聊白名单。单聊必须命中白名单才响应。
- `FEISHU_GROUP_ENABLED`：是否启用群聊响应。
- `FEISHU_GROUP_ALLOWED_CHAT_IDS`：群聊白名单。空值表示允许所有群。
- `FEISHU_GROUP_ALLOWED_OPEN_IDS`：群内用户白名单。空值表示允许所有用户。
- `FEISHU_OUTBOUND_ENABLED`：是否启用 outbox 出站消费。
- `FEISHU_OUTBOUND_DRY_RUN`：只写日志和审计，不实际发送。
- `FEISHU_OUTBOUND_WAKE_ENABLED`：是否启用本地 wake 接口。
- `FEISHU_OUTBOUND_WAKE_HOST`：wake 绑定地址，必须为 `127.0.0.1`。
- `FEISHU_OUTBOUND_WAKE_PORT`：wake 端口，`0` 表示系统分配可用端口。
- `FEISHU_DIRECTORY_ENABLED`：是否启用全量可见通讯录只读缓存与按姓名发送。
- `FEISHU_DIRECTORY_REFRESH_MS`：后台刷新间隔，默认 6 小时。
- `FEISHU_DIRECTORY_MAX_AGE_MS`：按姓名发送允许的最大缓存年龄，默认 24 小时。
- `FEISHU_DIRECTORY_MIN_USER_COUNT`：拒绝用过小同步结果覆盖成功缓存的下限。
- `FEISHU_GROUP_DIRECTORY_ENABLED`：是否启用机器人已加入群的只读缓存与按唯一群名发送。
- `FEISHU_GROUP_DIRECTORY_REFRESH_MS`：桥进程内群目录定时刷新间隔，默认 30 分钟；启动时会立即同步一次。
- `FEISHU_GROUP_DIRECTORY_MAX_AGE_MS`：按群名发送允许的最大缓存年龄，默认 2 小时。
- `FEISHU_GROUP_DIRECTORY_PAGE_SIZE`：群列表分页大小，范围 1–100。
- `FEISHU_DOCBOX_ENABLED`：是否启用文档任务 docbox。
- `FEISHU_DOCBOX_DRY_RUN`：只记录任务意图；不创建版本、不创建或编辑文档。
- `FEISHU_DOCBOX_ALLOWED_SOURCES`：允许写入 docbox 的调用方 source。

生产建议：

- 单聊必须配置明确 open_id。
- 群聊上线前先限定 chat_id。
- 群聊默认只开放 help 和 cmd 类基础命令，不开放说话模式和 task 模式。
- 出站目标不设本地白名单；上线前必须先开启 dry-run，并只对用户明确授权的目标做真实验证。
- 卡片回调除 `im:message:readonly` 外，还必须在飞书开放平台启用 `card.action.trigger` 回调。官方 SDK 长连接在回调窗口内先返回轻量 toast，既有 `handleCardAction` 再异步执行，避免慢业务处理触发平台 `200671`。
- 入站只允许一个官方 SDK 长连接。`lark-cli` 继续承担所有飞书 API 与出站调用，不启动并行 `lark-cli event consume`。
- 同一飞书应用跨机器部署时只允许一个实例处于 `primary`；其他实例使用 `manual-only`。飞书长连接为竞争消费，不能用本地过滤可靠拆分默认消息与卡片回调，因此不提供 `cards-only`。档位切换只启停入站适配器，不改变消息、卡片、队列或能力注册表业务逻辑。
- `@larksuiteoapi/node-sdk 1.73.0` 尚未在公开分发路径处理 `card` 数据帧，因此 `lib/official-event-adapter.js` 只覆盖这一处数据帧入口。SDK 内部钩子变化时桥会拒绝启动并要求复核；上游原生支持后删除该覆盖。

## 5. 飞书权限

飞书桥本身依赖机器人消息能力和基础群信息能力：

```text
im:message:send_as_bot
im:message:readonly
im:message.p2p_msg:readonly
im:message.group_at_msg:readonly
im:chat:readonly
contact:user.base:readonly
contact:user.id:readonly
```

启用全量通讯录缓存时，应用可见范围必须覆盖全组织，并增加只读通讯录权限。推荐使用 `contact:contact:readonly_as_app`；如果控制台采用细分权限，至少覆盖用户基础信息、用户部门关系、部门基础信息和用户 ID。不要授予通讯录写权限，也不需要手机号或邮箱读取权限。

启用群目录缓存使用既有 `im:chat:readonly`，只分页读取机器人已经加入的群；不读取群成员，不修改群资料，也不搜索未加入的公开群。

事件订阅：

```text
im.message.receive_v1
card.action.trigger
```

`im:message:readonly` 同时用于下载入站消息资源和读取卡片原消息内容。卡片事件除订阅外还必须在开放平台启用回调配置。

事件通道：

```text
@larksuiteoapi/node-sdk 单一长连接
```

桥从 lark-cli 安全 profile 在内存中解析凭据；不要求在 `.env.local` 重复保存 App Secret。只有同时显式提供 `FEISHU_APP_ID` 与 `FEISHU_APP_SECRET` 时才采用环境变量覆盖，缺一项会 fail closed。

飞书文档能力统一通过固定 lark-cli 适配器执行。创建文档使用 `docs +create → fetch`，正文只走 stdin；更新既有文档由飞书桥执行基础版本保护更新。文档相关常用权限见 `docs/feishu-codex-integration.md`。

会议、智能纪要和妙记使用个人用户身份，所需 OAuth scopes 为：

```text
vc:meeting.search:read
vc:meeting
vc:meeting.meetingevent:read
vc:record:readonly
vc:note:read
minutes:minutes.search:read
minutes:minutes.basic:read
minutes:minutes:readonly
minutes:minutes:update
minutes:minutes.upload:write
```

`vc:meeting` 是当前飞书控制台提供的会议基础权限；旧权限码 `vc:meeting:readonly` 已不再可搜索，不得继续请求。应用后台权限和用户 OAuth 必须同时覆盖并发布。桥只接入会议查询、智能纪要/妙记读取和受控妙记编辑；即使用户令牌持有较宽的会议权限，实时会议控制、妙记原始媒体下载、权限变更与删除仍排除。

## 6. 消息模式

项目内统一使用三个业务分流名称：

```text
命令模式
任务模式
说话模式
```

### 命令模式

触发：

```text
help
cmd
cmd <命令>
cmd命令
```

行为：

- 不调用 Codex。
- 由飞书桥本地立即处理。
- 适合状态查询、任务查询、身份查询、审计查询。

常用命令：

```text
cmd ping
cmd help
cmd id
cmd whoami
cmd 群信息
cmd 成员列表
cmd 状态
cmd 最近消息
cmd 最近任务
cmd 最近出站
cmd 出站 <OUT-id>
cmd 最近文档
cmd 文档 <DOC-id>
cmd 任务 <task_id>
cmd 审计 今天
cmd echo <文本>
```

群聊当前只建议开放基础命令：

```text
help
cmd ping
cmd id
cmd whoami
cmd 群信息
cmd 状态
```

### 任务模式

触发：

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
- 等待、任务已创建、任务完成和任务失败分别使用独立且稳定的飞书幂等键；同一阶段重试保持幂等，不同阶段不得互相去重。

任务 ID 示例：

```text
TASK-20260527132533-7041
```

任务查询：

```text
task任务
task 任务
cmd 最近任务
cmd 任务 <task_id>
```

### 继续任务

触发方式 1：

```text
task继续1 <追加要求>
task继续<TASK_ID> <追加要求>
```

触发方式 2：

在飞书里回复某条任务相关机器人消息，直接输入追加要求。

行为：

- 使用父任务的 `threadId` 作为 `resumeThreadId`。
- 新建一条子任务记录。
- Codex prompt 只发送本次追加要求，不再嵌套“继续任务/原任务描述/追加要求”。
- 日志保留 `parentTaskId`、`resumeThreadId`、`parentDescription`，用于审计和追踪。

### 说话模式

触发：

```text
不以 help / cmd / task / task继续 开头的普通文本
```

行为：

- 立即回复“等待Codex响应中”。
- 每条未引用卡片的根消息创建独立 Codex Thread，不复用全局会话。
- 卡内追问和引用回复通过私有消息绑定，只恢复对应卡片的 Thread。
- 同一张卡的后续轮次串行，不同默认卡片可以并发执行。
- 默认 Thread 标题为 `CODEX_FEISHU_DEFAULT_THREAD_TITLE`。
- `CODEX_FEISHU_DEFAULT_SESSION` 仅作为旧版 `sessions.json` 指针的迁移清理键。
- 等待、最终回答和失败通知分别使用独立且稳定的飞书幂等键，避免第一条等待提示吞掉后续结果。

说话模式中的明确项目延续意图会进入“项目升级”子路由，例如：

```text
我需要继续完成飞书桥项目
```

显式延续的处理顺序固定为：读取 KSF active 项目目录并精确匹配 → 在当前卡片绑定的 Codex Thread 加载项目上下文 → 回读并验证该 Thread 的 KSF 项目投影 → 只把原卡升级为项目任务控制卡 → 将该默认对话绑定标记为已升级。普通对话轮次结束后也执行只读投影检查；只有同一 Thread 的最新绑定能在 active 项目目录中精确回源时才自动升级。成功后标题使用项目名，任务原名作为次级身份，后续内容从升级后的任务卡继续；其他默认卡片和下一条根消息完全独立。该机制只连接既有项目，不创建项目，也不允许已升级任务静默改绑；任一步失败都保留当前卡片的默认对话绑定。

## 7. 权限策略

单聊：

```text
只响应 FEISHU_DIRECT_ALLOWED_OPEN_IDS 中的用户。
未授权用户会收到“当前机器人单聊只响应授权用户。”
```

群聊：

```text
FEISHU_GROUP_ENABLED=false 时完全不响应。
FEISHU_GROUP_ENABLED=true 时只响应被 @ 的机器人消息事件。
可通过 FEISHU_GROUP_ALLOWED_CHAT_IDS 限定群。
可通过 FEISHU_GROUP_ALLOWED_OPEN_IDS 限定群内用户。
默认群聊不开放说话模式和任务模式。
```

安全原则：

- 群聊能力逐步放开。
- 写入、移动、改权限类操作不在群里默认开放。
- Codex 不应直接调用飞书发消息工具给用户或群发消息，统一由飞书桥回传。

## 8. 日志与审计

### messages.jsonl

记录飞书消息收发、授权、路由、错误、Codex 回复等事件。

常见 `direction`：

```text
inbound
authz
accepted
outbound
codex_reply
duplicate_ignored
ignored
error
codex_transport_started
codex_transport_failed
codex_daemon_start
outbound_outbox_result
outbox_error
default_session_rebuild
session_resume_failed
```

`direction=error` 可附带 `errorReplyIds` 和 `errorReplyError`，用于区分“Codex 处理失败”和“失败通知回传失败”。

入站文本如果包含 `[TRACE:<code>]`，`messages.jsonl` 会记录：

```json
{
  "trace": {
    "code": "REQ-20260530-000001"
  }
}
```

入站文本如果是明确 key-value 格式，会额外记录 `parsed.fields`。飞书桥只做通用解析，不解释字段业务含义。

### tasks.jsonl

任务事件日志。一个任务会有多行事件，通过 `id` 合并。

常见字段：

```json
{
  "id": "TASK-...",
  "status": "running|completed|failed",
  "description": "任务描述",
  "instruction": "干净的用户要求",
  "parentTaskId": "TASK-...",
  "resumeThreadId": "019e...",
  "threadId": "019e...",
  "runLogPath": "...",
  "result": "最终结果",
  "error": "错误堆栈"
}
```

### default-conversations-v1.json

保存在私有数据根中的默认卡片—Thread 绑定：

```json
{
  "protocol": "codex-feishu-default-conversation-v1",
  "schemaVersion": 1,
  "conversations": [{
    "id": "CHAT-...",
    "threadId": "019e...",
    "rootMessageId": "om_...",
    "state": "active"
  }]
}
```

实际记录还包含工作目录、授权单聊、唯一操作者和关联消息 ID，仅保存在权限为 `0600` 的私有文件中，不通过公开状态接口返回。旧 `sessions.json` 中 `CODEX_FEISHU_DEFAULT_SESSION` 对应的全局指针会在启动时删除，但不会删除原 Codex Thread。

### task-runs

每个后台任务一个日志文件：

```text
logs/task-runs/TASK-xxxx.log
```

内容是 app-server 通知事件 JSONL，包括：

```text
item/agentMessage/delta
item/completed
thread/status/changed
turn/completed
```

### Markdown 审计页

每日生成一个 Markdown 文件，用于人读和 AI 检索：

```text
logs/audit/YYYY-MM-DD.md
```

### outbox-results.jsonl

记录 outbox 消费结果。状态枚举：

```text
dry_run
sent
partial_sent
denied
failed
duplicate
invalid
```

示例：

```json
{
  "id": "OUT-20260530123000-ABCD",
  "status": "sent",
  "target": {
    "type": "chat_id",
    "id": "oc_xxx"
  },
  "messageIds": ["om_xxx"],
  "trace": {
    "code": "REQ-20260530-000001"
  },
  "sentAt": "2026-05-30T12:30:03.000+08:00"
}
```

### outbox-state.json

记录 outbox worker 的本地状态，包括已处理行数、已处理 ID、最近处理时间、最近错误和 wake server 实际端口。

### docbox-results.jsonl

记录 docbox 文档任务编排结果。状态枚举：

```text
accepted
completed
dry_run
denied
failed
duplicate
invalid
```

### docbox-state.json

记录 docbox worker 的本地状态，包括已处理行数、已处理 ID、最近处理时间、最近错误和 wake server 实际端口。

### bridge.pid

记录当前桥进程 PID 和启动时间，用于防止多个 `bot-bridge.js` 实例同时消费 outbox/docbox 或重复接收事件。正常退出时会自动清理；如果进程被强制杀死，启动时会检查旧 PID 是否仍存活，死进程的锁会被自动替换。

## 9. 主动发飞书消息的 outbox 链路

其他项目要主动发飞书消息时，不直接调用飞书 OpenAPI，而是使用 outbox：

```text
调用方
  -> append 完整单行 JSON 到 logs/outbox.jsonl
  -> POST http://127.0.0.1:<wake-port>/internal/outbox/wake
  -> 查询 logs/outbox-results.jsonl 获取结果
```

`outbox.jsonl` 是唯一出站事实队列。wake 接口只负责唤醒 worker，不接收业务消息体，不代表发送成功。wake 失败时，polling fallback 仍会消费 outbox。

出站请求结构：

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
    "system": "calling-system-name",
    "code": "REQ-20260530-000001",
    "objectType": "collaboration_request",
    "objectId": "xxx"
  },
  "createdAt": "2026-05-30T12:30:00.000+08:00"
}
```

字段规则：

- `id` 必填且全局唯一。
- `type` 第一版只支持 `text`。
- `target.type` 第一版只支持 `chat_id` 和 `open_id`。
- outbox 内的 `target.id` 仍必须是已解析的显式 ID。统一客户端可在入队前用安全通讯录缓存解析唯一精确姓名，或用安全群目录缓存解析机器人已加入群中的唯一精确群名；同名或模糊匹配不会入队。
- `source` 必填。
- `trace` 可选，飞书桥只原样记录和透传。

### 发送操作方法

1. 在 `.env.local` 中启用出站能力：

```text
FEISHU_OUTBOUND_ENABLED=true
FEISHU_OUTBOUND_DRY_RUN=false
FEISHU_OUTBOUND_WAKE_ENABLED=true
FEISHU_OUTBOUND_WAKE_HOST=127.0.0.1
FEISHU_OUTBOUND_WAKE_PORT=0
```

2. 重启飞书桥：

```bash
launchctl kickstart -k gui/$(id -u)/com.example.feishu-bot-bridge
```

3. 获取 wake 实际端口：

```bash
cd /path/to/feishu-bot-bridge
node -e 'const fs=require("fs"); const s=JSON.parse(fs.readFileSync("logs/outbox-state.json","utf8")); console.log(s.wake.actualPort)'
```

4. 写入一条 outbox 请求。发送到单聊或群聊都使用 `target.type: "chat_id"`；发送到用户 open_id 时使用 `target.type: "open_id"`。

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

5. 唤醒 outbox worker：

```bash
port=$(node -e 'const fs=require("fs"); const s=JSON.parse(fs.readFileSync("logs/outbox-state.json","utf8")); console.log(s.wake.actualPort)')
curl -X POST "http://127.0.0.1:${port}/internal/outbox/wake"
```

6. 查看发送结果：

```bash
tail -n 10 logs/outbox-results.jsonl
tail -n 10 logs/messages.jsonl
```

成功结果应包含：

```json
{
  "status": "sent",
  "messageIds": ["om_xxx"]
}
```

常见状态：

- `sent`：已真实发送。
- `dry_run`：只记录日志，未真实发送；可能来自 `FEISHU_OUTBOUND_DRY_RUN`，也可能来自单次请求的 `dryRun: true`（统一客户端 `--dry-run`）。
- `denied`：为兼容历史结果保留的终态；当前出站目标策略不再因本地白名单产生该状态。
- `duplicate`：`id` 已处理，不会重复发送。
- `invalid`：outbox 请求结构或 JSON 行不合法。
- `failed` / `partial_sent`：飞书 API 或分段发送失败，看 `error` 字段。

这样可以保持安全边界：调用方必须明确提出“发什么、发给谁”，飞书桥负责结构校验、发送、去重和审计；目标是否可达由飞书平台权限决定。

## 10. 飞书文档任务的 docbox 链路

其他项目要创建或编辑飞书文档时，不直接调用飞书 OpenAPI，而是使用 docbox。飞书桥只负责编排、权限、状态、日志、审计和唤醒；文档相关能力统一通过 `lark-cli` 执行。

```text
调用方
  -> append 完整单行 JSON 到 logs/docbox.jsonl
  -> POST http://127.0.0.1:<docbox-wake-port>/internal/docbox/wake
  -> 查询 logs/docbox-results.jsonl 获取任务结果
```

第一版只支持 `type: "document_task"`，动作支持 `create_document` 和 `update_document`。

- `create_document`：固定执行 `lark-cli docs +create`，再以 `docs +fetch` 复读；不启动自由 Codex 任务，公开结果只保留脱敏后的标题、URL 指纹、token 指纹和摘要。
- `update_document`：由飞书桥通过 `lib/lark-cli-runner.js` 执行基础更新链路。默认采用 `versionPolicy: "official_before_update"`，必须先创建飞书官方文档版本再编辑；版本创建失败时不得继续写入。

`FEISHU_DOCBOX_DRY_RUN=true` 时只记录任务意图，不创建版本、不创建或编辑文档。

docx 官方版本管理固定使用 raw API：

```text
POST /open-apis/drive/v1/files/:file_token/versions
GET  /open-apis/drive/v1/files/:file_token/versions
```

请求体或参数必须包含 `obj_type: "docx"`。`drive +version-history` shortcut 不作为当前 docx token 的版本查询依据。

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

`versionPolicy` 可省略。对 `update_document`，省略时默认按 `official_before_update` 执行。第一版不提供默认跳过版本的路径。

`updateMode` 可省略，默认 `append`。第一版支持：

- `append`：追加到文档末尾，推荐默认测试模式。
- `overwrite`：全文覆盖，属于高影响操作，应只用于明确授权的测试或迁移。
- `str_replace`：按 `lark-cli 1.0.92` 的 `--pattern` 契约执行字符串替换，调用方必须用 `--pattern-file` 提供唯一精确旧文本。该模式容易误匹配，优先用于测试文档或明确授权的局部替换。

常见状态：

- `accepted`：飞书桥已接受请求，开始执行文档任务。
- `completed`：文档任务已完成。创建文档表示固定创建与复读均完成；更新文档表示官方版本创建、正文更新和复读均完成。
- `dry_run`：全局或请求级 dry-run 只记录任务意图，不创建版本、不编辑文档。actionbox 同样接受请求级 dry-run，并跳过预检和写入执行器。
- `denied`：source 未命中 `FEISHU_DOCBOX_ALLOWED_SOURCES`。
- `duplicate`：`id` 已处理，不会重复执行。
- `invalid`：docbox 请求结构或 JSON 行不合法。
- `failed`：权限、目标解析、版本创建、文档更新或 Codex 创建任务失败，看 `error` 和 run log。

`update_document` 的 `completed` / `failed` 结果会记录 `document`、revision 和 `version` 字段。成功时应包含：

```json
{
  "document": {
    "url": "<FEISHU_DOCX_OR_WIKI_URL>",
    "token": "docx_token",
    "objType": "docx"
  },
  "beforeRevisionId": "123",
  "afterRevisionId": "124",
  "version": {
    "policy": "official_before_update",
    "created": true,
    "versionId": "xxx",
    "versionName": "DOC-20260601-xxx 前置版本",
    "createdAt": "2026-06-01T12:40:00.000+08:00"
  }
}
```

版本创建失败或 Codex 未确认版本已创建时，结果为 `failed`，并包含：

```json
{
  "status": "failed",
  "error": "document_version_create_failed",
  "version": {
    "policy": "official_before_update",
    "created": false
  }
}
```

## 11. 启动与运维

手动启动：

```bash
cd /path/to/feishu-bot-bridge
npm run start:awake
```

LaunchAgent 启动：

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
tail -f /path/to/feishu-bot-bridge/logs/messages.jsonl
tail -f /path/to/feishu-bot-bridge/logs/tasks.jsonl
```

检查 Codex daemon：

```bash
codex app-server daemon version
ls -la ~/.codex/app-server-control
```

重启 daemon：

```bash
codex app-server daemon stop
codex app-server daemon start
```

## 12. 对其他项目的复用建议

本机脚本、Codex Skill 或其他项目调用飞书桥时，优先使用统一客户端：

```bash
node /path/to/feishu-bot-bridge/scripts/bridge-client.js --help
```

客户端负责安全配置、目标别名、请求 ID、JSONL 并发追加、wake fallback、终态等待和默认脱敏。完整用法见 `docs/bridge-client.md`。

如果另一个项目只想发起任务：

1. 不要直接操作 `tasks.jsonl`。
2. 优先通过 outbox 发协同消息，或另行设计明确的任务接口。
3. 让飞书桥统一做权限、发送、审计和去重。

如果另一个项目只想读取任务状态：

1. 可以只读 `logs/tasks.jsonl`。
2. 按 `id` 合并多行事件。
3. 以最后状态为准。

如果另一个项目想继续某个任务：

1. 使用父任务的 `threadId`。
2. 新建子任务记录。
3. Codex prompt 只发送本次追加要求。
4. 日志中保留 `parentTaskId` 和 `resumeThreadId`。

如果另一个项目想复用飞书权限：

1. 不要复制 App Secret 到新项目。
2. 通过环境变量或本机安全配置注入。
3. 尽量把飞书发送能力集中留在飞书桥。

如果另一个项目想创建或编辑飞书文档：

1. 不要直接调用飞书文档 OpenAPI。
2. 通过 docbox 提交 `document_task` 请求。
3. 创建文档时由飞书桥执行固定 `docs +create → fetch`；更新文档时由飞书桥执行版本保护更新。
4. 以 `docbox-results.jsonl` 为执行结果来源。

## 13. 当前限制

- 当前没有承载业务消息体的 HTTP API；三个 wake 接口仅唤醒对应队列，不接收业务正文。
- outbox 支持文本、Markdown、卡片、图片和文件；目标可由显式 ID、本机别名、唯一姓名或唯一群名解析。
- 当前 docbox 第一版只支持文档创建和基础编辑任务；不执行复杂局部块编辑、批注、权限修改或 Wiki 节点移动。
- 当前会议与妙记保留安全读写和固定事件，不提供机器人现场控制、妙记原始媒体下载、权限变更或删除。
- 当前群聊不开放 task 和说话模式。
- 授权单聊可处理图片、文件、音频、视频和富文本；群聊附件仍不交给 Codex。
- 当前日志是 JSONL，不是 SQLite；并发写入规模大时需要后续升级。
- Desktop UI 实时同步依赖 `codex app-server proxy` 和本机 daemon 状态；回落独立 app-server 时可能出现 UI 延迟补齐。
