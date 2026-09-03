# 飞书通讯录缓存与按姓名发送 ExecPlan

## 目标

让用户在明确给出收件人姓名与文本内容时，通过飞书桥按姓名解析唯一用户并继续走既有 outbox 发送。桥使用机器人应用的只读通讯录权限，定期缓存应用可见范围内的组织成员；同名用户首次必须由用户选择，选择结果写入本机安全目标配置，后续可直接发送。

## 当前状态

- `scripts/bridge-client.js` 只接受目标别名或显式 `chat_id:` / `open_id:`。
- 主动出站没有本地目标白名单，但仍保留逐次用户授权、outbox、幂等、dry-run、结果查询和审计。
- `lib/lark-cli-runner.js` 是飞书 API 的统一执行层，默认使用 bot identity。
- 本机客户端配置位于 `~/.config/feishu-bridge/client.json`，目录权限 `0700`、文件权限 `0600`。
- KSF 的 `ksf-feishu-bridge` 处于 `trial`，版本 `0.2.0`，当前明确禁止按姓名猜测目标。
- 工作树包含此前尚未提交的桥客户端、出站策略与文档改动；本任务必须在其上增量实现，不覆盖或回退已有变更。

## 边界

飞书桥负责：

- 通过 `lark-cli` 的只读 OpenAPI 获取应用可见部门和成员。
- 分页、去重、在职状态过滤、安全缓存、精确姓名索引和同名候选。
- 在 outbox 入队前把唯一姓名或已确认姓名绑定解析为 `open_id`。
- 定期刷新、缓存状态、权限错误报告和脱敏诊断。

飞书桥不负责：

- 写入或修改飞书通讯录。
- 读取手机号、个人邮箱、性别等与发送无关的字段。
- 模糊匹配后自动选择用户，或在同名时默认选择第一项。
- 按名称搜索群聊、自动拉群、媒体消息或绕过 outbox 发送。
- 自动修改飞书开发者后台的权限与应用可见范围。

## 数据与接口设计

### 飞书读取链路

- 固定使用 bot identity。
- 读取 `/open-apis/contact/v3/departments/:department_id/children`，从根部门 `0` 获取可见部门。
- 逐部门读取 `/open-apis/contact/v3/users`，手动按 `page_token` 分页，避免依赖 CLI 输出合并格式。
- 用户以 `open_id` 去重；部门路径仅用于同名区分。
- 需要应用全组织可见范围，以及只读通讯录权限。优先使用 `contact:contact:readonly_as_app`；细分权限模式至少覆盖用户基础信息、用户部门关系、部门基础信息和用户 ID。

### 本机缓存

- 默认路径：`~/.config/feishu-bridge/directory.json`。
- 目录 `0700`，文件 `0600`；拒绝符号链接，使用临时文件、`fsync` 和原子 rename。
- 缓存字段只包含：schema、同步时间、来源、完整性、用户 `open_id`、姓名/英文名、部门路径和活动状态。
- 不保存手机号、邮箱或其他个人资料；不把缓存内容写入仓库、KSF、JSONL 业务日志或 Markdown 审计。
- 同步失败保留最后一次成功缓存，不用失败结果覆盖。

### 姓名解析

- 名称规范化使用 Unicode NFKC、首尾空白清理、连续空白折叠和拉丁字符小写化。
- 解析顺序：本机已确认别名/姓名绑定 → 唯一精确姓名/英文名 → 返回同名候选或未找到。
- 禁止模糊结果自动发送。
- 同名候选只输出显示名、部门路径和 `open_id` 脱敏指纹。用户选择后把姓名绑定写入 `client.json`，不在命令参数或输出中暴露真实 ID。

### 刷新与失效

- `FEISHU_DIRECTORY_ENABLED=false` 为仓库安全默认值。
- 启用后桥启动时刷新，并按 `FEISHU_DIRECTORY_REFRESH_MS` 定时刷新，默认 6 小时。
- `FEISHU_DIRECTORY_MAX_AGE_MS` 默认 24 小时。
- 按姓名发送发现缓存缺失或过期时先尝试一次同步；同步失败则 fail closed，不入 outbox。显式 ID 和既有普通别名继续可用。

## CLI 改动

- `targets directory status`
- `targets directory sync`
- `targets directory search --query <name>`
- `targets directory bind --name <name> --candidate <fingerprint>`
- `send --target-name <name> --text-file <file|->`

消息正文继续只从 stdin 或文件读取。姓名可以作为参数，但真实目标 ID 不能出现在命令行或输出中。

## 安全策略

- 通讯录能力只读；不加入任意 OpenAPI 透传入口。
- 仅允许上述两个 contact GET endpoint，所有调用集中在 `lib/lark-cli-runner.js`。
- 同名、未找到、缓存过期、权限不足和数据损坏都在 outbox 入队前停止。
- 发送仍要求用户本轮明确给出动作、姓名和正文；目录缓存本身不构成发送授权。
- outbox 请求只保存解析后的目标 ID，不保存查询姓名或候选列表。
- 状态和诊断只输出数量、时间、是否过期、权限模式和脱敏错误。

## 实施进度

- [x] 用户确认全量通讯录只读缓存、唯一姓名直发、同名首次选择后记住绑定。
- [x] 核对本机 `lark-cli` 支持 contact v3 GET、bot identity、手动分页参数。
- [x] 实现通讯录缓存与纯解析逻辑。
- [x] 接入统一 lark-cli runner。
- [x] 接入 bridge-client 命令和按姓名发送。
- [x] 接入桥启动刷新和定时刷新。
- [x] 更新环境模板、技术文档和 KSF trial Skill。
- [x] 完成单元、集成、Skill 与 KSF runtime 验证。
- [ ] 在用户完成飞书权限与可见范围配置后，执行只读真实同步冒烟测试。

## 验证

- Node 内置测试覆盖部门与用户分页、跨部门去重、活动状态、Unicode 名称规范化、唯一/同名/未找到、姓名绑定、缓存权限、原子写入、过期和损坏。
- 假 `lark-cli` 验证只调用允许的 GET endpoint，使用 bot identity，不读取或输出额外字段。
- 验证同名、模糊匹配、过期且刷新失败时没有 outbox 记录。
- 验证唯一姓名解析后仍使用原 outbox 请求结构、请求 ID、同步等待和脱敏输出。
- 运行 `npm test`、`node --check`、Skill 快速校验和 KSF `--verify-runtime`。
- 生产环境第一步只运行 `status` / `doctor`；用户授权后只同步并搜索一个姓名，不自动发送真实消息。

## 回滚

- 设置 `FEISHU_DIRECTORY_ENABLED=false` 并重启桥，立即停止后台同步和按姓名发送；显式 ID/别名、outbox、docbox 不受影响。
- 删除 `~/.config/feishu-bridge/directory.json` 可清除缓存；删除前必须再次确认具体目标。
- 删除由目录选择建立的姓名别名，可恢复同名未绑定状态。
- 回退 `lib/contact-directory.js`、runner、CLI、配置文档和 Skill `0.3.0` 相关增量即可恢复 `0.2.0` 行为，不需要修改入站权限或 outbox 格式。

## 发现与决策记录

- 2026-09-01：本机 `lark-cli 1.0.45` 的 `contact +search-user` 需要 user OAuth，只适合按需搜索；全量后台缓存改用 bot identity 的 contact v3 只读 API。
- 2026-09-01：用户明确接受高权限闭环，但只读通讯录足以完成目标，因此不申请通讯录写权限，也不缓存邮箱和手机号。
- 2026-09-01：唯一姓名可以在本轮授权内直发；同名是误发风险，必须首次选择并持久化绑定。
- 2026-09-01：首次实现测试发现新机器上锁文件先于安全目录创建会失败；已把 `0700` 目录创建前置，并增加回归测试。
- 2026-09-01：当前 `lark-cli auth check` 不能用来证明 bot 应用已获得 contact scopes，生产权限以飞书后台配置和首次只读同步结果为准；不得因检查不确定而自动申请更广权限。
- 2026-09-01：首次真实同步在读取根部门时返回飞书 `40004 no dept authority`，安全停止且未生成缓存。下一步需要在飞书开发者后台授予全组织只读通讯录/部门读取权限并确认应用可见范围；不通过缩减到不完整根部门列表规避权限错误。
