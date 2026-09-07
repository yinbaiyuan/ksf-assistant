# KSFAssistant

KSFAssistant 的名称含义是“知识－技能－飞轮－助手”（Knowledge–Skills–Flywheel Assistant）。

KSFAssistant 是一个 macOS 菜单栏与 Windows 系统托盘应用，用来查看 Codex 额度、Token 活动和任务状态。它也可以在软件内接入飞书；KSF 项目工作台是可选增强能力。

当前测试版本：`0.11.0-preview.4`。本版作为首个测试里程碑，包含飞书接入、任务收发、Token 统计、受管 Skills 和桌面批准界面的改进，详见[版本说明](docs/releases/0.11.0-preview.4.md)。保留[用户身份写操作桌面批准门禁](docs/architecture/user-write-approval.md)及[受管飞书能力](docs/architecture/managed-feishu-capabilities.md)，通过[随包 Skills 入口适配](docs/architecture/managed-feishu-skills.md)统一调用路径。

当前源码增加[飞书扫码创建应用与沿用现有配置](docs/architecture/feishu-app-registration.md)。扫码创建仅用于首次配置，不覆盖已有应用、授权或业务记录；创建应用不等于用户授权或飞书服务验收通过。

[飞书配置生命周期](docs/architecture/feishu-configuration-lifecycle.md)由 Core 统一汇总实际应用、身份、权限、功能模式与连接状态；旧向导进度不决定是否接入。检查不自动授权、绑定操作者、启用功能或发送测试，配置变更须通过桌面的独立操作完成。

KSFAssistant 是社区开源项目，不是 OpenAI 官方产品，也不代表 OpenAI
背书。“Codex”和“OpenAI”及其相关商标归各自权利人所有。

## 普通用户快速开始

1. 从未来公开 Release 页面下载与你的电脑匹配的安装包。
2. 安装并打开 KSFAssistant。
3. 保持 Codex 已登录。额度、Token 和任务状态会自动出现。
4. 如需飞书，在“设置 → 飞书”中检查已有配置；首次配置可扫码创建或接入已有应用。用户授权、远程操作者绑定和功能启用分别确认，机器人身份不依赖用户 OAuth。
5. 如需 KSF 项目工作台，在“设置 → KSF 知识库”中选择目录；软件会自动验证。

普通用户不需要安装 Node.js、Go、Git 或 Ruby，不需要运行 Terminal/PowerShell，不需要启动后台服务，也不需要编辑 `.env`、JSON 或其他配置文件。飞书租户管理员必须完成的官方授权和应用发布确认会由软件打开对应飞书页面，并在返回后自动检查。

预览包可能尚未签名或公证。安装前请核对发布页提供的 SHA-256；macOS 或 Windows 可能显示系统安全提醒。项目不会要求用户通过关闭系统安全能力来安装。

## 功能与边界

- 显示 Codex 通用额度及重置时间。
- 汇总本机普通输入、缓存输入、输出和 30 天 Token 历史，并提供可选 API 价格估算。
- 显示 Codex Desktop 顶层任务的运行、等待与完成状态。
- 可选连接 KSF 项目目录；KSF 不可用时不影响额度、Token、任务状态或飞书。
- KSFAssistant 自动管理 KSFAssistant Core 与飞书服务的完整生命周期。关闭面板或最小化到托盘不会停止服务；明确退出应用才会关闭进程树。
- 飞书凭据由官方 CLI 管理本机安全存储；App Secret 通过私有 stdin 传入，Token 与设备凭据不返回界面、不进入普通日志或命令行。
- 官方 CLI/Skills 随应用固定版本；任务报告 CLI 可在 Core 关闭时运行，报告不改变 Codex 的执行权。
- 飞书真实用户 ID、消息正文和队列内容不进入 KSFAssistant 的渲染层。

## 平台支持

- macOS 13 或更新版本：Apple Silicon 与 Intel
- Windows 10/11：x64 与 arm64

macOS 与 Windows 安装包都只携带 Go 飞书服务与固定版 `lark-cli`，不携带独立 Node 飞书运行时或服务源码。四个平台不会自动回退到 Node，也不会同时消费同一飞书应用的真实事件；尚未完成的实机验收会阻止发布，而不是切回旧实现。

## 开源与贡献

本项目采用 [MIT License](LICENSE)。安全、隐私、支持与贡献规则见：

- [贡献指南](CONTRIBUTING.md)
- [安全政策](SECURITY.md)
- [隐私说明](PRIVACY.md)
- [支持范围](SUPPORT.md)
- [行为准则](CODE_OF_CONDUCT.md)
- [架构说明](docs/ARCHITECTURE.md)
- [0.11 本机预览边界](docs/architecture/preview-0.11.md)
- [产品身份与升级](docs/architecture/product-identity-migration.md)
- [开发者构建说明](docs/CONTRIBUTING_BUILD.md)
- [第三方依赖声明](THIRD_PARTY_NOTICES.md)

本轮只准备源码与预览资产，不创建公开远端、不推送或发布。未来公开仓库将从审计通过的工作树导出干净快照并创建单一首提交，不携带当前私有 Git 历史、remote、refs、reflog 或对象库。

文档和知识库操作使用受管 `ksfas-lark`；旧 docbox 与 `ksf-feishu-bridge` Skill 已清理，范围和兼容边界见 [遗留飞书能力清理](docs/architecture/feishu-legacy-retirement.md)。
