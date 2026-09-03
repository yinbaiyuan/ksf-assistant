# 飞书桥安全能力补全与 1.0.0 升级 ExecPlan

## 目标

在当前 `0.9.0` 能力基线与既有未提交成果之上，分阶段补全用户确认的飞书安全能力子集，最终统一为：桥与 capability `1.0.0`、npm package `1.2.0`、KSF Skill `1.0.0`、`skillCompatibility: 1.0.x`。

本计划不接 Approval，不执行 Git 暂存、提交、推送或发布。

## 当前状态

- 基线提交：`7855f0dac5f756ee772abee32adcd73d51f896c5`，分支 `main`。
- 工作树包含既有未提交成果；本任务只做增量修改，不覆盖、不重置。
- 飞书 API 统一由 `lark-cli 1.0.92` 执行；官方 SDK `1.73.0` 只维持一条事件长连接。
- 现有 outbox、docbox、actionbox 与 schema v2 保持兼容。
- 开工前现有 Node 测试基线为 101 项通过。

## 架构与业务边界

- 飞书桥仍是本机通道和受控执行器，不承载审批或业务对象生命周期。
- 消息写入进入 outbox，Docx 写入进入 docbox，其他受控写动作进入 actionbox。
- 新能力必须出现在固定注册表中；不存在任意 shortcut、任意 OpenAPI 或任意 EventKey 透传。
- Event 只进入本机私有收件箱，供查询和后续显式工作流使用，不自动产生业务写入。
- Approval API 与 Approval Event 完全排除。
- 删除、清空、权限/角色/成员管理、Wiki 移动、后台全量抓取、实时会议控制、原始妙记媒体下载、自动化与秘密管理继续排除。

## 分阶段实现

### 0.10.0

- 引入固定能力注册表，声明身份、风险、输入约束、读取上限、预检、执行、复读和脱敏。
- 为 actionbox 增加本机 wake，并保留 polling fallback。
- 固定接入 `lark-cli 1.0.92` 实际目录中的 23 个非 Approval EventKey，使用官方 SDK 单一连接。原计划写 22 个，但目录共 25 个、排除 2 个 Approval 后实际为 23 个。
- 增加私有、按日分卷、按 `event_id` 去重的事件收件箱，以及固定类型订阅适配器。
- 增加 `events catalog|status|recent|get|watch` 客户端入口。

### 0.11.0

- 补齐消息与群聊、通讯录、Docs、Wiki、评论。
- 接入 Whiteboard、Mindnotes 与飞书 Markdown 的受控读写。
- 高影响写入必须确认，并执行预检/快照、写入、复读。

### 0.12.0

- 补齐日历、任务、Sheets、Base、会议与妙记的安全子集。
- 增加 `standup-report` 与 `meeting-summary` 只读组合工作流；默认不发布。

### 1.0.0

- 接入妙搭 Apps 开发主链路的固定读写与远端操作轮询。
- 统一版本、权限矩阵、doctor、技术规格、客户端文档和测试。
- 正式 KSF trial Skill 修改前展示精确差异、风险、验证与回退，取得独立确认后再修改。

## 安全策略

- 读能力必须限定目标、时间、页数或结果数量；禁止后台全量抓取。
- 所有写动作需要用户本轮显式授权、dry-run、幂等 ID、队列与审计。
- 高影响写入要求 `confirmHighImpact: true`，并且预检或快照失败时 fail-closed。
- 正文和结构化 payload 只从 stdin 或 `0600` 私有文件进入，不进入命令行、日志或审计正文。
- 输出默认脱敏真实目标 ID；缓存只保留目标解析必需字段。
- Event 收件箱只保存必要元数据与脱敏指纹，逐字稿事件不保存完整正文。
- 远端操作超时保留原请求 ID，不生成新 ID重试。

## 验证

- 每阶段运行 `node --check`、Node 内置测试、能力/权限矩阵与 doctor。
- 测试固定注册表白名单、身份、范围上限、stdin/私有文件、脱敏、幂等、wake fallback、同步/异步与远端轮询。
- 假 `lark-cli` 验证固定 argv、正文不泄漏、高影响预检与复读、排除动作不入队。
- Event 验证 23 个固定键、单连接、订阅/取消、重连、去重、脱敏和逐字稿裁剪。
- 真实写入仅使用用户确认的“Codex桥测试”资产和“codex测试群”，每次先 dry-run；测试资产保留、不自动删除。
- 权限或控制台配置不足时精确报告缺口并停止对应真实验收，不自动修改飞书权限。

## 回退

- 每阶段保留独立模块和能力开关；关闭对应事件、actionbox 或新增能力即可停止新路径。
- 代码回退以本计划开工时记录的 Git 状态与关键文件 SHA-256 为边界，绝不使用破坏性 Git 命令覆盖用户改动。
- KSF Skill 尚未获得独立确认时不修改；若后续确认并试运行，回退为恢复已冻结的 `0.7.1` Skill 文件与证据。

## 进度与发现

- 2026-09-02：完成 KSF 路由复验和工作树基线冻结。
- 2026-09-02：完成固定能力注册表、`lark-cli 1.0.92` 参数快照、actionbox 本机 wake、23 个固定事件与私有事件收件箱。
- 2026-09-02：完成 0.11.0、0.12.0 与 1.0.0 代码主链，注册 219 项能力；新增全注册写动作矩阵、事件、wake 与组合工作流测试。
- 2026-09-02：加入 outbox、docbox、actionbox 请求级 dry-run 与显式授权字段，并将 `create_document` 从自由 Codex 任务收敛为固定 `lark-cli docs +create → fetch` 链路；正文只走 stdin，运行日志私有且不保留目标 ID 或正文。
- 2026-09-02：生产烟测先 dry-run，再向“codex测试群”发送固定验收消息；已创建并绑定专用 Docx `Codex桥测试-Docx-1.0.0`。首次创建暴露的旧自由任务日志已就地脱敏并收紧为 `0600`；误创建的同名 Markdown 文件按测试资产保留，不执行删除。
- 2026-09-02：增加 schema v4 专用测试资产绑定。只有注册表审查过的创建结果字段可用 `--save-as 'Codex桥测试…'` 私下保存；后续 `asset:<别名>` 必须通过字段类型校验。raw ID 只存在 `0600` 的 `client.json`，公开输出只返回指纹。
- 2026-09-02：完成消息、Docx、文档内 Whiteboard、评论、Sheets、Base、Wiki、Calendar、Tasklist 与 Markdown 的“先 dry-run、再专用资产写入、再复读”生产烟测。首个 Tasklist 已创建但因实际结果键为 `guid` 未建立绑定，按边界保留；修正固定提取键后创建并绑定了可复用 Tasklist，不执行删除。
- 2026-09-02：Apps dry-run、异步原请求 ID 查询及两个只读组合工作流通过。当前 profile 缺少 Apps、Mindnotes、会议与 Minutes 等 31 项所需 scope，对应真实烟测按计划停止，未自动扩权。
- 设计收紧：文档内 Whiteboard 只接受 Mermaid/PlantUML/自包含 SVG，并经 docbox 的官方版本保护；Base 资源块明确排除 workflow 类型；Mindnotes 创建/更新按 `node_id` 分流并要求幂等 client token。
- 2026-09-02：最终全量回归 `132/132` 通过；全部 JavaScript 文件 `node --check` 与 `git diff --check` 通过。
- 2026-09-02：重启 `com.lawis.feishu-bot-bridge` 后最终 doctor 为 `healthy`：桥进程存活、官方 SDK 单连接已连接、23 个固定事件运行、三类 wake 均只监听本机、通讯录与群目录缓存新鲜，client config 权限为 `0600`。
- 2026-09-02：当前 user profile 在 119 项实现所需 scope 中仍缺 31 项，集中在会议室、用户/机器人搜索、用户态 IM/Feed、Mindnotes、Minutes、Apps 与 VC；对应真实验收按计划停止。额外已授权的删除、权限、角色、Workflow 等 scope 不会成为桥能力。
- 待记录：KSF Skill 独立确认门禁。
