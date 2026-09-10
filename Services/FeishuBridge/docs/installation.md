# 飞书桥与 Codex Skill 完整安装指南

> 迁移期维护者参考：本文保留独立 Node 桥的历史部署与回退步骤，不是
> KSFAssistant 普通用户安装入口，也不得作为公开预览包的用户指引。

本文仅适用于维护者验证生产回退实现。KSFAssistant 普通用户无需独立安装或常驻飞书桥。

## 1. 安装结果与安全模型

安装会建立：

- 一个飞书企业自建应用及机器人。
- 一份 `lark-cli` 安全 profile，用于所有飞书 API 读取和写入。
- 一条官方 Node SDK 入站长连接；不会并行启动 `lark-cli event consume`。
- macOS LaunchAgent 或 Windows 当前用户计划任务。
- outbox、docbox、actionbox 三条受控写入队列。
- 平台私有目录：macOS 与 Windows 均为 `~/.config/feishu-bridge`；Windows 使用当前用户 ACL。
- 仓库内 `.agents/skills/feishu-bridge` REPO Skill，可选复制到 `~/.agents/skills/feishu-bridge`。

安装程序不会替用户开启飞书权限、发布应用、扩大入站白名单、发送真实消息或删除现有数据。

## 2. 前置条件

共同要求：

- Git。
- Node.js 20 或更高版本。
- npm。
- 当前用户可调用的 Codex CLI；需要桌面任务连接时再安装并登录 Codex Desktop。
- 一个有权限创建企业自建应用、配置权限和发布版本的飞书账户。

检查：

```bash
git --version
node --version
npm --version
codex --version
```

Windows 使用 PowerShell 5.1 或 7，并要求当前用户可使用系统 `ScheduledTasks` 模块。不要在管理员账户中配置 lark-cli、再用普通账户运行桥；凭据和计划任务必须属于同一登录用户。

## 3. 克隆与安装依赖

```bash
git clone <repository-url> feishu-bot-bridge
cd feishu-bot-bridge
npm ci
```

确认依赖版本和基础测试：

```bash
npx lark-cli --version
npm test
node --check bot-bridge.js
node --check scripts/bridge-client.js
```

预期 `lark-cli version 1.0.92`。不要在没有重新验证能力注册表、事件帧和参数快照的情况下单独升级 `lark-cli` 或官方 SDK。

### 3.1 交给 Codex 执行安装

克隆后从本仓库目录启动 Codex，REPO Skill 会被自动发现。可直接发送：

```text
$feishu-bridge 请按 docs/installation.md 在当前机器安装飞书桥。先完成只读预检，列出需要我在飞书后台手动完成的权限、事件和发布步骤；不要发送真实消息或写入真实文档。
```

Codex 可以安装依赖、生成精确权限/事件清单、创建本机配置、安装常驻服务和 USER Skill，并执行只读检查。飞书开放平台的应用创建、权限审批、事件选择、版本发布和浏览器 OAuth 仍需由当前租户的有权用户确认。

完成飞书后台步骤后，继续发送：

```text
$feishu-bridge 飞书后台权限、事件和应用版本已配置并发布。继续完成本机凭据、常驻服务和 USER Skill 安装，最后运行只读验收；真实写入仍只做 dry-run。
```

## 4. 创建飞书应用

在飞书开放平台创建“企业自建应用”：

1. 启用机器人能力。
2. 保存 App ID；App Secret 只进入 lark-cli 安全存储和平台凭据存储，不写入 `.env.local`。
3. 在权限管理中为应用申请桥需要的权限。
4. 在事件与回调中选择“使用长连接接收事件”。
5. 添加桥的固定事件目录。
6. 启用卡片回调，并确认 `card.action.trigger` 可达。
7. 创建并发布应用版本。权限或事件修改后必须再次发布。

飞书控制台名称可能随版本调整；判断是否真的生效，以后续 `permissions`、`doctor` 和真实入站验收为准。

### 4.1 导出精确权限与事件清单

权限和 EventKey 由代码固定注册，不在文档维护第二份易过期副本：

```bash
npm run install:requirements
node scripts/install-requirements.js scopes --identity bot --format lines
node scripts/install-requirements.js scopes --identity user --format lines
node scripts/install-requirements.js events --format lines
```

- 基础连接只要求消息、卡片、按钮回调与附件链路所需的 6 项 Bot 权限；其余 Bot scope 属于具体能力目录。
- User 清单中的 119 项是当前版本可用能力的权限上限，不是首次连接或一次性 OAuth 的要求。首次缺少本人标识时只申请 `contact:user.base:readonly`；文档、日历等能力在首次使用时按命令描述符申请缺项。
- 固定事件为 23 项非 Approval EventKey。
- 多余权限不会自动成为桥能力。

把上述输出作为本版本的能力目录与管理员预配置参考，不要将 User 全集作为首次授权清单。不要申请 Approval，不要因控制台中存在额外权限就扩展桥能力。

### 4.2 通讯录与知识库附加配置

要使用“按姓名发送”：

- 将应用通讯录可见范围设为所需组织范围；全量缓存需要全组织可见。
- 授予输出清单中的只读用户和部门权限。
- 不需要通讯录写权限、手机号或邮箱读取权限。

要让机器人写知识库：

- 在目标 Wiki 空间成员管理中添加机器人应用。
- 赋予编辑权限。
- 个人云空间仍使用用户身份，知识库固定使用机器人身份；权限失败不会跨身份回退。

## 5. 初始化 lark-cli 安全凭据

扫码 OAuth 只能授权一个已经配置到本机的应用，不能让飞书平台把既有应用的 App Secret 透露给本机。普通用户由 KSFAssistant 软件内向导创建专用应用或安全接入已有应用；本页保留的命令仅供迁移期开发和故障定位。

本机安全导入使用 stdin 或私有 JSON 文件，Secret 不进入聊天、命令行、`.env.local`、Skill 或版本化文件：

```bash
printf '%s' '{"appId":"<existing_app_id>","appSecret":"<existing_app_secret>","brand":"feishu"}' \
  | npm run bridge -- auth configure-existing --payload-file -
```

Windows PowerShell 也使用 stdin，不把 Secret 放进参数：

```powershell
'{"appId":"<existing_app_id>","appSecret":"<existing_app_secret>","brand":"feishu"}' |
  npm run bridge -- auth configure-existing --payload-file -
```

在 Windows 上，该命令会同时把官方 SDK 长连接需要的同一应用凭据写入当前用户 DPAPI；macOS 上由 `lark-cli`/Keychain 维护。导入完成后再发起用户 OAuth 二维码。

```bash
npm run bridge -- auth start-config
```

`auth start-config` 默认只接受已经存在的安全配置路径；未检测到既有配置时会 fail-closed，并说明需要既有 profile、Agent 绑定或一次性本机安全导入。只有用户明确要创建新应用时，才允许：

```bash
npm run bridge -- auth start-config --create-new
```

如果显式新建流程返回 `qrPath`，打开或扫描该二维码完成飞书开放平台配置。复用既有机器人时，不使用这个新建二维码。完成既有配置导入后验证：

```bash
npx lark-cli auth status --json
```

后备路径仍可在人工终端中交互复用既有应用，但不要让 AI 记录真实 App ID、App Secret 或配置原文。Windows 官方 SDK 长连接需要同一应用凭据；如需只写 DPAPI 后备凭据，可使用交互脚本，不得把 Secret 写入命令行：

```powershell
.\windows\Set-FeishuBridgeCredential.ps1 -AppId '<app_id>'
```

该后备脚本只在当前 Windows 用户的 DPAPI 文件中保存凭据；安装器、Skill 和文档不得保存明文 Secret。

## 6. 完成用户 OAuth

完整能力要求飞书应用后台权限和个人 OAuth 两层同时具备。使用桥客户端发起精确 user scope 的设备授权，并返回二维码：

```bash
npm run bridge -- auth start-user
```

扫码授权完成后，回到本机执行收尾：

```bash
npm run bridge -- auth finish-user
```

`start-user` 默认使用代码导出的完整 user scope；如只做轻量验证，可用 `--scope recommend`。浏览器完成授权后验证：

```bash
npx lark-cli auth status --verify --json
npm run bridge -- permissions
```

`permissions.identities.user.missing` 必须为空；`application.complete` 与 `oauth.complete` 都必须为 `true`。如果刚增加权限，先确认应用版本已发布，再重新执行 OAuth。

## 7. 创建本机配置

macOS：

```bash
cp .env.example .env.local
chmod 600 .env.local
```

Windows PowerShell：

```powershell
Copy-Item .env.example .env.local
```

编辑 `.env.local`。首次安装至少确认：

```text
LARK_CLI_PROFILE=default
FEISHU_EVENT_CONSUMER_ENABLED=true
FEISHU_EVENT_TRANSPORT=official-sdk
FEISHU_DIRECT_ALLOWED_OPEN_IDS=<允许操控机器人的本人 open_id>

FEISHU_GROUP_ENABLED=false
FEISHU_OUTBOUND_ENABLED=false
FEISHU_OUTBOUND_DRY_RUN=true
FEISHU_DOCBOX_ENABLED=false
FEISHU_DOCBOX_DRY_RUN=true
FEISHU_ACTIONBOX_ENABLED=false
FEISHU_ACTIONBOX_DRY_RUN=true
```

不要把 App Secret 或 OAuth token 写入该文件。

### 7.1 获取本人的 open_id

OAuth 完成后，可用当前用户身份查询自己：

```bash
npx lark-cli contact +search-user --user-ids me --as user --json
```

把结果中的本人 `open_id` 写入本机 `.env.local` 的 `FEISHU_DIRECT_ALLOWED_OPEN_IDS`，不要提交、截图或复制到项目文档。

### 7.2 逐步启用能力

先保持所有写队列关闭和 dry-run 打开。基础入站稳定后再按需启用：

```text
FEISHU_OUTBOUND_ENABLED=true
FEISHU_DOCBOX_ENABLED=true
FEISHU_ACTIONBOX_ENABLED=true

FEISHU_OUTBOUND_WAKE_ENABLED=true
FEISHU_DOCBOX_WAKE_ENABLED=true
FEISHU_ACTIONBOX_WAKE_ENABLED=true
```

首次真实验收前继续保持三个 `*_DRY_RUN=true`。请求级 `--dry-run` 始终优先，可在全局真实写入开启后继续安全演练。

通讯录和群目录按需启用：

```text
FEISHU_DIRECTORY_ENABLED=true
FEISHU_GROUP_DIRECTORY_ENABLED=true
```

群目录只同步机器人已经加入的群。入站群聊权限与主动出站相互独立；不要因为出站开放而清空入站限制。

完整字段解释见 `docs/configuration.md`。

## 8. 初始化客户端与目标

```bash
npm run bridge -- targets init
npm run bridge -- targets list
```

建立本人别名时，真实 ID 从 stdin 输入：

macOS：

```bash
printf '%s' '<open_id>' | npm run bridge -- targets set message 我 --type open_id --value-file -
```

Windows PowerShell：

```powershell
'<open_id>' | npm run bridge -- targets set message 我 --type open_id --value-file -
```

启用目录后可同步并检查：

```bash
npm run bridge -- targets directory sync
npm run bridge -- targets directory status
npm run bridge -- targets group-directory sync
npm run bridge -- targets group-directory status
```

目录和绑定保存在平台私有 `client.json`，不会进入仓库。

## 9. 启动 macOS 常驻服务

先手动验证：

```bash
npm run start:awake
```

确认能启动后按 `Control-C` 停止，再安装 LaunchAgent：

```bash
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs/feishu-bot-bridge"
cp launchd/com.example.feishu-bot-bridge.plist "$HOME/Library/LaunchAgents/com.example.feishu-bot-bridge.plist"
```

编辑复制后的 plist：

- Node 可执行文件绝对路径。
- `scripts/start-bridge.js` 绝对路径。
- `WorkingDirectory`。
- `HOME`。
- `FEISHU_BRIDGE_PROJECT_ROOT`。
- stdout/stderr 日志路径。

保持 plist 的 Label 和 `.env.local` 的 `FEISHU_BRIDGE_LAUNCHD_LABEL` 一致，然后：

```bash
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.example.feishu-bot-bridge.plist"
launchctl kickstart -k "gui/$(id -u)/com.example.feishu-bot-bridge"
npm run bridge -- status
npm run bridge -- doctor
```

如果使用自定义 Label，同时修改 plist 文件、plist 内 Label 和 `.env.local`，三者必须一致。

## 10. 启动 Windows 常驻服务

在普通用户 PowerShell 中：

```powershell
npm run windows:install
npm run bridge -- status
npm run bridge -- doctor
Get-ScheduledTask -TaskName FeishuBotBridge
```

安装器会：

- 创建并收紧 `%USERPROFILE%\.config\feishu-bridge` ACL；如存在旧 `%LOCALAPPDATA%\FeishuBridge`，只复制尚未迁移的私有文件，不覆盖或删除旧目录。
- 注册当前用户登录时启动的 `FeishuBotBridge` 计划任务。
- 失败后一分钟重试且不设置任务执行时长上限。
- 立即启动任务。

自定义任务名时，直接调用安装脚本传入 `-TaskName`，并在 `.env.local` 设置相同的 `FEISHU_BRIDGE_WINDOWS_TASK_NAME`：

```powershell
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File .\windows\Install-FeishuBridge.ps1 -TaskName Team.FeishuBotBridge
```

普通飞书消息到 Codex 不依赖 Desktop IPC。只有控制一个已经打开的 Codex Desktop 任务时，才需要配置当前用户私有 Named Pipe；详情见 `docs/windows-runtime.md`。

## 11. 安装 Codex Skill

仓库已经包含：

```text
.agents/skills/feishu-bridge/
```

### 11.1 REPO Scope

在本仓库或其子目录启动 Codex，无需复制；Codex 会发现该 REPO Skill。可显式使用：

```text
$feishu-bridge 检查当前桥状态
```

### 11.2 USER Scope

需要在其他项目使用时，从本仓库执行：

```bash
npm run skill:install
npm run skill:check
```

安装器把 Skill 复制到：

```text
~/.agents/skills/feishu-bridge
```

并写入只存在于本机安装副本的 `installation.json`，用于定位当前桥仓库。安装器拒绝覆盖同路径的非本项目 Skill。Codex 通常会自动发现变更；未出现时重启 Codex。

项目更新后重新同步 USER Skill：

```bash
npm ci
npm run skill:install
```

移除 USER Skill：

```bash
npm run skill:uninstall
```

该操作只删除由本项目安装器管理的 USER Skill 副本，不删除桥仓库、配置、队列、凭据或审计。

## 12. 验收顺序

### 12.1 只读验收

```bash
npm run bridge -- capabilities
npm run bridge -- permissions
npm run bridge -- status
npm run bridge -- doctor
```

通过标准：

- 版本与依赖匹配。
- 服务已加载并运行。
- 官方 SDK 连接为 connected。
- 固定事件没有 missing 或 unsupported key。
- `permissions` 没有 user 缺口，bot ready。
- 私有目录和配置权限安全。
- 三条队列没有硬错误。

### 12.2 入站验收

由白名单内用户给机器人发送：

```text
cmd ping
```

应收到 `pong`。再发送一条普通文本，确认处理中与最终结果更新在同一张卡片。需要卡片回调时，验证快速追问；再引用回复任务卡发送一张图片，确认附件进入对应任务。

### 12.3 出站验收

先启用 outbox，但保持全局 dry-run：

```bash
printf '%s' '飞书桥安装验收' | npm run bridge -- send --target 我 --format text --content-file - --dry-run
```

确认返回 `dry_run`、日志和审计正确后，由用户明确授权测试目标与正文，再关闭全局 dry-run、重启服务并正式发送。不要把 dry-run 描述为已送达。

### 12.4 文档与 actionbox

对专用测试资产依次执行：

1. 请求级 dry-run。
2. 创建或普通追加。
3. 读取结果或 revision。
4. 验证审计和脱敏。

覆盖、替换、结构变化和其他高影响动作必须另行确认。生产资产不用于首次测试。

## 13. 更新与回退

更新：

```bash
git pull
npm ci
npm test
npm run skill:install
npm run bridge -- restart
npm run bridge -- doctor
```

macOS 停止服务：

```bash
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.example.feishu-bot-bridge.plist"
```

Windows 停止并注销服务：

```powershell
npm run windows:uninstall
```

Windows 卸载保留私有数据和 DPAPI 凭据。macOS bootout 也不删除日志、Keychain、客户端配置或目录缓存。确认不再需要后，由用户自行处理私有数据；仓库脚本不自动删除历史数据。

## 14. 常见问题

### Skill 找不到桥项目

```bash
npm run skill:install
npm run skill:check
```

或设置当前会话的 `FEISHU_BRIDGE_PROJECT_ROOT`。不要手工把作者机器路径写进 Skill。

### `permissions` 显示缺口

确认应用后台已开权限、创建并发布新版本，然后重新执行对应用户 OAuth。权限开通但未发布仍不会生效。

### Wiki 返回 `131006`

在目标知识库的成员管理中添加机器人并授予编辑权限；不要回退为 user 身份重试。

### 卡片回调出现 `200671`

检查 `card.action.trigger`、卡片回调配置、应用版本发布状态以及官方 SDK 长连接。桥会先返回轻量 toast，再异步处理；不要启动第二个事件消费者。

### macOS `status` 找不到 LaunchAgent

检查 plist Label 与 `FEISHU_BRIDGE_LAUNCHD_LABEL` 是否完全一致，再使用 `launchctl print gui/$(id -u)/<label>`。

### Windows `status` 找不到计划任务

检查 `FEISHU_BRIDGE_WINDOWS_TASK_NAME` 与安装时 `-TaskName` 是否一致，并运行：

```powershell
Get-ScheduledTask -TaskName FeishuBotBridge
```

### `doctor.health=degraded`

这是可运行警告，不等于桥故障。根据具体 warning 处理缓存过期或队列体积；只有 `failed` 才视为不可用。

## 15. 安装完成检查表

- [ ] 依赖安装完成且测试通过。
- [ ] 飞书应用启用机器人。
- [ ] 精确权限清单已开通并发布。
- [ ] 23 个固定事件已配置，Approval 未接入。
- [ ] lark-cli bot profile 可用。
- [ ] user OAuth application/oauth 两层完整。
- [ ] `.env.local` 未被 Git 跟踪。
- [ ] 入站本人 open_id 已加入白名单。
- [ ] macOS LaunchAgent 或 Windows 计划任务运行。
- [ ] `doctor` 没有硬错误。
- [ ] `cmd ping` 收到 pong。
- [ ] 出站 dry-run 通过。
- [ ] 真实写入只在明确授权后执行。
- [ ] REPO Skill 可发现；需要跨项目时 USER Skill 已安装。
