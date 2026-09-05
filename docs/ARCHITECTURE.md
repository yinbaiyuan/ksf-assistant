# 架构说明

```text
CodexAssistant 桌面应用
        │
        └─ 私有进程通信 ─ CodexAssistant 核心服务
                              ├─ Codex 与本机任务
                              ├─ KSF 项目上下文
                              └─ 私有进程通信 ─ CodexAssistant 飞书服务
                                                     ├─ 飞书消息与卡片
                                                     ├─ 队列与任务连接
                                                     └─ 固定飞书能力
```

## 边界

- CodexAssistant 桌面应用负责原生窗口、托盘/菜单栏、目录选择、登录项和安全外部链接。
- CodexAssistant Core 是唯一业务核心，唯一持有 Codex App Server、Desktop IPC 与 KSF 上下文解析；额度、任务目录和飞书任务控制复用同一个长期 App Server 客户端。
- CodexAssistant 飞书服务只负责飞书传输、卡片、授权、幂等、审计与队列状态机。它通过 `codexassistant-core-bridge-v1` 白名单能力端口调用核心服务，不直接创建 Codex 或本机任务客户端。
- 核心服务与飞书服务只使用受管子进程的匿名 stdin/stdout 管道；协议帧限制 4 MiB，支持双向并发、超时、取消与 EOF 生命周期，不开放端口或 Socket。固定 DTO 严格拒绝未知字段、尾随 JSON、畸形可选参数以及无参数方法上的额外载荷，并保留 JSON 数字精度。
- 飞书服务保持独立进程以隔离网络、OAuth、队列与外部命令故障。它不能注册为系统常驻服务，生命周期唯一所有者是 CodexAssistant。
- KSF 是可选只读集成，不属于启动前置条件。
- renderer 只接收脱敏 DTO；秘密和真实飞书 ID 不跨越渲染边界。

## 降级与状态

飞书服务将飞书传输、飞书工作流和核心能力端口分层。Feishu inbound/outbound、Codex App Server、Desktop IPC、KSF context、固定 `lark-cli` 能力及三个工作箱分别报告 `ready / degraded / unavailable / disabled`；单项能力不可用不终止服务。只有重复实例、父级管道关闭或私有数据安全无法保证时退出。

飞书守护进程只创建一个 `CapabilityService`，私有 RPC、调度、目录解析和结果核对均使用同一实例；native one-shot client 每次调用也只创建一个实例并注入各命令适配器。`lark-cli` 只有在路径安全、可执行、`--version` 精确为 `1.0.92` 且本地 schema 探针成功后才标记为 ready；探针结果按文件身份缓存，不随 Dashboard 刷新访问远端 API。

飞书服务维护带 revision 的聚合快照并在变化时推送给核心服务。Dashboard 读取核心服务内存，不再组合 `status`、`targets list`、`task-link protocol` 和 `task-link list` 四次子进程调用。macOS 与 Windows 桌面应用都合并并发 Dashboard 请求；面板活跃状态 3 秒、空闲 15 秒、后台额度 5 分钟。设置、价格、飞书向导和权限只在进入对应页面或修改后读取。

飞书服务自建任务使用一个 2 秒状态观察器；Desktop 连接任务使用一个按任务观察器，并优先读取核心服务的本机任务推送缓存。全量任务链接扫描只在启动和每分钟孤儿检查时运行。

## 飞书迁移

Go 入口维持 `client.json` schema v4 与任务连接 schema v2；旧队列 schema v2 由一次性迁移器导入内部 V3 工作项仓。能力注册表升级为 v2，共 816 项。原有 23 类事件、邮件接收事件和 2 类审批状态事件，共 26 类事件由 `oapi-sdk-go/v3.11.0` 的同一条长连接接入。审批事件还需通过固定能力为当前授权用户建立“参与审批”或“管理审批”订阅关系；不会启动第二个 watcher。邮件事件本地开关默认关闭，关闭时载荷不入箱、不持久化、不触发工作流。219 项冻结能力及扩展长尾能力继续委托随包 `lark-cli 1.0.92`，消息、卡片和媒体由 Go SDK 执行。Node 只保留冻结离线回放基线，不进入安装包、运行入口或真实事件链路。

能力策略使用 `disabled / confirm_each / allowed` 三态。读取与普通写入默认允许，立即发送、高影响写入和远程操作默认逐次确认，删除、清空、覆盖、移动与历史回退默认禁用。101 项破坏性能力不能按风险级整体放开，也不能设为免确认，只能逐项设为 `confirm_each`。每项都有版本化守卫：`strong` 使用权威预读和后置核验，`bounded` 至少生成脱敏影响摘要；远端结果无法证明时只能进入 `outcome_unknown`。五分钟确认凭证绑定能力、目标参数指纹、预读证据和策略 revision；执行前队列再次复核总开关、策略与预读证据。

审批域开放 14 项固定业务能力和 4 项事件订阅能力。审批定义、实例和任务查询默认允许；发起、同意、拒绝、抄送、催办、加签、转交及订阅变更默认逐次确认；撤回实例和退回任务属于破坏性动作，默认关闭，并带固定实例预读与复读门禁。审批表单、意见、用户标识和实例标识继续按私有正文处理，不进入命令行、普通日志或事件历史。

已提交操作超时不会被当成失败，也不会自动重放副作用。具备复读能力的操作最多后台核对三次；无法安全复读或三次后仍不明确时进入 `outcome_unknown + manual_review`。等待确认过期则进入 `expired + reprepare_on_user_request`，可以确定本次没有执行。

三个工作箱内部使用 V3 工作项仓：每个请求以独立私有文件在 `pending / running / terminal` 间原子迁移，结果按 ID直接读取，Dashboard 只读 O(1) 原子索引。统一调度器总并发 4、`lark-cli` 并发 2、长远端任务并发 1；同目标按 conflict key 串行，不同目标公平并行。旧 schema v2 只扫描迁移一次，原文件进入 30 天私有归档，迁移失败时禁止写入且不启用双消费者。

队列终态、结果、事件和入站回执保留 30 天或每类 20,000 条；操作记录保留 90 天或 10,000 条；审计日志保留 180 天或合计 200 MB。未知结果和人工复核最多保护 90 天。飞书入站一旦终态便删除原始 payload，仅保留指纹和安全分类。子服务 stderr 持续排空到 `0600` 诊断环：1 MB × 5、最多 7 天；非结构化内容不保存原文。

Plan 选择链路保留 Desktop request ID 的原始字符串／整数类型和字节。卡片只携带绑定任务、turn、owner、request 与问题定义的 revision 哈希；提交前重新匹配权威快照。Follower 的 `{ok:true}` 只代表收到请求，只有严格更新的快照中精确 request ID 已消失才算成功。5 秒同步等待后最多继续核验到 30 秒，超时进入 `outcome_unknown` 并重新开放选项，答案绝不自动重放。连续三轮实机选择均解除 Desktop 等待前，不得宣称 Desktop 任务控制完整。

macOS arm64/x86_64 与 Windows x64/arm64 均只选择 Go 飞书服务，不携带独立 Node runtime、飞书 npm 生产依赖或 Node 服务源码，也不存在跨运行时自动回退。四平台真实硬件验收仍是发布门槛，交叉编译不能替代实机结论。

任务链接磁盘 schema 保持 v2 并无损保留未知字段。UI 只根据 `linkState + turnState + turnOwner + controls` 计算展示状态，不再消费旧 `state`。活动连接不清理；已断开或过期记录保留 7 天，终态历史最多 500 条；待卡片同步、附件清理或恢复中的记录最多保护 30 天，超过后写脱敏放弃审计再清除。

## 生命周期

macOS 的按钮退出只把一次退出请求交给 AppKit 运行循环；按钮与系统退出均由 `AppDelegate.applicationShouldTerminate` 统一等待 `UsageViewModel.shutdown`。不得在持有主队列的 Swift Task 内调用 `NSApplication.terminate`，否则 `terminateLater` 的嵌套模态循环会阻塞负责确认退出的 MainActor 任务。重复按钮请求合并为一次。Core 的 `shutdown` 在停止受管服务、写出确认后立即结束 RPC 读取循环，不依赖宿主再发送输入或关闭 stdin。回归入口为 `bash scripts/test-quit-lifecycle.sh` 及 Go RPC shutdown 测试。

Core 启动桥，桥通过父级控制管道感知所有者。正常退出先等待最多 5 秒，再结束进程组或 Windows kill-on-close Job Object。异常退出按 1、2、5 秒退避；滚动 5 分钟最多重启 3 次，持续健康 5 分钟后清零。超过阈值进入 `degraded`，只允许用户“重新启动”。
