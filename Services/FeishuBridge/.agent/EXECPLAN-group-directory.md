# 群目录与按唯一群名发送 ExecPlan

## 目标

让用户在本轮明确给出群名称与文本内容时，通过飞书桥把机器人已加入的群解析为唯一 `chat_id`，再继续走既有 outbox 发送。用户不需要手工查询或维护 `chat_id`。

## 当前状态

- 现有 outbox 已允许任意显式 `chat_id`，并保留逐次发送授权、幂等、脱敏和审计。
- `bridge-client` 已支持显式目标、本机别名和只读用户通讯录的唯一姓名解析。
- `lark-cli im +chat-list --as bot` 可只读列出机器人已加入的群。2026-09-01 的只读实测已经唯一找到目标群，当前应用权限足够。
- `ksf-feishu-bridge` 当前为 `0.3.0`、System 级、trial；当前契约明确拒绝按群名搜索。

## 边界

飞书桥负责：

- 只读分页获取机器人已加入的群。
- 把群名称和 `chat_id` 写入本机安全缓存。
- 唯一精确群名或已确认同名绑定的解析。
- 解析后通过既有 outbox 提交、等待和审计。

飞书桥不负责：

- 搜索机器人未加入的公开群或自动入群。
- 模糊群名自动选择、同名默认选第一项或根据活跃度猜测。
- 修改群名称、群成员、群权限或机器人可用范围。
- 改变单聊/群聊入站权限、开放群内 Codex 对话或任务模式。
- 绕过 outbox 直接发送。

## 改动方案

### 数据与配置

- 新增 `lib/group-directory.js`，独立承担分页、缓存、精确解析、搜索、同名绑定和失效判断。
- 缓存默认位于 `~/.config/feishu-bridge/group-directory.json`，状态位于同目录的 `group-directory-state.json`；目录权限 `0700`、文件权限 `0600`。
- 缓存只保存发送所需的群名称、`chat_id` 和必要的非敏感区分字段，不保存成员列表或消息内容。
- `client.json` 增加 `groupNameBindings`；既有配置向后兼容加载。
- 新增环境变量：
  - `FEISHU_GROUP_DIRECTORY_ENABLED=false`
  - `FEISHU_GROUP_DIRECTORY_CACHE_PATH`
  - `FEISHU_GROUP_DIRECTORY_STATE_PATH`
  - `FEISHU_GROUP_DIRECTORY_REFRESH_MS=1800000`
  - `FEISHU_GROUP_DIRECTORY_MAX_AGE_MS=7200000`
  - `FEISHU_GROUP_DIRECTORY_PAGE_SIZE=100`
- 群目录服务纳入桥进程生命周期：启动后立即同步，随后每 30 分钟刷新一次；缓存超过 2 小时仍未成功刷新时，`doctor` 才报告 `degraded`。
- `im.chat.updated_v1`、群解散、机器人入群和退群事件触发一次防抖刷新，避免群改名或可见群集合变化长期滞后。

### 飞书读取

- 在 `lib/lark-cli-runner.js` 增加应用身份 `GET /open-apis/im/v1/chats` 的数组参数封装。
- 只读取机器人已加入的群并处理全部分页；分页 token 异常或超过上限时失败，不覆盖最后一次成功缓存。
- 不使用可返回未加入公开群的模糊搜索结果作为发送目标。

### CLI

- 增加：
  - `targets group-directory status|sync`
  - `targets group-directory search --query <name>`
  - `targets group-directory bind|unbind --name <name> [--candidate <fingerprint>]`
  - `send --target-group-name <exact-name> --text-file ...`
- `--target`、`--target-name`、`--target-group-name` 三者互斥。
- 唯一精确群名直接解析；同名返回脱敏候选并停止；模糊或未找到不入 outbox。
- 已绑定群改名、机器人退群或缓存中消失时返回 `binding_stale`，不迁移到另一个同名群。

### Skill 与长期入口

- 原地把 `.agents/skills/ksf-feishu-bridge` 从 `0.3.0` 更新为 `0.4.0`，保持 `stage: trial` 和现有 `trial-evidence.yaml`。
- 更新触发、能力边界、消息和运维参考、测试与行为评测；真实 ID 不进入 Skill 或 KSF。
- 更新飞书桥项目记忆根卡中的稳定能力边界，不写运行态群名或目标 ID。

## 安全策略

- 用户每次仍必须明确给出动作、群名和正文；缓存发现不等于长期发送授权。
- 群名必须唯一精确命中或命中用户确认过且仍有效的绑定。
- 群 ID、缓存和绑定只进入本机安全配置，不进入命令行、Skill、KSF、普通日志或审计正文。
- 正文继续从 stdin 或文件进入，不拼入 shell 命令。
- 平台不可达、机器人退群、权限失败、同名、缓存过期刷新失败时停止，不自动更换目标或重复发送。
- 现有出站 dry-run、请求 ID、去重、wake fallback、终态查询和审计保持不变。

## 验证步骤

1. RED：分页与去重、唯一精确匹配、同名、模糊、失效绑定、安全缓存、过期刷新失败。
2. RED：CLI 群目录 status/sync/search/bind/unbind、三种目标互斥、按群名入队与脱敏输出。
3. GREEN：完成最小实现，逐项运行目标测试。
4. 回归：运行项目全部 Node 测试、`node --check bot-bridge.js` 和既有 CLI 测试。
5. Skill：验证 trigger、non-trigger、success、failure、安全和 source closure；运行 KSF runtime 验证。
6. 生产只读：启用后同步群目录，确认“codex测试群”唯一精确命中且缓存权限安全。
7. 真实发送：仅按用户本轮授权向“codex测试群”发送一条测试文本，查询并交付 outbox 终态。
8. 生命周期刷新修复：验证启动立即同步、定时刷新、事件防抖、停止清理、同步失败保留旧缓存，以及 `doctor` 在成功刷新后恢复 `healthy`。

## 回滚

- 设置 `FEISHU_GROUP_DIRECTORY_ENABLED=false`，立即停止按群名解析；显式 ID、普通别名、个人姓名发送、入站权限和 docbox 不受影响。
- 回退 `group-directory`、runner、CLI、配置文档和 Skill `0.4.0` 增量即可恢复 `0.3.0` 行为。
- 安全缓存可保留为停用状态；如需删除，另行取得明确授权。
- 不执行 Git 暂存、提交或发布。

## 进度

- [x] 用户确认精确方案、真实测试目标和测试消息意图。
- [x] 只读验证当前机器人能唯一找到目标群，现有权限足够。
- [x] RED 测试证明新增行为尚不存在。
- [x] GREEN 实现与邻近回归通过。
- [x] Skill `0.4.0` 与 KSF runtime 验证通过。
- [x] 真实群目录同步和目标群发送完成。
- [x] 群目录常驻刷新与事件防抖修复完成，166 项 Node 回归、JavaScript 语法和差异检查通过。
- [x] 重启桥并验证群目录启动同步完成；群缓存 `stale=false`、无错误，`doctor` 恢复 `healthy`，官方 SDK 23/23 事件与三类 wake 正常。

## 发现与决策

- 2026-09-01：使用机器人“已加入群列表”作为唯一发现来源，不使用可能包含未加入公开群的搜索结果，避免解析出不可发送目标。
- 2026-09-01：当前应用身份已经能读取机器人所在群，因此本次不申请新的飞书权限。
- 2026-09-01：群名解析与用户姓名解析分为两个独立缓存和绑定命名空间，避免把人员与群聊目标混合。
- 2026-09-01：群目录按需刷新而不是常驻后台同步；默认缓存 1 小时，CLI 发现过期时刷新，因此无需改变 LaunchAgent 或重启桥。
- 2026-09-01：RED 阶段群目录测试因模块不存在失败，CLI 测试因不识别 `--target-group-name` 和 `group-directory` 失败；最小实现后项目全量 40 项 Node 测试通过。
- 2026-09-01：`ksf-feishu-bridge` 原地升级为 `0.4.0` 并保持 trial，Skill 定向验证和 KSF runtime 验证通过；动态运行时清单无需手工修改。
- 2026-09-01：生产群目录成功同步 2 个机器人已加入群，“codex测试群”唯一精确命中；真实 outbox 请求到达 `sent` 终态，桥诊断包含 group directory 且全部通过。
- 2026-09-02：人工验收发现“按需刷新 + 1 小时过期”会让空闲运行中的桥周期性进入 `degraded`，而联系人目录在同一进程内会自动刷新。用户确认废止 2026-09-01 的按需刷新决定，改为“启动同步 + 30 分钟定时刷新 + 群变更事件防抖 + 按群名操作兜底刷新”；失败时仍保留最后一次完整缓存。
