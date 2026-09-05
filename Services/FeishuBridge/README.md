# Feishu Bot Bridge

> 冻结离线基线：本目录保留 Node 实现用于契约回放，不进入 macOS 或
> Windows 安装包、生产运行入口和自动回退链路。当前实现位于 `Core`，
> 两个平台均只运行 Go 飞书服务。

在 macOS 或 Windows 本机运行的飞书工作桥：使用飞书官方 SDK 的单一长连接接收入站事件，通过固定版本 `lark-cli` 执行受控飞书能力，并把授权消息连接到本机 Codex。

核心能力包括消息与群聊、通讯录和群目录、Docs/Wiki/评论、Whiteboard/Mindnotes/Markdown、日历任务、Sheets/Base、会议妙记、妙搭 Apps、固定事件收件箱、Codex 任务卡和结果审计。

授权单聊中，每条不引用卡片的默认输入都会创建独立 Codex Thread；卡内追问和引用回复只续接对应卡片。默认对话与项目任务使用同一套卡片外壳：未绑定项目时标题为“Codex 对话”，同一 Thread 形成可验证的 KSF 项目投影后，原卡标题改为项目名并升级为任务控制卡。发送“我需要继续完成某项目”会主动触发精确匹配和归属验真；普通讨论逐步落到项目时也只依据 KSF 权威投影升级，不根据正文猜测，不自动创建或静默切换项目。

## 安装入口

- 完整安装：[docs/installation.md](docs/installation.md)
- macOS/Windows 配置：[docs/configuration.md](docs/configuration.md)
- Windows 宿主机制：[docs/windows-runtime.md](docs/windows-runtime.md)
- 统一客户端：[docs/bridge-client.md](docs/bridge-client.md)
- 能力矩阵：[docs/capability-matrix-1.0.0.md](docs/capability-matrix-1.0.0.md)
- 项目内 Codex Skill：[.agents/skills/feishu-bridge/SKILL.md](.agents/skills/feishu-bridge/SKILL.md)

克隆后先执行：

```bash
npm ci
npm run install:requirements
npm test
```

项目内启动 Codex 时，`.agents/skills/feishu-bridge` 会作为 REPO Skill 自动发现。需要在其他项目调用时：

```bash
npm run skill:install
npm run skill:check
```

也可以在克隆后的仓库中直接交给 Codex 安装：

```text
$feishu-bridge 请按 docs/installation.md 在当前机器安装飞书桥。先完成只读预检，列出需要我在飞书后台手动完成的权限、事件和发布步骤；不要发送真实消息或写入真实文档。
```

飞书后台配置并发布后，再告诉 Codex：

```text
$feishu-bridge 飞书后台权限、事件和应用版本已配置并发布。继续完成本机凭据、常驻服务和 USER Skill 安装，最后运行只读验收；真实写入仍只做 dry-run。
```

## 安全边界

- 消息写入 outbox，Docx 正文写入 docbox，其他固定写动作进入 actionbox。
- 每次真实写入都需要本轮明确授权，并先运行请求级 dry-run。
- 高影响写入要求确认、预检或快照，并在写后复读。
- 不开放 Approval、删除清空、权限角色成员管理、Wiki 移动、任意 OpenAPI/EventKey、实时会议控制、Base/Apps 自动化或后台全量抓取。
- App Secret、token 和真实飞书 ID 不进入仓库、Skill 或公开审计。

## 当前版本

- Bridge / capability：`1.0.0`
- npm package：`1.2.0`
- lark-cli：`1.0.92`
- Feishu Node SDK：`1.73.0`
- 固定注册能力：219 项
- 固定非 Approval Event：23 项

本项目不会自动修改飞书控制台权限，也不会在安装时发送真实消息或写入真实文档。
