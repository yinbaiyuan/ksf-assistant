# KSFAssistant 产品身份与升级

KSFAssistant 表示“知识－技能－飞轮－助手”（Knowledge–Skills–Flywheel Assistant）。应用 ID 为 `com.ksfassistant.desktop`，Swift 应用为 `KSFAssistant`、共享模块为 `KSFAssistantCore`，Go 模块为 `ksfassistant/core`，自有命令使用 `ksf-assistant-*` 前缀。

产品更名不改变 Codex 平台 API、账号权限、功能、飞书能力策略或持久化业务 schema。Core 与飞书子进程的私有协议一起更新，不支持混装新旧服务二进制。

## 旧设置迁移

首次启动在服务启动前执行迁移。macOS 优先识别 `com.codexassistant.desktop`，其次为 `com.ksf.codexusagebar`；Windows 优先识别 `CodexAssistant`，其次为 `Codex Usage Bar`。已有目标不覆盖，旧文件保留。迁移保留未知字段，但它们不因此成为 renderer 可访问的数据或可写设置。

文件迁移先写同一文件系统的临时位置，再原子发布。失败不标记完成、不回退为空设置启动。固定项目与项目排序只转换准确的旧项目记忆卡相对路径，不替换任意包含旧名称的用户内容。

## 保留的旧标识

| 标识 | 保留原因 |
| --- | --- |
| `CodexAssistant`、`Codex Usage Bar`、`CodexUsageBar` 及旧 bundle ID | 旧设置迁移、运行中旧宿主识别、安装退出等待与相关测试 |
| `CODEX_USAGE_BAR_SUPPORT_DIR` 与其默认 `CodexUsageBar` 目录 | 外部 KSF 面板桥拥有的接口，本次不修改 KSF 运行机制 |
| KSF 下 `runtime-data/codex-usage-bar` | 既有每日聚合历史缓存位置，不新建空账本 |
| `codex-feishu-task-link-v1` | 任务连接协议及卡片幂等身份，不是产品品牌 |
| 历史签名证书名称 | 发布卫生检查继续拒绝旧、新机器专属证书引用 |
| 旧远端地址、历史提交与已安装旧包 | 本次不修改远端、Git 历史或生产安装 |

逐文件例外见 `scripts/check-product-identity.mjs`；新增例外必须说明兼容对象，不能通过拆分字符串逃避检查。

## 进程、秘密与切换边界

飞书仍使用同一个 `FEISHU_BRIDGE_DATA_DIR` 和 `logs/bridge.pid`，保留旧 PID 记录格式。新版额外持有同目录生命周期 OS 锁，以避免新版并发启动争抢陈旧 PID；锁文件本身不删除。旧活跃实例和无法确认的锁状态均拒绝启动新消费者。

Keychain、lark-cli 配置和 DPAPI 凭据保持原位。测试仅使用隔离夹具，不读取真实秘密，不复制生产队列。

不同目录运行的历史独立 Node 桥可能不参与同一把锁。新代码无法追溯修复旧进程的互斥实现；正式切换前必须确认旧应用、独立桥及其自动重启入口已停止。交叉编译不替代四平台实机验收，也不能证明旧安装已切换。

本轮只交付源码、文档和构建验证。安装版、Codex 已保存的工程路径及外部 KSF 飞书 Skill 不自动调整；部署前需另行更新这些入口。回退保留旧数据，只撤销本轮代码与目录更名，不覆盖其他任务的修改。

## 验证

运行 `node scripts/check-product-identity.mjs`、Go 全量与相关 Race 测试、Windows Node 测试、Swift 测试及独立迁移测试，再做四目标 Go 构建与 macOS universal2 包检查。迁移测试覆盖旧源优先级、目标已存在、未知字段、重复迁移、坏数据和失败保护；进程锁测试使用隔离数据验证旧 PID 占用与并发互斥。
