# 架构说明

## 定位与总体结构

KSFAssistant（知识－技能－飞轮－助手）是本地多进程桌面应用：两个平台宿主共享一个 Go 业务核心，核心再管理一个 Go 飞书子进程。它不是需要独立部署后端的云端微服务系统；Codex 与飞书是外部平台，KSF 是可选的外部上下文来源。

本文描述当前源码的职责、数据流和运行边界，不把产品名称表达的长期方向当作已经实现的能力。当前产品可以概括为 Codex 工作台、飞书执行桥和 KSF 项目入口，而非完整的 KSF 知识—技能—飞轮运行系统。

```text
macOS：SwiftUI / AppKit                 Windows：Electron
菜单栏、设置、原生系统操作               托盘、页面、原生系统操作
              │                              │
              └──── JSON-RPC / stdin/stdout ──┘
                               │
                   Go Core：统一业务编排与状态聚合
                     ├─ Codex App Server：额度、任务目录与草稿
                     ├─ Codex Desktop IPC：任务状态与控制
                     ├─ 本地会话记录：Token、历史、费用估算
                     ├─ KSF 外部桥：项目目录与任务归属投影
                     ├─ integration：任务关联、动作解释与业务卡片
                     ├─ 本机网关：原生 CLI 的受限请求入口
                     ├─ userapproval：用户身份写操作的单次桌面批准
                     └─ v2 匿名管道 ─ Go 飞书子进程
                                         ├─ 消息、卡片、事件与授权
                                         ├─ 执行治理、工作队列与审计
                                         └─ 随包 lark-cli → 飞书
```

## 代码地图

以下路径均相对于代码仓库根目录；源码负责实现事实，本文负责解释模块之间的关系。

| 目录或入口 | 职责 |
| --- | --- |
| [Sources/KSFAssistant](../Sources/KSFAssistant/) | macOS 宿主。`KSFAssistantApp` 管理应用入口和退出；`UsageViewModel` 管理界面状态与刷新；`CoreServiceProcessClient` 管理 Core 子进程和 RPC。 |
| [Sources/KSFAssistantCore](../Sources/KSFAssistantCore/) | Swift 模型和辅助库，包含格式化、历史、Token 和项目相关辅助逻辑，供 macOS 宿主及测试使用。 |
| [Windows/src](../Windows/src/) | Windows 宿主。`main.cjs` 管理窗口、托盘和系统操作；`preload.cjs` 暴露受限接口；`core-client.cjs` 管理 Core；`renderer` 使用普通 HTML/CSS/JavaScript。 |
| [Core/cmd/ksf-assistant-core](../Core/cmd/ksf-assistant-core/) | Go Core 可执行入口，组装业务服务并通过标准输入输出提供 RPC。 |
| [Core/internal/rpc](../Core/internal/rpc/) / [domain](../Core/internal/domain/) | 分别承载桌面请求分发、共享数据模型与版本化协议。 |
| [Core/internal/service](../Core/internal/service/) | 跨模块装配：仪表盘聚合、业务集成端口、本机 CLI 网关及飞书管理 RPC。 |
| [Core/internal/codex](../Core/internal/codex/) / [desktop](../Core/internal/desktop/) | 分别适配 Codex App Server 与 Desktop IPC，不由界面或飞书服务重复实现客户端。 |
| [Core/internal/tokens](../Core/internal/tokens/) / [pricing](../Core/internal/pricing/) | 读取本地会话 Token、构建历史与项目统计，并按所选价格方案计算估算费用。 |
| [Core/internal/bridge](../Core/internal/bridge/) | 调用外部 KSF 桥，消费版本化项目目录和任务投影，不直接解析 KSF Markdown。 |
| [Core/internal/integration](../Core/internal/integration/) | Core 内部业务模块：任务链接唯一存储、Codex 动作、Plan/输入选择、观察器、卡片内容与持久事件接收。 |
| [Core/internal/taskruntime](../Core/internal/taskruntime/) / [ksf-assistant-task](../Core/cmd/ksf-assistant-task/) | Core 关闭时仍可读写的中立任务记录；任务级锁、CAS、幂等及快照/历史原子提交。仅消费当前 KSF v6，不编排执行。 |
| [Core/internal/toolchain](../Core/internal/toolchain/) / [ksf-assistant-toolchain](../Core/cmd/ksf-assistant-toolchain/) | 随包 CLI、官方 Skills 与用户受管入口的校验、所有权、冲突检测和回退。 |
| [Core/internal/feishuprotocol](../Core/internal/feishuprotocol/) / [privateipc](../Core/internal/privateipc/) | 飞书 v2 通用契约与有界私有通信；corebridge 仅保留 Core 内部控制 DTO，不再暴露跨进程控制方法。 |
| [Core/internal/feishucli](../Core/internal/feishucli/) / [localipc](../Core/internal/localipc/) | 原生 CLI 解析、静态目录和当前用户本机网关；feishucommands 只在受管飞书进程执行命令。 |
| [Core/cmd/ksf-assistant-feishu-bridge](../Core/cmd/ksf-assistant-feishu-bridge/) / [Core/internal/feishu](../Core/internal/feishu/) | 飞书可执行入口及传输、授权、能力策略、通用卡片操作、工作队列、审计和子进程监管实现。 |
| [Services/FeishuBridge](../Services/FeishuBridge/) | 旧 Node 飞书实现的冻结离线契约回放基线，不是生产服务或运行时回退路径。 |
| [Tests](../Tests/) / [Windows/test](../Windows/test/) / [scripts](../scripts/) | Swift、Windows 回归以及构建、打包、身份检查和 SBOM 工具；Go 测试与实现放在对应包内。 |

### 两个 Core 的区别

Go `Core` 是 macOS 与 Windows 共同使用的业务运行时。Swift `KSFAssistantCore` 是 macOS 的模型与辅助库，不是第二个跨平台业务核心，Windows 不运行它。新增共享业务规则和外部协议适配应进入 Go；Swift 层保留界面需要的模型转换、格式化及平台辅助逻辑。

Windows 的 Electron 仍使用自身的 Node 环境；“不携带独立 Node 飞书运行时”不等于“Windows 应用不使用 Node”。

## 主要数据流

### 仪表盘读取

桌面宿主调用 `dashboard/read`，由 `Service.Dashboard` 聚合额度、任务活动、Token、项目和飞书快照，再返回统一的 `DashboardSnapshot`。Core 优先使用 Desktop 活动快照；不可用或缺少观察结果时使用 App Server 任务观察作为降级来源。价格模块基于所选方案给出费用估算，不将其当作实际账单。

各数据源按独立刷新策略缓存，飞书状态来自子进程推送后的内存快照；一次界面刷新不等于重新扫描全部文件或调用全部外部接口。实现入口见 [service.go](../Core/internal/service/service.go)。

### 项目任务创建与执行

用户选择项目后，Core 校验 KSF 根目录、读取项目目录并生成任务名称和提示词，再通过独立 App Server 创建任务并用 `thread/inject_items` 保存任务名称、目录等初始化信息。Core 等待该进程退出、释放会话写入权后，返回 `desktop-prepared-context`。宿主打开对应任务，再调用 `task/submit`；Core 通过独立控制连接等待 Desktop 接管，通过正常文本输入提交完整首条指令，使用户消息和回复均进入桌面记录，指令只提交一次。提交只允许匹配的准备记录消费一次，超时不重放。创建与开始执行是两个步骤，不能把创建成功当作任务已经运行。

项目程序启动是另一条链路：Core 准备结构化启动动作，平台宿主负责在可见终端中执行，并保持用户触发和项目路径边界。实现入口见 [service.go](../Core/internal/service/service.go)、[launch.go](../Core/internal/service/launch.go) 和平台宿主。

### 飞书事件与任务控制

`0.11.0-preview.2` 增加独立[用户身份写操作批准](architecture/user-write-approval.md)：受管 CLI 与飞书执行器共用 Go Core 状态机，宿主通过专属控制管道显示原生批准窗口。user 纯读取默认不弹框，user 写先经原有策略再逐次批准，bot 保持原权限；无法确定语义的命令拒绝。它不属于 integration 的任务卡片批准，也不属于 KSF 任务运行态或治理 Skill。等待 UI 不占业务锁，授权切换与实际执行通过短生命周期跨进程锁互斥。

0.11 只由固定版官方 CLI 承担生产授权、传输和事件消费，不直接链接飞书 SDK。官方总线 ACK 先于应用工作队列持久化；它不是业务执行确认，也不是零丢失承诺。CLI 转换后的消息/卡片按固定 schema 归一化；附件读取原消息核对绑定，延迟卡片 Token 不进入应用持久化队列。具体边界见 [本机预览改造](architecture/preview-0.11.md)。

飞书服务先持久保存事件，验证当前授权后通过 v2 事件端口交付 Core。Core 的 integration 模块持久接受后才 ACK；ACK 只代表交付，不代表任务执行成功。Core 解释任务链接、Plan 和输入动作，调用本地 Codex/Desktop 适配器，构建业务卡片；卡片发送和更新仍必须经过飞书服务的绑定与治理门禁。未注册业务处理器的事件留在飞书事件查询能力中，不自动创建任务。

卡片接入层只提取通用身份、消息和不透明 `Value/FormValue`，不解释业务动作。Core 校验动作、链接、问题和 Plan revision；旧卡片信封按原规范计算摘要，避免升级后丢 ACK 的重投递被误判为不同事件。

Core 事件执行分为 `preparing` 与 `executing`：只有可证明尚未开始副作用的失败允许有限退避重试；开始执行后失败或旧 running 中断保留为结果未知，不自动重放。活动收件箱只保留最近 64 条终态投影，其余去重收据原子归档到同一私有数据根，维持 30 天去重窗口，不再占满活动容量。归档不是第二个消费者；重试耗尽和结果未知均保留受限载荷并显示降级。

通用消息确认后由飞书服务使用原 Operation、原输入绑定和共享直接适配器执行，返回原结果 ID，无须调用方再次提交消息。确认与执行之间进程中断的 queued 消息在启动恢复时要求重新准备，绝不静默重放；running/verifying 仍按结果未知处理。任务卡片同步只有在当前内容和目标消息仍与已发送版本相同时清除待同步标记，旧响应不能清除新状态。回复保留期内的已释放或过期任务链接在附件暂存及 Codex 调用前被拒绝。

消息、文档和通用动作由各自工作箱接入统一调度器；本地持久化工作项支持原子状态迁移、并发限制与同目标串行。请求已被接收不等于远端副作用已成功，无法核实的结果保留为未知状态，具体规则见后文。

### KSF 集成的实际边界

当前 [bridge/ksf.go](../Core/internal/bridge/ksf.go) 调用所选 KSF 根目录下的 `.agents/skills/ksf-load-route-context/scripts/ksf_panel_bridge.rb`，通过外部 Ruby 解释器获取项目目录和任务归属投影。因此，这条可选集成仍依赖外部桥脚本及 Ruby，并不是应用内嵌了 KSF 微内核。缺少桥接依赖时应报告 KSF 能力不可用，不把它扩大为整个应用的启动失败。

业务读取不直接改写项目卡；桥接启用动作 `--enable` 与目录、投影读取分开，不应把“可选只读集成”理解为启用过程绝无本地状态写入。

## 边界

- KSFAssistant 桌面应用负责原生窗口、托盘/菜单栏、目录选择、登录项和安全外部链接。
- KSFAssistant Core 是唯一跨平台业务核心，统一持有 Codex App Server、Desktop IPC 与 KSF 桥客户端；KSF 正文解释留在外部桥。额度、任务目录和飞书任务控制复用同一个长期 App Server 客户端。
- KSFAssistant 飞书服务只负责飞书传输、卡片、授权、幂等、审计与队列状态机。它通过 `ksfassistant-feishu-v2` 交付通用事件，不拥有 Codex 控制端口、任务链接存储或 KSF 读取权限。
- 核心服务与飞书服务只使用受管子进程的匿名 stdin/stdout 管道；协议帧限制 4 MiB，支持双向并发、超时、取消与 EOF 生命周期，不新增 HTTP 业务端口。原生 CLI 另经 Core 的 Unix Domain Socket（macOS）或当前用户 ACL 的 Named Pipe（Windows）接入；端点绑定当前用户与规范化数据根。固定 DTO 严格拒绝未知字段、尾随 JSON、畸形可选参数以及无参数方法上的额外载荷，并保留 JSON 数字精度。
- 飞书服务保持独立进程以隔离网络、OAuth、队列与外部命令故障。它不能注册为系统常驻服务，生命周期唯一所有者是 KSFAssistant。
- KSF 项目内容是可选只读数据源，不属于启动前置条件；桥接依赖及启用动作边界见前文。
- renderer 只接收脱敏 DTO；秘密和真实飞书 ID 不跨越渲染边界。

## 降级与状态

飞书服务快照只描述自身运行状态；Core 合成业务链接与 Codex/Desktop/KSF 状态。Feishu inbound/outbound、Codex App Server、Desktop IPC、KSF context、固定 `lark-cli` 能力及三个工作箱分别报告 `ready / degraded / unavailable / disabled`；单项能力不可用不终止服务。只有重复实例、父级管道关闭或私有数据安全无法保证时退出。

飞书守护进程只创建一个 `CapabilityService`，私有 RPC、调度、目录解析和结果核对均使用同一实例；native one-shot client 不再创建执行器、读取凭据或修改队列；它只发送类型化本机请求。应用未运行时业务命令明确失败，不自动启动或离线排队。`lark-cli` 只有在路径安全、可执行、`--version` 精确为 `1.0.93` 且本地 schema 探针成功后才标记为 ready；探针结果按文件身份缓存，不随 Dashboard 刷新访问远端 API。

飞书服务维护带 revision 的运行快照并在变化时推送给核心服务。Core 为每次连接生成新代次，只在同代次内比较 revision；自动重启与手动重启共用握手和缓存初始化，拒绝旧连接迟到数据。Dashboard 读取核心服务内存，不再组合 `status`、`targets list`、`task-link protocol` 和 `task-link list` 四次子进程调用。macOS 与 Windows 桌面应用都合并并发 Dashboard 请求；面板活跃状态 3 秒、空闲 15 秒、后台额度 5 分钟。设置、价格、飞书向导和权限只在进入对应页面或修改后读取。

飞书服务自建任务使用一个 2 秒状态观察器；Desktop 连接任务使用一个按任务观察器，并优先读取核心服务的本机任务推送缓存。全量任务链接扫描只在启动和每分钟孤儿检查时运行。

## 飞书迁移

Go 入口维持 `client.json` schema v4、任务连接 schema v2 和已有队列迁移；本次更换传输不重建业务事实。冻结能力注册表继续兼容，生产执行统一使用 `lark-cli 1.0.93`。历史 26 个事件名保留查询/回放；本预览受管监听只启用消息和卡片，其他事件需显式订阅接入，Mail 旧事件明确不支持。消息、卡片、附件与归属核验也由官方 CLI 完成。Node 只保留离线回放，不进入安装包或真实事件链路。

能力策略使用 `disabled / confirm_each / allowed` 三态。读取与普通写入默认允许，立即发送、高影响写入和远程操作默认逐次确认，删除、清空、覆盖、移动与历史回退默认禁用。101 项破坏性能力不能按风险级整体放开，也不能设为免确认，只能逐项设为 `confirm_each`。每项都有版本化守卫：`strong` 使用权威预读和后置核验，`bounded` 至少生成脱敏影响摘要；远端结果无法证明时只能进入 `outcome_unknown`。五分钟确认凭证绑定能力、目标参数指纹、预读证据和策略 revision；执行前队列再次复核总开关、策略与预读证据。

审批域开放 14 项固定业务能力和 4 项事件订阅能力。审批定义、实例和任务查询默认允许；发起、同意、拒绝、抄送、催办、加签、转交及订阅变更默认逐次确认；撤回实例和退回任务属于破坏性动作，默认关闭，并带固定实例预读与复读门禁。审批表单、意见、用户标识和实例标识继续按私有正文处理，不进入命令行、普通日志或事件历史。

已提交操作超时不会被当成失败，也不会自动重放副作用。具备复读能力的操作最多后台核对三次；无法安全复读或三次后仍不明确时进入 `outcome_unknown + manual_review`。等待确认过期则进入 `expired + reprepare_on_user_request`，可以确定本次没有执行。

三个工作箱内部使用 schema v4 工作项，保留 `workbox-v3` 原路径。工作项持久保存 Operation 关联和执行阶段，在 `pending / running / terminal` 间原子迁移；Operation 是业务状态权威，索引只是可重建投影。统一调度总并发 4、`lark-cli` 并发 2、长远端任务并发 1，同目标串行。恢复统一检查跨文件中断窗口；无法证明授权或执行阶段的旧工作不自动重放。迁移失败时禁止受影响写入，不启用双消费者。

队列终态、结果、事件和入站回执保留 30 天或每类 20,000 条；操作记录保留 90 天或 10,000 条；审计日志保留 180 天或合计 200 MB。未知结果和人工复核最多保护 90 天。飞书入站一旦终态便删除原始 payload，仅保留指纹和安全分类。子服务 stderr 持续排空到 `0600` 诊断环：1 MB × 5、最多 7 天；非结构化内容不保存原文。

Plan 选择链路保留 Desktop request ID 的原始字符串／整数类型和字节。卡片只携带绑定任务、turn、owner、request 与问题定义的 revision 哈希；提交前重新匹配权威快照。Follower 的 `{ok:true}` 只代表收到请求，只有严格更新的快照中精确 request ID 已消失才算成功。5 秒同步等待后最多继续核验到 30 秒，超时进入 `outcome_unknown` 并重新开放选项，答案绝不自动重放。连续三轮实机选择均解除 Desktop 等待前，不得宣称 Desktop 任务控制完整。

macOS arm64/x86_64 与 Windows x64/arm64 均只选择 Go 飞书服务，不携带独立 Node runtime、飞书 npm 生产依赖或 Node 服务源码，也不存在跨运行时自动回退。四平台真实硬件验收仍是发布门槛，交叉编译不能替代实机结论。

任务链接磁盘 schema 保持 v2 并无损保留未知字段。UI 只根据 `linkState + turnState + turnOwner + controls` 计算展示状态，不再消费旧 `state`。活动连接不清理；已断开或过期记录保留 7 天，终态历史最多 500 条；待卡片同步、附件清理或恢复中的记录最多保护 30 天，超过后写脱敏放弃审计再清除。

## 生命周期

桌面宿主拥有 Core 子进程，Core 拥有飞书子进程。关闭或隐藏面板不等于退出应用；只有明确退出才执行整棵受管进程树的关闭链路，飞书服务不独立常驻。

macOS 的按钮退出只把一次退出请求交给 AppKit 运行循环；按钮与系统退出均由 `AppDelegate.applicationShouldTerminate` 统一等待 `UsageViewModel.shutdown`。不得在持有主队列的 Swift Task 内调用 `NSApplication.terminate`，否则 `terminateLater` 的嵌套模态循环会阻塞负责确认退出的 MainActor 任务。重复按钮请求合并为一次。Core 的 `shutdown` 在停止受管服务、写出确认后立即结束 RPC 读取循环，不依赖宿主再发送输入或关闭 stdin。回归入口为 `bash scripts/test-quit-lifecycle.sh` 及 Go RPC shutdown 测试。

Core 启动桥，桥通过父级控制管道感知所有者。正常退出先等待最多 5 秒，再结束进程组或 Windows kill-on-close Job Object。异常退出按 1、2、5 秒退避；滚动 5 分钟最多重启 3 次，持续健康 5 分钟后清零。超过阈值进入 `degraded`，只允许用户“重新启动”。

## 维护关注点

当前分层的主要收益是跨平台共享业务、平台能力隔离、飞书故障隔离，以及 KSF 可选集成。以下是基于当前职责分布的维护判断，不代表已确认的缺陷或已经实施的重构：

- Go `service` 和 macOS `UsageViewModel` 是职责较集中的协调点。新增能力应把协议、领域规则和平台动作放回对应模块，避免在协调入口持续叠加实现细节。
- 飞书模块同时覆盖传输、治理、持久化和任务控制，维护时应保持契约、队列状态机和外部执行器的边界，不绕过统一权限、幂等和核对路径。
- Swift 辅助库中的 Token、历史和项目逻辑与 Go 存在概念重叠。调整前应核对实际调用关系，避免新增两套独立业务规则，也不能仅因名称相近就删除仍被宿主使用的逻辑。
- 外部 KSF Ruby 桥与冻结 Node 回放基线属于当前依赖和兼容边界，不代表未来目标架构；替换或清理需要单独验证契约和回归，不能通过文档改写宣称已经完成。
