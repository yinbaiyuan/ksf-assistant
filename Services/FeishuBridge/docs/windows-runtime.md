# Windows 运行机制

> 冻结离线基线：本文只记录已经退出生产的 Node 兼容实现及旧独立常驻
> 方式，不代表当前 Windows 架构。当前 macOS/Windows 安装包均只使用 Go
> 飞书服务；本目录内容不进入安装包、运行入口或自动回退链路。

端到端安装先阅读 `docs/installation.md`；本文只说明 Windows 宿主层细节。

飞书桥在 Windows 11 上复用与 macOS 完全相同的业务核心：官方 SDK 入站长连接、outbox/docbox/actionbox、能力注册表、目录缓存、显式授权、幂等、脱敏和审计都不分叉。Windows 只替换宿主层。

## 运行模型

| 关注点 | macOS | Windows |
|---|---|---|
| 常驻 | 当前用户 LaunchAgent | 当前用户计划任务 |
| 私有数据 | `~/.config/feishu-bridge` | `%USERPROFILE%\.config\feishu-bridge` |
| 权限 | 目录 `0700`、文件 `0600` | 关闭 ACL 继承，仅当前用户、SYSTEM、Administrators |
| 官方 SDK 凭据 | lark-cli/macOS Keychain | 当前用户 DPAPI 文件 |
| lark-cli | `.bin/lark-cli` | 官方 `scripts/run.js`，由 Node 直接启动 |
| 防休眠 | `caffeinate` | `SetThreadExecutionState` |
| Desktop IPC | 当前用户 Unix Socket | 可选、显式配置的命名管道 |

计划任务只启动固定的 `windows/Run-FeishuBridge.ps1`，不携带 Secret、目标 ID 或消息正文。卸载计划任务不会删除私有数据、凭据、队列或审计。

`restart` 会先停止计划任务，再只清理命令行精确指向当前仓库 `scripts/start-bridge.js` 或 `bot-bridge.js` 的遗留 Node 进程，最后启动任务并等待新桥进程就绪。这避免计划任务已回到 `Ready`、旧子进程却继续持有 PID 锁和入站长连接的假重启状态。后台启动输出写入私有数据根的 `logs/host`，继承同一 Windows ACL，不进入仓库。

## 前置条件

- Windows 11，Windows PowerShell 5.1 或 PowerShell 7 能调用系统 ScheduledTasks 模块。
- Node.js 20 或更高版本，`node.exe`、`npm` 和 `codex.exe` 对当前用户可见。
- 使用当前登录用户完成 lark-cli、Codex 和计划任务配置；不要混用管理员或其他 Windows 账户。
- 飞书应用权限、事件订阅和发布状态与 macOS 版相同。

## 首次安装

在 PowerShell 中进入仓库：

```powershell
Set-Location C:\path\to\feishu-bot-bridge
npm ci
Copy-Item .env.example .env.local
```

编辑 `.env.local`。保留业务开关、事件目录和入站权限；建议不填写路径项，让平台层选择 Windows 私有目录。Windows 会强制日志、三类队列、事件、目录缓存和临时出站媒体位于 `FEISHU_BRIDGE_DATA_DIR` 内；任何逃逸路径都会让主进程和写命令 fail-closed，并由 `doctor` 报出字段名。

初始化凭据时，可创建专用飞书应用或复用既有应用。二维码 OAuth 只能授权已经配置到本机的应用，不能把既有应用的 App Secret 从飞书平台取回；迁移期开发者可通过安全导入或既有 `lark-cli` profile 准备测试凭据，普通用户由 CodexAssistant 软件内向导完成。

本机安全导入从 stdin 或私有 JSON 文件读取 Secret，不把 Secret 放进命令行、`.env.local`、Skill 或版本化文件，并会同步写入当前 Windows 用户 DPAPI，供官方 SDK 长连接使用：

```powershell
'{"appId":"<existing_app_id>","appSecret":"<existing_app_secret>","brand":"feishu"}' |
  npm run bridge -- auth configure-existing --payload-file -
```

验证配置：

```powershell
npx lark-cli auth status
```

`auth start-config` 默认不创建应用；未发现既有配置时会 fail-closed。只有用户明确要创建全新的飞书 CLI 应用时，才允许：

```powershell
npm run bridge -- auth start-config --create-new
```

如需只写 DPAPI 后备凭据，也可以使用交互脚本，不把 Secret 放进命令行：

```powershell
.\windows\Set-FeishuBridgeCredential.ps1 -AppId '<app_id>'
```

用户 OAuth 也走同一套二维码入口：

```powershell
npm run bridge -- auth start-user
npm run bridge -- auth finish-user
```

安装并启动用户级计划任务：

```powershell
npm run windows:install
npm run bridge -- status
npm run bridge -- doctor
```

安装器会创建 `%USERPROFILE%\.config\feishu-bridge`、收紧 ACL、注册登录触发、失败后一分钟重启且无执行时长上限的 `FeishuBotBridge` 任务，然后立即启动。发现旧 `%LOCALAPPDATA%\FeishuBridge` 时，安装器只复制目标中尚不存在的文件，不覆盖、不删除旧目录。

## 日常运维

```powershell
npm run bridge -- status
npm run bridge -- doctor
npm run bridge -- start
npm run bridge -- restart
Get-ScheduledTask -TaskName FeishuBotBridge
```

`status` 在 Windows 返回统一的 `service` 字段和兼容显示字段 `windowsTask`；`doctor` 检查计划任务、PID、私有根 ACL、lark-cli、DPAPI 凭据、官方 SDK、事件目录、Codex daemon 和三类队列。

更新代码后在仓库执行 `npm ci`，再运行 `npm run bridge -- restart`。队列 schema、客户端配置和业务缓存无需迁移。

## Codex Desktop 任务连接

普通“飞书消息 → Codex app-server → 飞书回复”在 Windows 上不依赖 Desktop IPC，安装后即可使用。

只有“把既有 Codex Desktop 任务连接到飞书并继续/修正/停止原任务”需要 Desktop 私有 IPC。当前实现支持 Windows 命名管道，但不猜测 Codex Desktop 的管道名称。只有宿主明确提供当前用户私有管道后，才在 `.env.local` 设置：

```text
CODEX_DESKTOP_IPC_PATH=\\.\pipe\codex-desktop-<current-user>
```

未配置时该可选能力 fail-closed，`task-link protocol` 会报告 `codexDesktopIPC: false`；其余桥能力不受影响。

## 卸载与回退

```powershell
npm run windows:uninstall
```

该命令停止并注销计划任务，但保留 `%USERPROFILE%\.config\feishu-bridge`。确认不再需要历史队列、凭据与审计后，再由用户手工处理该目录；卸载脚本不会自动删除。

## Windows 真机验收

自动化测试可以验证固定参数、路径、DPAPI 适配和命名管道约束，但不能替代 Windows 真机。至少完成：

1. `npm test` 全量通过，`npm run bridge -- doctor` 无硬错误。
2. 授权用户给机器人发送一条文本，原卡片收到终态回复。
3. 主动发送先 dry-run，再向明确测试目标发送一次。
4. 执行 `restart` 后 PID 更新且队列请求 ID 不变化。
5. 注销并重新登录，计划任务自动恢复，23 个固定事件消费者重新连接。
6. 睡眠/唤醒后桥恢复在线；存在活动任务连接时系统休眠抑制生效。
7. 若配置命名管道，完成一次既有 Desktop 任务的继续、修正和停止；未配置则把这一项记录为可选未验收。
