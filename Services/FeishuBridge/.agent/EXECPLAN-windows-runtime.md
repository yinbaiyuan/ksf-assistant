# Windows 运行机制与跨平台宿主层

## 目标

在不复制飞书业务逻辑的前提下，让同一套飞书桥代码能够在 macOS 与 Windows 11 上安装、常驻、启动、停止、诊断和运行。Windows 使用用户级计划任务、DPAPI 凭据文件、用户私有 ACL、原生休眠抑制和可选 Codex Desktop 命名管道；outbox、docbox、actionbox、官方 SDK、能力注册表、目录缓存、事件收件箱、Codex app-server、日志与审计继续共用现有实现。

## 当前状态

- 主程序、队列、飞书能力和 Codex app-server 基本不依赖平台。
- `scripts/start-bridge.js` 固定调用 `/usr/bin/caffeinate`。
- 统一客户端固定检查和控制 `launchctl`。
- 私有配置默认写入 `~/.config/feishu-bridge`，敏感缓存和临时资源依赖 POSIX `0700/0600`。
- 官方 SDK 凭据复用逻辑只实现了 macOS Keychain。
- Codex Desktop 控制固定验证 Unix Socket，并用 `/usr/bin/open` 打开任务。
- `lark-cli` 官方 npm 包已经声明支持 `win32`，但本项目默认可执行路径没有处理 Windows 启动器。

## 边界

- 飞书业务能力、权限、显式发送授权、三类队列、dry-run、幂等、脱敏和审计语义不因平台变化。
- Windows 只新增宿主适配、安装和运维机制；不引入第二套业务实现。
- Windows 常驻使用当前登录用户的计划任务，不伪装成系统服务；OAuth、DPAPI 和 Codex 都保持同一用户身份。
- Windows 不读取 macOS Keychain。官方 SDK 使用当前用户 DPAPI 凭据文件；`lark-cli` 继续使用自己的官方 Windows 原生凭据存储。
- Codex Desktop 任务连接在 Windows 上只有配置了命名管道时启用；未配置时普通飞书到 Codex app-server 链路仍完整可用，doctor 明确报告该可选宿主集成状态。
- 不改变飞书权限、入站白名单、主动出站授权或审计格式。

## 改动方案

1. 新增平台运行模块，统一平台名、私有数据根、日志根、lark-cli 启动入口、Codex 可执行名、IPC 端点、打开任务和休眠抑制。
2. 把 `launchctl` 专用逻辑收敛为服务管理适配：macOS 保持 LaunchAgent；Windows 使用固定 PowerShell 查询/启动计划任务，公开状态同时保留兼容字段。
3. Windows 默认把配置、缓存、事件、队列、日志和私有临时文件放入 `%LOCALAPPDATA%\FeishuBridge`，安装时移除继承 ACL，仅授予当前用户、SYSTEM 和 Administrators。
4. 新增 DPAPI 凭据设置脚本和只在进程内解密的读取适配；正文和秘密不进入命令行、日志或仓库。
5. 新增 Windows 安装、卸载、运行 PowerShell 脚本，注册登录触发、失败重启、无限运行时长的用户级计划任务；卸载不删除配置和审计数据。
6. Codex Desktop IPC 支持 Windows 命名管道和 `Start-Process codex://...`，macOS Unix Socket 行为保持不变。
7. 更新统一客户端、doctor、环境示例、配置文档、技术规格和实施指南，明确平台差异与复刻流程。

## 安全策略

- 计划任务不携带 App Secret、token、目标 ID 或消息正文。
- DPAPI 凭据绑定当前 Windows 用户，读取脚本只从环境传文件路径，明文仅通过子进程 stdout 进入桥内存。
- Windows 私有根和项目缓存目录使用显式 ACL；doctor 检查 ACL 初始化标记和路径边界。
- 服务控制只接受配置中的固定任务名，不拼接 PowerShell 命令；任务名通过环境变量传入固定脚本。
- Windows 上 `.cmd` 不通过包含业务输入的 shell 拼接执行；lark-cli 直接由 Node 启动官方 `scripts/run.js`。
- 现有 outbox/docbox/actionbox 最终权限裁决和审计保持不变。

## 验证步骤

- Node 单元测试覆盖平台路径、Windows lark-cli 启动、服务状态/控制、命名管道、打开任务、休眠抑制和 DPAPI 适配的固定参数与脱敏。
- 现有完整 `npm test`、`node --check` 与 `git diff --check` 全部通过。
- 在 macOS 上复读 `status/doctor`，确认 LaunchAgent、官方 SDK、23/23 事件和三类队列无回归。
- Windows 真机验收：安装依赖、设置 DPAPI 凭据、运行 `lark-cli auth status`、注册计划任务、启动、doctor、收一条授权入站消息、执行一次 dry-run、重启和注销后自启动。
- Windows Codex Desktop 命名管道未能在本机 macOS 验证时明确保留为真机验收项，不把模拟测试描述为真实通过。

## 回滚方式

- Windows 可运行 `windows/Uninstall-FeishuBridge.ps1` 停止并注销计划任务，配置、队列和审计数据默认保留。
- 代码回滚时删除 Windows 脚本和平台适配调用，恢复 `launchctl`、`caffeinate`、Unix Socket 和 macOS 路径的原实现即可；业务队列 schema 不变，无需数据迁移。

## 进度

- [x] 完成平台耦合清单和边界设计。
- [x] 实现跨平台宿主层与服务管理。
- [x] 实现 Windows DPAPI、ACL、安装和运行脚本。
- [x] 接入主程序、统一客户端和 Codex Desktop IPC。
- [x] 更新文档与环境示例。
- [x] 完成自动化回归和 macOS 生产只读验证（184/184，doctor healthy）。
- [x] 交付 Windows 真机验收清单与未验证边界（真机执行待 Windows 主机）。
