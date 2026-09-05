# 兼容性

| 组件 | 团队版要求 | 失败边界 |
| --- | --- | --- |
| macOS | 13 或更高；arm64 / x86_64 | 更低版本不支持 |
| Windows | Windows 10/11；x64 / arm64；Codex Desktop 已安装 | 更低版本及 32 位系统不支持 |
| 核心服务 | `ksf-assistant-core-v2`；Go 原生二进制与宿主架构匹配 | 核心不可用时宿主显示恢复提示，不直接读取 Codex/KSF/飞书数据 |
| Codex CLI | 支持 `account/rateLimits/read` 与 `account/usage/read` | 额度与账号 Token 独立降级 |
| Codex Desktop | 支持当前本机 IPC 协议 | 任务计数、打开任务和前台首轮提交可能不可用 |
| KSF | 根目录含 `AGENTS.md`、标准面板桥及 v1 Catalog/Projection 协议；Ruby 3.2+ 在 `PATH` | 项目工作台与长期 Token 历史不可用 |
| 飞书服务 | macOS 与 Windows 四个目标均使用包内 Go 服务及固定 `lark-cli 1.0.92`；任务控制要求 `codex-feishu-task-link-v1` response v2 | 飞书状态、测试发送与任务连接独立降级；退出 KSFAssistant 时随核心服务一起停止；不自动跨运行时回退 |

Codex Desktop IPC 不是公开稳定接口。macOS 默认连接当前用户 Unix Socket；Windows 预览版不猜测私有端点，必须通过 `CODEX_DESKTOP_IPC_PATH` 明确指定兼容 Codex Desktop 暴露的当前用户命名管道。IPC 不可用时，额度、账号 Token、项目目录和已完成任务仍可读取，但实时状态及前台首轮提交会降级。升级 Codex 后应运行对应平台烟测，再判断是否继续兼容。

核心服务调用 KSF 桥时会在未显式设置的情况下，把 `CODEX_USAGE_BAR_SUPPORT_DIR` 映射到当前平台的用户配置目录；KSF Markdown 的解释仍由 KSF 自有桥负责。

## 飞书统一服务入口

- Core↔飞书只接受 `ksfassistant-feishu-v2`，没有旧私有协议自动回退；桌面 `ksf-assistant-core-v2` 与任务链接外部 response v2 保持不变。
- 原生 CLI 的业务命令要求 KSFAssistant 已运行，经当前用户本机网关进入 Core；不再独立执行、启动应用或写离线队列。帮助、版本和静态目录可脱机读取。
- Windows Named Pipe ACL、真实退出链路及硬件迁移需要 Windows 实机验收。Go 交叉编译只证明编译兼容，不能代替这些验收。
- 运行数据仅在新版启动时按 [迁移约束](architecture/feishu-service-v2-migration.md) 处理。源码构建不表示已安装应用或生产数据已切换。
